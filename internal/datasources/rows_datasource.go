package datasources

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"
)

var (
	_ datasource.DataSource              = &scalarDataSource{}
	_ datasource.DataSourceWithConfigure = &scalarDataSource{}
	_ datasource.DataSource              = &rowsDataSource{}
	_ datasource.DataSourceWithConfigure = &rowsDataSource{}
)

type rowsDataSource struct {
	baseDataSource
	spec rowSpec
}

type rowsModel struct {
	Name             types.String `tfsdk:"name"`
	DatabaseName     types.String `tfsdk:"database_name"`
	SecretName       types.String `tfsdk:"secret_name"`
	Path             types.String `tfsdk:"path"`
	DiveID           types.String `tfsdk:"dive_id"`
	FlightID         types.String `tfsdk:"flight_id"`
	GuideID          types.String `tfsdk:"guide_id"`
	RoleName         types.String `tfsdk:"role_name"`
	Username         types.String `tfsdk:"username"`
	Topic            types.String `tfsdk:"topic"`
	ReferenceType    types.String `tfsdk:"reference_type"`
	ReferenceURL     types.String `tfsdk:"reference_url"`
	ReferenceSchema  types.String `tfsdk:"reference_schema"`
	ReferenceTable   types.String `tfsdk:"reference_table"`
	ReferenceColumn  types.String `tfsdk:"reference_column"`
	ReferenceView    types.String `tfsdk:"reference_view"`
	ReferenceMacro   types.String `tfsdk:"reference_macro"`
	ReferenceUUID    types.String `tfsdk:"reference_uuid"`
	RunNumber        types.Int64  `tfsdk:"run_number"`
	Limit            types.Int64  `tfsdk:"limit"`
	Offset           types.Int64  `tfsdk:"offset"`
	IncludeOrgShares types.Bool   `tfsdk:"include_org_shares"`
	OwnerOnly        types.Bool   `tfsdk:"owner_only"`
	RowsJSON         types.String `tfsdk:"rows_json"`
}

func (d *rowsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.spec.name
}

func (d *rowsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"rows_json": schema.StringAttribute{
			Computed:            true,
			Sensitive:           true,
			MarkdownDescription: "Raw MotherDuck catalog rows encoded as JSON. Sensitive because catalog rows can include share URLs and account metadata.",
		},
	}
	for _, attr := range d.spec.attrs {
		attrs[attr] = rowAttribute(attr, d.spec.attrRequired(attr))
	}
	if len(d.spec.typedRows) > 0 {
		attrs["rows"] = d.spec.typedRowsAttribute()
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: d.spec.description,
		Attributes:          attrs,
	}
}

func (d *rowsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	config := d.readConfig(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	query, err := d.spec.build(config)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MotherDuck data source configuration", err.Error())
		return
	}
	if d.spec.requiredFunction != "" && !d.functionAvailable(ctx, client, &resp.Diagnostics) {
		return
	}
	var rowsJSON string
	var ok bool
	if d.spec.name == "role_members" {
		rowsJSON, ok = d.queryRoleMembers(ctx, client, config.RoleName.ValueString(), &resp.Diagnostics)
	} else {
		rowsJSON, ok = d.queryRows(ctx, client, query, &resp.Diagnostics)
	}
	if !ok {
		return
	}
	rowsJSONValue := types.StringValue(rowsJSON)
	var typedRows types.List
	if len(d.spec.typedRows) > 0 {
		var diags diag.Diagnostics
		typedRows, diags = d.spec.typedRowsValue(rowsJSON)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	d.setState(ctx, &resp.State, config, rowsJSONValue, typedRows, resp)
}

func (d *rowsDataSource) queryRoleMembers(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, roleName string, diags *diag.Diagnostics) (string, bool) {
	role := sqlbuild.QuoteIdentifier(roleName)
	usersJSON, ok := d.queryRows(ctx, client, "SHOW USERS OF ROLE "+role, diags)
	if !ok {
		return "", false
	}
	rolesJSON, ok := d.queryRows(ctx, client, "SHOW ROLES OF ROLE "+role, diags)
	if !ok {
		return "", false
	}
	var (
		users []map[string]any
		roles []map[string]any
	)
	if err := json.Unmarshal([]byte(usersJSON), &users); err != nil {
		diags.AddError("Unable to decode MotherDuck role users", err.Error())
		return "", false
	}
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		diags.AddError("Unable to decode MotherDuck role members", err.Error())
		return "", false
	}
	rows := make([]map[string]any, 0, len(users)+len(roles))
	for _, row := range users {
		rows = append(rows, map[string]any{
			"member_name":        row["username"],
			"member_type":        "user",
			"email":              row["email"],
			"is_service_account": row["is_service_account"],
			"granted_at":         row["granted_at"],
		})
	}
	for _, row := range roles {
		rows = append(rows, map[string]any{
			"member_name":        row["role_name"],
			"member_type":        "role",
			"email":              nil,
			"is_service_account": nil,
			"granted_at":         row["granted_at"],
		})
	}
	encoded, err := json.Marshal(sortRoleMemberRows(rows))
	if err != nil {
		diags.AddError("Unable to encode MotherDuck role members", err.Error())
		return "", false
	}
	return string(encoded), true
}

func (d *rowsDataSource) queryRows(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, query string, diags *diag.Diagnostics) (string, bool) {
	var rowsJSON string
	err := retry.SQL(ctx, func() error {
		var readErr error
		rowsJSON, readErr = client.QueryRowsJSON(ctx, query)
		return readErr
	})
	if err != nil {
		if mdsql.IsUnsupportedCommand(err) {
			diags.AddError(
				"MotherDuck SQL command unavailable",
				fmt.Sprintf("%s is not supported by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_%s data source.", query, d.spec.name),
			)
			return "", false
		}
		diags.AddError("Unable to read MotherDuck rows data source", err.Error())
		return "", false
	}
	if d.spec.postProcess == nil {
		return rowsJSON, true
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		diags.AddError("Unable to decode MotherDuck rows data source", err.Error())
		return "", false
	}
	data, err := json.Marshal(d.spec.postProcess(rows))
	if err != nil {
		diags.AddError("Unable to encode MotherDuck rows data source", err.Error())
		return "", false
	}
	return string(data), true
}

func sortRowsBy(field string) func([]map[string]any) []map[string]any {
	return func(rows []map[string]any) []map[string]any {
		sort.SliceStable(rows, func(i, j int) bool {
			left, _ := rows[i][field].(string)
			right, _ := rows[j][field].(string)
			return left < right
		})
		return rows
	}
}

func sortRoleMemberRows(rows []map[string]any) []map[string]any {
	sort.SliceStable(rows, func(i, j int) bool {
		leftType, _ := rows[i]["member_type"].(string)
		rightType, _ := rows[j]["member_type"].(string)
		if leftType != rightType {
			return leftType < rightType
		}
		leftName, _ := rows[i]["member_name"].(string)
		rightName, _ := rows[j]["member_name"].(string)
		return leftName < rightName
	})
	return rows
}

func (d *rowsDataSource) functionAvailable(ctx context.Context, client interface {
	Exists(context.Context, string, ...any) (bool, error)
}, diags *diag.Diagnostics) bool {
	var available bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		available, existsErr = client.Exists(ctx, "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = lower(?)", d.spec.requiredFunction)
		return existsErr
	})
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return false
	}
	if !available {
		diags.AddError(
			"MotherDuck SQL function unavailable",
			fmt.Sprintf("%s is not exposed by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_%s data source.", d.spec.requiredFunction, d.spec.name),
		)
		return false
	}
	return true
}

func (s rowSpec) attrRequired(name string) bool {
	for _, required := range s.requiredAttrs {
		if required == name {
			return true
		}
	}
	return false
}

func rowAttribute(name string, required bool) schema.Attribute {
	switch name {
	case "limit":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source limit", 0)}, "Maximum number of rows to return when the underlying MotherDuck catalog function supports limits.")
	case "offset":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source offset", 0)}, "Number of rows to skip when the underlying MotherDuck catalog function supports offsets.")
	case "run_number":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source run number", 1)}, "MotherDuck Flight run number.")
	case "dive_id":
		return rowStringAttribute(required, uuidValidators(), "Dive ID. Must be a UUID with no leading or trailing whitespace.")
	case "flight_id":
		return rowStringAttribute(required, uuidValidators(), "Flight ID. Must be a UUID with no leading or trailing whitespace.")
	case "guide_id":
		return rowStringAttribute(required, uuidValidators(), "Guide ID. Must be a UUID with no leading or trailing whitespace.")
	case "role_name":
		return rowStringAttribute(required, nil, "MotherDuck role name.")
	case "username":
		return rowStringAttribute(required, nil, "MotherDuck user or service-account principal.")
	case "topic":
		return rowStringAttribute(required, nil, "Optional slash-separated Guide topic subtree filter.")
	case "reference_type":
		return rowStringAttribute(required, nil, "Optional Guide reference filter type: catalog, dive, flight, or guide.")
	case "reference_url":
		return rowStringAttribute(required, nil, "MotherDuck database or share URL for a catalog Guide reference filter.")
	case "reference_schema":
		return rowStringAttribute(required, nil, "Optional schema narrowing for a catalog Guide reference filter.")
	case "reference_table":
		return rowStringAttribute(required, nil, "Optional table narrowing for a catalog Guide reference filter.")
	case "reference_column":
		return rowStringAttribute(required, nil, "Optional column narrowing for a catalog Guide reference filter.")
	case "reference_view":
		return rowStringAttribute(required, nil, "Optional view narrowing for a catalog Guide reference filter.")
	case "reference_macro":
		return rowStringAttribute(required, nil, "Optional macro narrowing for a catalog Guide reference filter.")
	case "reference_uuid":
		return rowStringAttribute(required, uuidValidators(), "Dive, Flight, or Guide UUID for a Guide reference filter.")
	case "include_org_shares":
		if required {
			return schema.BoolAttribute{Required: true, MarkdownDescription: "Whether to include organization-shared Dives when the MotherDuck function supports it."}
		}
		return schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether to include organization-shared Dives when the MotherDuck function supports it."}
	case "owner_only":
		if required {
			return schema.BoolAttribute{Required: true, MarkdownDescription: "Whether to restrict Flight listings to Flights owned by the current user."}
		}
		return schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether to restrict Flight listings to Flights owned by the current user. Ignored for callers who can only view their own Flights."}
	case "name":
		return rowStringAttribute(required, nil, "Optional exact object name filter.")
	case "database_name":
		return rowStringAttribute(required, nil, "MotherDuck database name filter.")
	case "secret_name":
		return rowStringAttribute(required, nil, "MotherDuck secret name.")
	case "path":
		return rowStringAttribute(required, nil, "Object-storage path to list through MotherDuck SQL.")
	default:
		return rowStringAttribute(required, nil, "MotherDuck data source argument.")
	}
}

func rowInt64Attribute(required bool, validators []validator.Int64, description string) schema.Attribute {
	if required {
		return schema.Int64Attribute{Required: true, Validators: validators, MarkdownDescription: description}
	}
	return schema.Int64Attribute{Optional: true, Validators: validators, MarkdownDescription: description}
}

func rowStringAttribute(required bool, validators []validator.String, description string) schema.Attribute {
	if required {
		return schema.StringAttribute{Required: true, Validators: validators, MarkdownDescription: description}
	}
	return schema.StringAttribute{Optional: true, Validators: validators, MarkdownDescription: description}
}

// attribute returns a typed field address usable for both configuration reads
// and state writes. Keeping this mapping in one place prevents them drifting.
func (m *rowsModel) attribute(name string) attr.Value {
	switch name {
	case "name":
		return &m.Name
	case "database_name":
		return &m.DatabaseName
	case "secret_name":
		return &m.SecretName
	case "path":
		return &m.Path
	case "dive_id":
		return &m.DiveID
	case "flight_id":
		return &m.FlightID
	case "guide_id":
		return &m.GuideID
	case "role_name":
		return &m.RoleName
	case "username":
		return &m.Username
	case "topic":
		return &m.Topic
	case "reference_type":
		return &m.ReferenceType
	case "reference_url":
		return &m.ReferenceURL
	case "reference_schema":
		return &m.ReferenceSchema
	case "reference_table":
		return &m.ReferenceTable
	case "reference_column":
		return &m.ReferenceColumn
	case "reference_view":
		return &m.ReferenceView
	case "reference_macro":
		return &m.ReferenceMacro
	case "reference_uuid":
		return &m.ReferenceUUID
	case "run_number":
		return &m.RunNumber
	case "limit":
		return &m.Limit
	case "offset":
		return &m.Offset
	case "include_org_shares":
		return &m.IncludeOrgShares
	case "owner_only":
		return &m.OwnerOnly
	default:
		return nil
	}
}

func (d *rowsDataSource) readConfig(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) rowsModel {
	var config rowsModel
	for _, name := range d.spec.attrs {
		if target := config.attribute(name); target != nil {
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(name), target)...)
		}
	}
	return config
}

func (d *rowsDataSource) setState(ctx context.Context, state interface {
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, config rowsModel, rowsJSON types.String, typedRows types.List, resp *datasource.ReadResponse) {
	for _, name := range d.spec.attrs {
		if value := config.attribute(name); value != nil {
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(name), value)...)
		}
	}
	resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root("rows_json"), rowsJSON)...)
	if len(d.spec.typedRows) > 0 {
		resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root("rows"), typedRows)...)
	}
}
