package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type secretResource struct{ baseResource }

type secretModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	SecretProvider types.String `tfsdk:"secret_provider"`
	Params         types.Map    `tfsdk:"params"`
	Storage        types.String `tfsdk:"storage"`
	Scope          types.String `tfsdk:"scope"`
	SecretSQL      types.String `tfsdk:"secret_sql"`
}

func NewSecretResource() resource.Resource { return &secretResource{} }

func (r *secretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a persistent MotherDuck secret. Secret values are write-only and redacted by MotherDuck.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Secret resource ID. This is the secret name.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Secret name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "DuckDB secret type, such as `S3`.",
				Validators:          sqlBareWordValidators(),
			},
			"secret_provider": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Optional DuckDB secret provider.",
				Validators:          sqlBareWordValidators(),
			},
			"params": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Sensitive secret parameters inserted into the CREATE SECRET statement.",
			},
			"storage": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret storage backend reported by DuckDB.",
			},
			"scope": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret scope reported by DuckDB.",
			},
			"secret_sql": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Optional raw secret body entries, for advanced provider-specific clauses. Values are inserted inside the CREATE SECRET parentheses after TYPE/PROVIDER.",
			},
		},
	}
}

func (r *secretResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSecretConfig(config, &resp.Diagnostics)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.createSecret(ctx, req.Plan, &resp.State, false, &resp.Diagnostics)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readSecret(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.createSecret(ctx, req.Plan, &resp.State, true, &resp.Diagnostics)
}

func (r *secretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var config, state secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desiredScope, ok := desiredSecretScopeFromParams(config.Params)
	if !ok || state.Scope.IsNull() || state.Scope.IsUnknown() {
		return
	}
	if state.Scope.ValueString() != desiredScope {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("scope"), desiredScope)...)
	}
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "DROP SECRET " + sqlbuild.QuoteIdentifier(state.Name.ValueString()) + " FROM motherduck"
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to drop MotherDuck secret", err.Error())
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *secretResource) createSecret(ctx context.Context, getter interface {
	Get(context.Context, any) diag.Diagnostics
}, setter interface {
	Set(context.Context, any) diag.Diagnostics
}, replace bool, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var plan secretModel
	diags.Append(getter.Get(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	params := map[string]string{}
	if !plan.Params.IsNull() {
		diags.Append(plan.Params.ElementsAs(ctx, &params, false)...)
	}
	if diags.HasError() {
		return
	}
	// Re-validate on the resolved plan: values unknown at plan time skipped the
	// plan-time checks, and everything below is spliced into raw SQL.
	validateSecretValues(secretValidationValues{
		Type:      plan.Type.ValueString(),
		Provider:  plan.SecretProvider.ValueString(),
		ParamKeys: slices.Sorted(maps.Keys(params)),
		RawSQL:    plan.SecretSQL.ValueString(),
	}, diags)
	if diags.HasError() {
		return
	}
	entries := []string{"TYPE " + strings.ToUpper(plan.Type.ValueString())}
	if !plan.SecretProvider.IsNull() && plan.SecretProvider.ValueString() != "" {
		entries = append(entries, "PROVIDER "+strings.ToUpper(plan.SecretProvider.ValueString()))
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entries = append(entries, strings.ToUpper(key)+" "+sqlbuild.StringLiteral(params[key]))
	}
	if !plan.SecretSQL.IsNull() && strings.TrimSpace(plan.SecretSQL.ValueString()) != "" {
		entries = append(entries, strings.TrimSpace(plan.SecretSQL.ValueString()))
	}
	createKeyword := "CREATE SECRET"
	if replace {
		createKeyword = "CREATE OR REPLACE SECRET"
	}
	query := fmt.Sprintf("%s %s IN MOTHERDUCK (%s)", createKeyword, sqlbuild.QuoteIdentifier(plan.Name.ValueString()), strings.Join(entries, ", "))
	if err := client.Exec(ctx, query); err != nil {
		diags.AddError("Unable to create MotherDuck secret", secretWriteDiagnostic(err))
		return
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	found := r.readSecret(ctx, &plan, diags)
	if !found && !diags.HasError() {
		diags.AddError("Unable to read MotherDuck secret", "Secret was created but was not visible in duckdb_secrets().")
		return
	}
	diags.Append(setter.Set(ctx, &plan)...)
}

func (r *secretResource) readSecret(ctx context.Context, model *secretModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var secretType, provider, storage, scope stdsql.NullString
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, `SELECT type, provider, storage, scope::VARCHAR FROM duckdb_secrets() WHERE name = ? AND storage = 'motherduck'`, model.Name.ValueString()).Scan(&secretType, &provider, &storage, &scope)
	})
	if err == stdsql.ErrNoRows {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck secret", err.Error())
		return false
	}
	model.ID = types.StringValue(model.Name.ValueString())
	model.Type = lowerNullString(secretType)
	model.SecretProvider = lowerNullString(provider)
	model.Storage = nullString(storage)
	model.Scope = nullString(scope)
	return true
}

// validateSecretConfig runs the shared secret checks on the values known at
// plan time. `type` and `secret_provider` are covered by their attribute
// validators at plan time, so only params and secret_sql are checked here.
func validateSecretConfig(config secretModel, diags *diag.Diagnostics) {
	values := secretValidationValues{}
	if !config.Params.IsNull() && !config.Params.IsUnknown() {
		values.ParamKeys = slices.Sorted(maps.Keys(config.Params.Elements()))
	}
	if !config.SecretSQL.IsNull() && !config.SecretSQL.IsUnknown() {
		values.RawSQL = config.SecretSQL.ValueString()
	}
	validateSecretValues(values, diags)
}

// secretValidationValues carries the secret fields that are spliced into the
// CREATE SECRET statement as raw SQL. Nil pointers mean "not provided or not
// yet known", so the field is skipped.
type secretValidationValues struct {
	Type      string
	Provider  string
	ParamKeys []string // sorted, so diagnostics are deterministic
	RawSQL    string
}

// validateSecretValues is the single source of truth for what may be spliced
// into CREATE SECRET. Plan-time validation and apply-time re-validation both
// call it, so a value unknown at plan time cannot bypass the checks.
func validateSecretValues(values secretValidationValues, diags *diag.Diagnostics) {
	if values.Type != "" {
		if detail := sqlBareOptionWordError(values.Type); detail != "" {
			diags.AddAttributeError(path.Root("type"), "Invalid MotherDuck SQL option", detail)
		}
	}
	if values.Provider != "" {
		if detail := sqlBareOptionWordError(values.Provider); detail != "" {
			diags.AddAttributeError(path.Root("secret_provider"), "Invalid MotherDuck SQL option", detail)
		}
	}
	for _, key := range values.ParamKeys {
		if !isBareSQLWord(key) {
			diags.AddAttributeError(path.Root("params").AtMapKey(key), "Invalid MotherDuck secret parameter", "Secret parameter keys must be single bare SQL option words containing only letters, numbers, and underscores, starting with a letter or underscore.")
		}
	}
	if strings.Contains(values.RawSQL, ";") {
		diags.AddAttributeError(path.Root("secret_sql"), "Invalid MotherDuck secret SQL", "Raw secret SQL clauses must not contain semicolons.")
	}
}

func desiredSecretScopeFromParams(params types.Map) (string, bool) {
	if params.IsNull() || params.IsUnknown() {
		return "", false
	}
	for key, value := range params.Elements() {
		if !strings.EqualFold(key, "scope") {
			continue
		}
		scope, ok := value.(types.String)
		if !ok || scope.IsNull() || scope.IsUnknown() {
			return "", false
		}
		return secretScopeStateValue(scope.ValueString()), true
	}
	return "", false
}

func secretScopeStateValue(scope string) string {
	return "['" + strings.ReplaceAll(scope, "'", "''") + "']"
}
