package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type viewResource struct{ baseResource }

type viewModel struct {
	ID       types.String `tfsdk:"id"`
	Database types.String `tfsdk:"database"`
	Schema   types.String `tfsdk:"schema"`
	Name     types.String `tfsdk:"name"`
	Query    types.String `tfsdk:"query"`
}

const viewServerDefinitionPrivateKey = "view_server_definition_v1"

type privateState interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

func NewViewResource() resource.Resource { return &viewResource{} }

func (r *viewResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_view"
}

func (r *viewResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck SQL view.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "View resource ID in `<database>.<schema>.<view>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the view.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema that contains the view.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "View name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"query": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Single SELECT query body for the view. Semicolons are rejected.",
			},
		},
	}
}

func (r *viewResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config viewModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Query.IsNull() && !config.Query.IsUnknown() {
		validateViewQuery(config.Query.ValueString(), &resp.Diagnostics)
	}
}

// validateViewQuery rejects a raw view body that could carry a second
// statement. It runs at plan time on known values and again at apply time on
// the resolved plan, because duckdb-go executes multi-statement strings and a
// value unknown at plan time would otherwise bypass the plan-time check.
func validateViewQuery(query string, diags *diag.Diagnostics) {
	if strings.Contains(query, ";") {
		diags.AddAttributeError(path.Root("query"), "Invalid MotherDuck view query", "View queries must be a single SELECT body and must not contain semicolons.")
	}
}

func (r *viewResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.writeView(ctx, req.Plan, &resp.State, resp.Private, false, &resp.Diagnostics)
}

func (r *viewResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state viewModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists := relationExists(ctx, r, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), "VIEW", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	definition, found := readViewServerDefinition(ctx, client, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if found {
		storedDefinition, ok := loadViewServerDefinition(ctx, req.Private, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if !ok {
			state.Query = types.StringValue(viewQueryFromDefinition(definition))
			storeViewServerDefinition(ctx, resp.Private, definition, &resp.Diagnostics)
		} else if storedDefinition != definition {
			state.Query = types.StringValue(viewQueryFromDefinition(definition))
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *viewResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.writeView(ctx, req.Plan, &resp.State, resp.Private, true, &resp.Diagnostics)
}

func (r *viewResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	dropRelation(ctx, r, "VIEW", req.State, &resp.Diagnostics)
}

func (r *viewResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importThreePartID(ctx, req.ID, resp)
}

// writeView creates the view, or replaces it when replace is true. Create uses
// plain CREATE VIEW so an existing view that Terraform does not manage is
// reported as a conflict instead of being silently overwritten.
func (r *viewResource) writeView(ctx context.Context, getter stateGetter, setter stateSetter, private privateState, replace bool, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var plan viewModel
	diags.Append(getter.Get(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	validateViewQuery(plan.Query.ValueString(), diags)
	if diags.HasError() {
		return
	}
	id := qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString())
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		diags.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	createKeyword := "CREATE VIEW "
	if replace {
		createKeyword = "CREATE OR REPLACE VIEW "
	}
	query := createKeyword + sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()) + " AS " + plan.Query.ValueString()
	if err := client.Exec(ctx, query); err != nil {
		diags.AddError("Unable to create MotherDuck view", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	diags.Append(setter.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	definition, found := readViewServerDefinition(ctx, client, plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString(), diags)
	if diags.HasError() {
		return
	}
	if !found {
		diags.AddError("Unable to read MotherDuck view", "View was created but was not visible in information_schema.views.")
		return
	}
	storeViewServerDefinition(ctx, private, definition, diags)
}

func readViewServerDefinition(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) (string, bool) {
	var definition stdsql.NullString
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, database); err != nil {
			return err
		}
		return client.QueryRow(ctx, `SELECT view_definition FROM information_schema.views WHERE table_catalog = ? AND table_schema = ? AND table_name = ?`, database, schemaName, name).Scan(&definition)
	})
	if err != nil && isNotFoundFor(err, database, schemaName, name) {
		return "", false
	}
	if errors.Is(err, stdsql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck view definition", err.Error())
		return "", false
	}
	if !definition.Valid {
		return "", false
	}
	return definition.String, true
}

func storeViewServerDefinition(ctx context.Context, private privateState, definition string, diags *diag.Diagnostics) {
	if private == nil {
		return
	}
	data, err := json.Marshal(definition)
	if err != nil {
		diags.AddError("Unable to encode MotherDuck view private state", err.Error())
		return
	}
	diags.Append(private.SetKey(ctx, viewServerDefinitionPrivateKey, data)...)
}

func loadViewServerDefinition(ctx context.Context, private privateState, diags *diag.Diagnostics) (string, bool) {
	if private == nil {
		return "", false
	}
	data, privateDiags := private.GetKey(ctx, viewServerDefinitionPrivateKey)
	diags.Append(privateDiags...)
	if privateDiags.HasError() || len(data) == 0 {
		return "", false
	}
	var definition string
	if err := json.Unmarshal(data, &definition); err != nil {
		diags.AddError("Unable to decode MotherDuck view private state", err.Error())
		return "", false
	}
	return definition, true
}

// viewQueryFromDefinition extracts the query body from a DuckDB view
// definition such as `CREATE VIEW s."v" AS SELECT 1;`. The AS keyword that
// introduces the query is the first one outside quoted identifiers, string
// literals, and parentheses, so view names such as "my as view" and column
// alias lists such as `v (x) AS SELECT 1` are skipped. A column alias list is
// not part of the managed query and is dropped.
func viewQueryFromDefinition(definition string) string {
	body := strings.TrimSpace(definition)
	if strings.HasPrefix(strings.ToUpper(body), "CREATE ") {
		if idx := viewDefinitionQueryStart(body); idx >= 0 {
			body = strings.TrimSpace(body[idx:])
		}
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, ";"))
	return body
}

// viewDefinitionQueryStart returns the offset just after the top-level AS
// keyword in a CREATE VIEW definition, or -1 when there is none.
func viewDefinitionQueryStart(definition string) int {
	var quote byte
	depth := 0
	for i := 0; i < len(definition); i++ {
		ch := definition[i]
		if quote != 0 {
			if ch == quote {
				if i+1 < len(definition) && definition[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			quote = ch
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || i == 0 || i+3 > len(definition) {
			continue
		}
		if isSQLSpace(definition[i-1]) && strings.EqualFold(definition[i:i+2], "AS") && isSQLSpace(definition[i+2]) {
			return i + 3
		}
	}
	return -1
}

func isSQLSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}
