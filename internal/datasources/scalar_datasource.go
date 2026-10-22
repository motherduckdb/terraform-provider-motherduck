package datasources

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlfunc"
)

type scalarDataSource struct {
	baseDataSource
	name        string
	description string
	query       string
	details     *scalarDetails
}

// scalarDetails adds computed attributes read from the first row of a table
// function. A session without the function, or a column the function does
// not return, leaves the attribute null instead of failing the read.
type scalarDetails struct {
	function string
	query    string
	attrs    []typedRowAttribute
	// unavailableMessage, when found in a query error, marks a session where
	// MotherDuck disables the function. The attributes are then left null
	// with a warning.
	unavailableMessage string
}

type scalarModel struct {
	Value types.String `tfsdk:"value"`
}

func NewCurrentUserDataSource() datasource.DataSource {
	return &scalarDataSource{
		name:        "current_user",
		description: "Reads the MotherDuck user for the current SQL session. `value` is the session user from `md_user()`. The other attributes come from `md_user_info()` and are null when the session does not expose them. When `md_user_info()` fails, they are null and the read adds a warning instead of failing.",
		query:       "SELECT md_user()",
		details: &scalarDetails{
			function:           "md_user_info",
			query:              "SELECT * FROM md_user_info()",
			unavailableMessage: "saas mode",
			attrs: []typedRowAttribute{
				{name: "user_id", description: "ID of the current user."},
				{name: "username", description: "Username of the current user."},
				{name: "org_id", description: "ID of the organization of the current user."},
				{name: "org_name", description: "Name of the organization of the current user."},
				{name: "org_type", description: "Plan type of the organization of the current user."},
				{name: "region", description: "MotherDuck region of the current session. Null when the MotherDuck client does not report it."},
			},
		},
	}
}

func NewVersionDataSource() datasource.DataSource {
	return &scalarDataSource{
		name:        "version",
		description: "Reads the MotherDuck version for the current SQL session.",
		query:       "SELECT md_version()",
		details: &scalarDetails{
			function: "md_pragma_version",
			query:    "SELECT * FROM md_pragma_version()",
			attrs: []typedRowAttribute{
				{name: "extension_hash", source: "md_extension_hash", description: "Build hash of the MotherDuck extension. Null when the session does not report it."},
			},
		},
	}
}

func NewLiveDucklingSizeDataSource() datasource.DataSource {
	return &scalarDataSource{name: "live_duckling_size", description: "Reads the live Duckling size for the current SQL session.", query: "SELECT type FROM md_live_duckling_size()"}
}

func (d *scalarDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name
}

func (d *scalarDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"value": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Scalar value returned by the MotherDuck SQL data source.",
		},
	}
	if d.details != nil {
		for _, detail := range d.details.attrs {
			attrs[detail.name] = schema.StringAttribute{
				Computed:            true,
				Sensitive:           detail.sensitive,
				MarkdownDescription: detail.description,
			}
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: d.description,
		Attributes:          attrs,
	}
}

func (d *scalarDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var value string
	err := retry.SQL(ctx, func() error {
		var readErr error
		value, readErr = client.ScalarString(ctx, d.query)
		return readErr
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck scalar data source", err.Error())
		return
	}
	if d.details == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, scalarModel{Value: types.StringValue(value)})...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("value"), types.StringValue(value))...)
	if resp.Diagnostics.HasError() {
		return
	}
	values := d.details.read(ctx, client, d.name, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, detail := range d.details.attrs {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(detail.name), values[detail.name])...)
	}
}

type scalarDetailsClient interface {
	sqlfunc.Exister
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}

// read returns one value per detail attribute. Every attribute is null when
// the function is missing, disabled, fails, or returns no rows. The detail
// attributes are optional extras next to `value`, so a failure here is a
// warning and never fails a configuration that reads only `value`.
func (s *scalarDetails) read(ctx context.Context, client scalarDetailsClient, dataSource string, diags *diag.Diagnostics) map[string]types.String {
	values := make(map[string]types.String, len(s.attrs))
	for _, detail := range s.attrs {
		values[detail.name] = types.StringNull()
	}
	leftNull := func(summary, reason string, err error) map[string]types.String {
		diags.AddWarning(summary, "The motherduck_"+dataSource+" data source left its "+s.function+" attributes null because "+reason+": "+err.Error())
		return values
	}
	available, err := sqlfunc.Exists(ctx, client, s.function)
	if err != nil {
		return leftNull("Unable to inspect MotherDuck SQL functions", "Terraform could not check whether the session exposes "+s.function, err)
	}
	if !available {
		return values
	}
	var rowsJSON string
	err = retry.SQL(ctx, func() error {
		var readErr error
		rowsJSON, readErr = client.QueryRowsJSON(ctx, s.query)
		return readErr
	})
	if err != nil {
		if s.unavailableMessage != "" && strings.Contains(strings.ToLower(err.Error()), s.unavailableMessage) {
			return leftNull("MotherDuck session details unavailable", "the current MotherDuck session disables that function", err)
		}
		return leftNull("Unable to read MotherDuck "+s.function, "the query failed", err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		return leftNull("Unable to decode MotherDuck "+s.function, "its result could not be decoded", err)
	}
	if len(rows) == 0 {
		return values
	}
	for _, detail := range s.attrs {
		source := detail.source
		if source == "" {
			source = detail.name
		}
		values[detail.name] = typedRowStringValue(rows[0][source])
	}
	return values
}
