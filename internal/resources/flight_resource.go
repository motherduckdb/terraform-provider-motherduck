package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"
)

type flightResource struct{ baseResource }

type flightModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	SourceCode        types.String `tfsdk:"source_code"`
	ScheduleCron      types.String `tfsdk:"schedule_cron"`
	RequirementsTxt   types.String `tfsdk:"requirements_txt"`
	Config            types.Map    `tfsdk:"config"`
	AccessTokenName   types.String `tfsdk:"access_token_name"`
	FlightSecretNames types.List   `tfsdk:"flight_secret_names"`
	MaxRuntimeSec     types.Int64  `tfsdk:"max_runtime_sec"`
	Status            types.String `tfsdk:"status"`
	CurrentVersion    types.Int64  `tfsdk:"current_version"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

func NewFlightResource() resource.Resource { return &flightResource{} }

func (r *flightResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flight"
}

func (r *flightResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck Flight definition through public SQL table functions.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Flight ID assigned by MotherDuck.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Flight name.",
			},
			"source_code": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Flight source code sent to MotherDuck.",
			},
			"schedule_cron": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional cron schedule for the Flight.",
			},
			"requirements_txt": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional Python requirements text for the Flight runtime.",
			},
			"config": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional string configuration passed to the Flight. Keys become Flight runtime environment variables and must be valid Flight config names.",
				Validators:          flightConfigMapValidators(),
			},
			"access_token_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional MotherDuck access token name for the Flight. When omitted, MotherDuck uses its default Flight token behavior and Terraform keeps this field unset.",
			},
			"flight_secret_names": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional MotherDuck secret names available to the Flight.",
			},
			"max_runtime_sec": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Maximum Flight runtime in seconds. `0` disables the runtime limit. When omitted, MotherDuck supplies its current default.",
				PlanModifiers:       int64UseStateForUnknown(),
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck Flight maximum runtime", 0, 4294967295)},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current Flight status reported by MotherDuck.",
			},
			"current_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Current Flight version number reported by MotherDuck.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Flight creation timestamp reported by MotherDuck.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Flight update timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *flightResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_create_flight", "motherduck_flight") {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_get_flight", "motherduck_flight") {
		return
	}
	var plan flightModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	args, ok := flightCreateArgs(ctx, &plan, &resp.Diagnostics)
	if !ok {
		return
	}
	var id string
	query := "SELECT flight_id::VARCHAR FROM MD_CREATE_FLIGHT" + sqlbuild.NamedArgs(args)
	if err := client.QueryRow(ctx, query).Scan(&id); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck Flight", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	r.readFlight(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *flightResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state flightModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readFlight(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *flightResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_flight", "motherduck_flight") {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_get_flight", "motherduck_flight") {
		return
	}
	var plan, state flightModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	args, ok := flightUpdateArgs(ctx, &plan, &state, &resp.Diagnostics)
	if !ok {
		return
	}
	args["flight_id"] = sqlbuild.StringLiteral(state.ID.ValueString()) + "::UUID"
	if err := client.Exec(ctx, "CALL MD_UPDATE_FLIGHT"+sqlbuild.NamedArgs(args)); err != nil {
		resp.Diagnostics.AddError("Unable to update MotherDuck Flight", err.Error())
		return
	}
	r.readFlight(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *flightResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_delete_flight", "motherduck_flight") {
		return
	}
	var state flightModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "CALL MD_DELETE_FLIGHT(flight_id := " + sqlbuild.StringLiteral(state.ID.ValueString()) + "::UUID)"
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck Flight", err.Error())
	}
}

func (r *flightResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importUUIDID(ctx, req.ID, resp)
}

func (r *flightResource) readFlight(ctx context.Context, model *flightModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_get_flight", "motherduck_flight") {
		return false
	}
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_get_flight_version", "motherduck_flight") {
		return false
	}
	var name, schedule, status, created, updated stdsql.NullString
	var currentVersion stdsql.NullInt64
	query := "SELECT flight_name, schedule_cron, status, current_version, created_at::VARCHAR, updated_at::VARCHAR FROM MD_GET_FLIGHT(flight_id := " + sqlbuild.StringLiteral(model.ID.ValueString()) + "::UUID)"
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(&name, &schedule, &status, &currentVersion, &created, &updated)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Flight", err.Error())
		return false
	}
	model.Name = nullString(name)
	model.ScheduleCron = nullString(schedule)
	model.Status = nullString(status)
	if currentVersion.Valid {
		model.CurrentVersion = types.Int64Value(currentVersion.Int64)
	}
	model.CreatedAt = nullString(created)
	model.UpdatedAt = nullString(updated)
	if currentVersion.Valid {
		r.readFlightVersion(ctx, model, currentVersion.Int64, diags)
	}
	return true
}

func (r *flightResource) readFlightVersion(ctx context.Context, model *flightModel, version int64, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var sourceCode, requirements, accessTokenName, secretNamesJSON, configJSON stdsql.NullString
	var maxRuntimeSec stdsql.NullInt64
	query := fmt.Sprintf(
		"SELECT source_code, requirements_txt, access_token_name, to_json(flight_secret_names)::VARCHAR, to_json(config)::VARCHAR, max_runtime_sec FROM MD_GET_FLIGHT_VERSION(flight_id := %s::UUID, version_number := %d)",
		sqlbuild.StringLiteral(model.ID.ValueString()),
		version,
	)
	if err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(&sourceCode, &requirements, &accessTokenName, &secretNamesJSON, &configJSON, &maxRuntimeSec)
	}); err != nil {
		diags.AddError("Unable to read MotherDuck Flight version", err.Error())
		return
	}
	model.SourceCode = nullString(sourceCode)
	model.RequirementsTxt = optionalStringFromLive(model.RequirementsTxt, requirements)
	model.AccessTokenName = optionalConfigOwnedStringFromLive(model.AccessTokenName, accessTokenName)
	model.FlightSecretNames = optionalStringListFromJSON(ctx, model.FlightSecretNames, secretNamesJSON, "flight_secret_names", diags)
	model.Config = optionalStringMapFromJSON(ctx, model.Config, configJSON, "config", diags)
	if maxRuntimeSec.Valid {
		model.MaxRuntimeSec = types.Int64Value(maxRuntimeSec.Int64)
	} else {
		model.MaxRuntimeSec = types.Int64Null()
	}
}

func flightConfigMapValidators() []validator.Map {
	return []validator.Map{flightConfigMapValidator{}}
}

type flightConfigMapValidator struct{}

func (v flightConfigMapValidator) Description(ctx context.Context) string {
	return "validates MotherDuck Flight config keys and values"
}

func (v flightConfigMapValidator) MarkdownDescription(ctx context.Context) string {
	return "Flight config keys must be non-empty, not reserved, and must not contain `=` or NULL bytes. Values must not contain NULL bytes."
}

func (v flightConfigMapValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for key, value := range req.ConfigValue.Elements() {
		if detail := invalidFlightConfigKey(key); detail != "" {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(key), "Invalid MotherDuck Flight config", detail)
			continue
		}
		stringValue, ok := value.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			continue
		}
		if strings.Contains(stringValue.ValueString(), "\x00") {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(key), "Invalid MotherDuck Flight config", fmt.Sprintf("Flight config value for key %q must not contain a NULL byte.", key))
		}
	}
}

func invalidFlightConfigKey(key string) string {
	switch {
	case key == "":
		return "Flight config keys must not be empty."
	case key == "MOTHERDUCK_TOKEN" || key == "MOTHERDUCK_FLIGHTS_RUN":
		return fmt.Sprintf("Flight config key %q is reserved and cannot be set.", key)
	case strings.Contains(key, "="):
		return fmt.Sprintf("Flight config key %q must not contain \"=\".", key)
	case strings.Contains(key, "\x00"):
		return fmt.Sprintf("Flight config key %q must not contain a NULL byte.", key)
	default:
		return ""
	}
}

func flightCreateArgs(ctx context.Context, model *flightModel, diags *diag.Diagnostics) (map[string]string, bool) {
	args := map[string]string{}
	args["name"] = sqlbuild.StringLiteral(model.Name.ValueString())
	args["source_code"] = sqlbuild.StringLiteral(model.SourceCode.ValueString())
	if !model.ScheduleCron.IsNull() {
		args["schedule_cron"] = sqlbuild.StringLiteral(model.ScheduleCron.ValueString())
	}
	if !model.RequirementsTxt.IsNull() {
		args["requirements_txt"] = sqlbuild.StringLiteral(model.RequirementsTxt.ValueString())
	}
	if !model.AccessTokenName.IsNull() {
		args["access_token_name"] = sqlbuild.StringLiteral(model.AccessTokenName.ValueString())
	}
	if !model.Config.IsNull() {
		config := map[string]string{}
		diags.Append(model.Config.ElementsAs(ctx, &config, false)...)
		args["config"] = sqlbuild.MapLiteral(config)
	}
	if !model.FlightSecretNames.IsNull() {
		names := []string{}
		diags.Append(model.FlightSecretNames.ElementsAs(ctx, &names, false)...)
		args["flight_secret_names"] = sqlbuild.ListLiteral(names)
	}
	if !model.MaxRuntimeSec.IsNull() && !model.MaxRuntimeSec.IsUnknown() {
		args["max_runtime_sec"] = fmt.Sprintf("%d", model.MaxRuntimeSec.ValueInt64())
	}
	return args, !diags.HasError()
}

func flightUpdateArgs(ctx context.Context, plan, state *flightModel, diags *diag.Diagnostics) (map[string]string, bool) {
	args := map[string]string{}
	if !plan.Name.Equal(state.Name) {
		args["name"] = sqlbuild.StringLiteral(plan.Name.ValueString())
	}
	if !plan.SourceCode.Equal(state.SourceCode) {
		args["source_code"] = sqlbuild.StringLiteral(plan.SourceCode.ValueString())
	}
	addOptionalStringUpdate(args, "schedule_cron", plan.ScheduleCron, state.ScheduleCron, "''")
	addOptionalStringUpdate(args, "requirements_txt", plan.RequirementsTxt, state.RequirementsTxt, "NULL")
	if plan.AccessTokenName.IsNull() && !state.AccessTokenName.IsNull() {
		diags.AddError(
			"Unable to clear MotherDuck Flight access token",
			"The public Flight update API does not currently expose a clear operation for access_token_name. Replace the Flight resource to return to the default Flight token behavior.",
		)
		return nil, false
	}
	addOptionalStringUpdate(args, "access_token_name", plan.AccessTokenName, state.AccessTokenName, "")
	if !plan.Config.Equal(state.Config) {
		if plan.Config.IsNull() {
			args["config"] = "NULL"
		} else {
			config := map[string]string{}
			diags.Append(plan.Config.ElementsAs(ctx, &config, false)...)
			args["config"] = sqlbuild.MapLiteral(config)
		}
	}
	if !plan.FlightSecretNames.Equal(state.FlightSecretNames) {
		if plan.FlightSecretNames.IsNull() {
			args["flight_secret_names"] = "NULL"
		} else {
			names := []string{}
			diags.Append(plan.FlightSecretNames.ElementsAs(ctx, &names, false)...)
			args["flight_secret_names"] = sqlbuild.ListLiteral(names)
		}
	}
	if !plan.MaxRuntimeSec.Equal(state.MaxRuntimeSec) && !plan.MaxRuntimeSec.IsUnknown() {
		args["max_runtime_sec"] = fmt.Sprintf("%d", plan.MaxRuntimeSec.ValueInt64())
	}
	return args, !diags.HasError()
}
