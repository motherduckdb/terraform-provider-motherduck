package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
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
	FlightParams   types.Map    `tfsdk:"flight_params"`
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
				MarkdownDescription: "DuckDB secret type, such as `s3`, or `flights` for a secret that Flights read as environment variables.",
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
				MarkdownDescription: "Sensitive secret parameters inserted into the CREATE SECRET statement. Not valid for `flights` secrets, which use `flight_params`.",
			},
			"flight_params": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Sensitive key and value pairs of a `flights` secret, sent as `PARAMS MAP {...}`. Required when `type = \"flights\"` unless `secret_sql` supplies `PARAMS`, and only valid for that type. A Flight that attaches the secret receives each pair as the environment variables `<key>` and `<secret name>_<key>`. MotherDuck redacts the values on read, so import cannot recover them.",
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
	query := "DROP SECRET IF EXISTS " + sqlbuild.QuoteIdentifier(state.Name.ValueString()) + " FROM motherduck"
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isSecretAlreadyDropped(err) {
		resp.Diagnostics.AddError("Unable to drop MotherDuck secret", err.Error())
	}
}

// isSecretAlreadyDropped treats a missing secret as deleted. DuckDB reports a
// missing secret as invalid input ("Failed to remove non-existent secret"),
// not as a catalog error, and some storage backends report it even with IF
// EXISTS.
func isSecretAlreadyDropped(err error) bool {
	return isNotFound(err) || strings.Contains(strings.ToLower(err.Error()), "non-existent")
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *secretResource) createSecret(ctx context.Context, getter stateGetter, setter stateSetter, replace bool, diags *diag.Diagnostics) {
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
	var flightParams map[string]string
	if !plan.FlightParams.IsNull() {
		flightParams = map[string]string{}
		diags.Append(plan.FlightParams.ElementsAs(ctx, &flightParams, false)...)
	}
	if diags.HasError() {
		return
	}
	validateFlightSecretValues(flightSecretValues{
		Type:              plan.Type.ValueString(),
		HasParams:         len(params) > 0,
		HasRawSQL:         strings.TrimSpace(plan.SecretSQL.ValueString()) != "",
		HasFlightParams:   flightParams != nil,
		FlightParamsKnown: flightParams != nil,
		FlightParamKeys:   slices.Sorted(maps.Keys(flightParams)),
	}, diags)
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
	if flightParams != nil {
		entries = append(entries, "PARAMS "+sqlbuild.MapLiteral(flightParams))
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
	// Save the created identity before the catalog readback so a readback
	// failure leaves a tainted resource that Terraform can destroy instead of
	// an untracked remote secret.
	prepareSecretCreateState(&plan)
	diags.Append(setter.Set(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	found := r.readSecret(ctx, &plan, diags)
	if !found && !diags.HasError() {
		diags.AddError("Unable to read MotherDuck secret", "Secret was created but was not visible in duckdb_secrets().")
		return
	}
	diags.Append(setter.Set(ctx, &plan)...)
}

// prepareSecretCreateState replaces unknown computed values with nulls so the
// state saved before readback is fully known.
func prepareSecretCreateState(model *secretModel) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.SecretProvider = knownString(model.SecretProvider)
	model.Storage = knownString(model.Storage)
	model.Scope = knownString(model.Scope)
}

func (r *secretResource) readSecret(ctx context.Context, model *secretModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var secretType, provider, storage, scope stdsql.NullString
	err := retry.SQL(ctx, func() error {
		// DuckDB stores secret names in lowercase, even when the CREATE SECRET
		// name is quoted, so match case-insensitively and keep the configured
		// spelling in state.
		return client.QueryRow(ctx, `SELECT type, provider, storage, scope::VARCHAR FROM duckdb_secrets() WHERE lower(name) = lower(?) AND storage = 'motherduck'`, model.Name.ValueString()).Scan(&secretType, &provider, &storage, &scope)
	})
	if errors.Is(err, stdsql.ErrNoRows) {
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
	validateFlightSecretConfig(config, diags)
}

// flightReservedParamNames are Flight run parameters that MotherDuck sets
// itself. A FLIGHTS secret key with one of these names is exposed to the run
// only under its <secret name>_<key> alias.
var flightReservedParamNames = []string{"MOTHERDUCK_TOKEN", "MOTHERDUCK_FLIGHTS_RUN", "MOTHERDUCK_FLIGHT_ID", "MOTHERDUCK_FLIGHT_RUN_ID"}

// flightSecretValues carries the fields that decide whether a FLIGHTS secret
// is well formed. An empty Type means the type is not known yet.
type flightSecretValues struct {
	Type            string
	HasParams       bool
	HasRawSQL       bool
	HasFlightParams bool
	// FlightParamsKnown is true when flight_params is set and its keys are
	// known, so an empty map can be told apart from an unknown one.
	FlightParamsKnown bool
	FlightParamKeys   []string // sorted, so diagnostics are deterministic
}

func validateFlightSecretConfig(config secretModel, diags *diag.Diagnostics) {
	values := flightSecretValues{
		HasParams:       !config.Params.IsNull() && (config.Params.IsUnknown() || len(config.Params.Elements()) > 0),
		HasRawSQL:       config.SecretSQL.IsUnknown() || strings.TrimSpace(config.SecretSQL.ValueString()) != "",
		HasFlightParams: !config.FlightParams.IsNull(),
	}
	if !config.Type.IsUnknown() {
		values.Type = config.Type.ValueString()
	}
	if !config.FlightParams.IsNull() && !config.FlightParams.IsUnknown() {
		elements := config.FlightParams.Elements()
		values.FlightParamsKnown = true
		values.FlightParamKeys = slices.Sorted(maps.Keys(elements))
		for _, key := range values.FlightParamKeys {
			if value, ok := elements[key].(types.String); ok && value.IsNull() {
				diags.AddAttributeError(path.Root("flight_params").AtMapKey(key), "Invalid MotherDuck Flights secret parameter", "Flights secret parameter values must not be null.")
			}
			if slices.Contains(flightReservedParamNames, key) {
				diags.AddAttributeWarning(path.Root("flight_params").AtMapKey(key), "Reserved MotherDuck Flights parameter name",
					fmt.Sprintf("MotherDuck sets %s for every Flight run, so a Flight that attaches this secret receives this value only as <secret name>_%s.", key, key))
			}
		}
	}
	validateFlightSecretValues(values, diags)
}

// validateFlightSecretValues checks a FLIGHTS secret. MotherDuck takes its
// values only from a non-empty PARAMS map, so ordinary params are rejected
// and flight_params is required unless secret_sql supplies PARAMS.
func validateFlightSecretValues(values flightSecretValues, diags *diag.Diagnostics) {
	if values.Type == "" {
		return
	}
	if !strings.EqualFold(values.Type, "flights") {
		if values.HasFlightParams {
			diags.AddAttributeError(path.Root("flight_params"), "Invalid MotherDuck secret parameters", "`flight_params` is only valid when `type = \"flights\"`. Use `params` for other secret types.")
		}
		return
	}
	if values.HasParams {
		diags.AddAttributeError(path.Root("params"), "Invalid MotherDuck Flights secret parameters", "A `flights` secret takes its values from `flight_params`. Move these entries to `flight_params`.")
	}
	if !values.HasFlightParams && !values.HasRawSQL {
		diags.AddAttributeError(path.Root("flight_params"), "Missing MotherDuck Flights secret parameters", "A `flights` secret requires `flight_params` with at least one entry.")
		return
	}
	if values.FlightParamsKnown && len(values.FlightParamKeys) == 0 {
		diags.AddAttributeError(path.Root("flight_params"), "Missing MotherDuck Flights secret parameters", "`flight_params` must contain at least one entry.")
	}
	for _, key := range values.FlightParamKeys {
		if strings.TrimSpace(key) == "" {
			diags.AddAttributeError(path.Root("flight_params"), "Invalid MotherDuck Flights secret parameter", "Flights secret parameter keys must not be empty.")
		}
	}
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
