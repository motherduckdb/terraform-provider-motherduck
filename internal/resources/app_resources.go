package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &diveResource{}
	_ resource.ResourceWithConfigure   = &diveResource{}
	_ resource.ResourceWithImportState = &diveResource{}
	_ resource.Resource                = &flightResource{}
	_ resource.ResourceWithConfigure   = &flightResource{}
	_ resource.ResourceWithImportState = &flightResource{}
	_ resource.Resource                = &flightRunResource{}
	_ resource.ResourceWithConfigure   = &flightRunResource{}
	_ resource.ResourceWithImportState = &flightRunResource{}
)

type diveResource struct{ baseResource }

type diveModel struct {
	ID                types.String `tfsdk:"id"`
	Title             types.String `tfsdk:"title"`
	Description       types.String `tfsdk:"description"`
	Content           types.String `tfsdk:"content"`
	APIVersion        types.Int64  `tfsdk:"api_version"`
	RequiredResources types.List   `tfsdk:"required_resources"`
	Status            types.String `tfsdk:"status"`
	StatusChangedAt   types.String `tfsdk:"status_changed_at"`
	StatusSetBy       types.String `tfsdk:"status_set_by"`
	StatusVersion     types.Int64  `tfsdk:"status_applies_to_version"`
	CurrentVersion    types.Int64  `tfsdk:"current_version"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	OwnerName         types.String `tfsdk:"owner_name"`
}

type diveRequiredResourceModel struct {
	Alias types.String `tfsdk:"alias"`
	URL   types.String `tfsdk:"url"`
}

func NewDiveResource() resource.Resource { return &diveResource{} }

func (r *diveResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dive"
}

func (r *diveResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck Dive through public SQL table functions.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Dive ID assigned by MotherDuck.",
			},
			"title": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Visible Dive title.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive description. Set this to an empty string to clear the visible description; removing an existing configured value is rejected because the public SQL update surface does not expose a null-clear operation.",
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Dive content source sent to MotherDuck.",
			},
			"api_version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive API version passed to MotherDuck when creating or updating content. Omit this to use the MotherDuck default.",
			},
			"required_resources": schema.ListNestedAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Optional share resources to mount into the Dive. This is config-owned because the current public `MD_GET_DIVE` output does not expose mounted resources during refresh or import.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"alias": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Alias exposed to the Dive content.",
							Validators:          nonBlankStringValidators("MotherDuck Dive required resource alias"),
						},
						"url": schema.StringAttribute{
							Required:            true,
							Sensitive:           true,
							MarkdownDescription: "MotherDuck share URL to mount into the Dive. This value is sensitive and can be sourced from `motherduck_share.url`.",
							Validators:          nonBlankStringValidators("MotherDuck Dive required resource URL"),
						},
					},
				},
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Dive governance status. New Dives default to `draft`; owners can set `draft`, `ready`, or `archived`, while `endorsed` requires organization-admin permission.",
				Validators:          diveStatusValidators(),
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"status_changed_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp when the Dive status was last explicitly set.",
			},
			"status_set_by": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "User UUID that last explicitly set the Dive status.",
			},
			"status_applies_to_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Dive version reviewed when the status was last explicitly set.",
			},
			"current_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Current Dive version number reported by MotherDuck.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Dive creation timestamp reported by MotherDuck.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Dive update timestamp reported by MotherDuck.",
			},
			"owner_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "MotherDuck owner name for the Dive.",
			},
		},
	}
}

func (r *diveResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_create_dive", "motherduck_dive") {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_get_dive", "motherduck_dive") {
		return
	}
	var plan diveModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	requestedStatus := plan.Status
	if diveStatusConfigured(requestedStatus) && !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_dive_status", "motherduck_dive") {
		return
	}
	args := map[string]string{
		"title":   sqlbuild.StringLiteral(plan.Title.ValueString()),
		"content": sqlbuild.StringLiteral(plan.Content.ValueString()),
	}
	if !plan.Description.IsNull() {
		args["description"] = sqlbuild.StringLiteral(plan.Description.ValueString())
	}
	if !plan.APIVersion.IsNull() {
		args["api_version"] = fmt.Sprintf("%d", plan.APIVersion.ValueInt64())
	}
	if !plan.RequiredResources.IsNull() {
		requiredResources, ok := diveRequiredResourcesArg(ctx, plan.RequiredResources, &resp.Diagnostics)
		if !ok {
			return
		}
		args["required_resources"] = requiredResources
	}
	query := "SELECT id::VARCHAR FROM MD_CREATE_DIVE" + sqlbuild.NamedArgs(args)
	var id string
	if err := client.QueryRow(ctx, query).Scan(&id); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck Dive", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	if !r.readDive(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !diveStatusChanged(requestedStatus, plan.Status) {
		return
	}
	if !r.updateDiveStatus(ctx, client, plan.ID.ValueString(), requestedStatus.ValueString(), &resp.Diagnostics) {
		return
	}
	if !r.readDive(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *diveResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state diveModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readDive(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *diveResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan, state diveModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	metadataArgs, updateMetadata := diveMetadataArgs(&plan, &state, &resp.Diagnostics)
	contentArgs, updateContent := diveContentArgs(ctx, &plan, &state, &resp.Diagnostics)
	updateStatus := diveStatusChanged(plan.Status, state.Status)
	if resp.Diagnostics.HasError() {
		return
	}
	if updateMetadata && !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_dive_metadata", "motherduck_dive") {
		return
	}
	if updateContent && !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_dive_content", "motherduck_dive") {
		return
	}
	if updateStatus && !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_dive_status", "motherduck_dive") {
		return
	}
	if (updateMetadata || updateContent || updateStatus) && !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_get_dive", "motherduck_dive") {
		return
	}
	if updateMetadata {
		metadataArgs["id"] = sqlbuild.StringLiteral(state.ID.ValueString()) + "::UUID"
		if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_UPDATE_DIVE_METADATA"+sqlbuild.NamedArgs(metadataArgs)); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck Dive metadata", err.Error())
			return
		}
	}
	if updateContent {
		contentArgs["id"] = sqlbuild.StringLiteral(state.ID.ValueString()) + "::UUID"
		if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_UPDATE_DIVE_CONTENT"+sqlbuild.NamedArgs(contentArgs)); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck Dive content", err.Error())
			return
		}
	}
	if updateStatus && !r.updateDiveStatus(ctx, client, state.ID.ValueString(), plan.Status.ValueString(), &resp.Diagnostics) {
		return
	}
	r.readDive(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *diveResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_delete_dive", "motherduck_dive") {
		return
	}
	var state diveModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "SELECT * FROM MD_DELETE_DIVE(id := " + sqlbuild.StringLiteral(state.ID.ValueString()) + "::UUID)"
	if err := retry.SQL(ctx, func() error {
		_, err := client.QueryRowsJSON(ctx, query)
		return err
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck Dive", err.Error())
	}
}

func (r *diveResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importUUIDID(ctx, req.ID, resp)
}

func (r *diveResource) readDive(ctx context.Context, model *diveModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_get_dive", "motherduck_dive") {
		return false
	}
	var title, description, created, updated, ownerName, content, status, statusChangedAt, statusSetBy stdsql.NullString
	var currentVersion, statusVersion stdsql.NullInt64
	statusAvailable := false
	if err := retry.SQL(ctx, func() error {
		var existsErr error
		statusAvailable, existsErr = client.Exists(ctx, "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = 'md_update_dive_status'")
		return existsErr
	}); err != nil {
		diags.AddError("Unable to inspect MotherDuck Dive status support", err.Error())
		return false
	}
	columns := "title, description, current_version, created_at::VARCHAR, updated_at::VARCHAR, owner_name, content"
	scanTargets := []any{&title, &description, &currentVersion, &created, &updated, &ownerName, &content}
	if statusAvailable {
		columns += ", status, status_changed_at::VARCHAR, status_set_by::VARCHAR, status_applies_to_version"
		scanTargets = append(scanTargets, &status, &statusChangedAt, &statusSetBy, &statusVersion)
	}
	query := "SELECT " + columns + " FROM MD_GET_DIVE(id := " + sqlbuild.StringLiteral(model.ID.ValueString()) + "::UUID)"
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(scanTargets...)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Dive", err.Error())
		return false
	}
	model.Title = nullString(title)
	model.Description = nullString(description)
	if currentVersion.Valid {
		model.CurrentVersion = types.Int64Value(currentVersion.Int64)
	}
	model.CreatedAt = nullString(created)
	model.UpdatedAt = nullString(updated)
	model.OwnerName = nullString(ownerName)
	model.Content = nullString(content)
	if statusAvailable {
		model.Status = nullString(status)
		model.StatusChangedAt = nullString(statusChangedAt)
		model.StatusSetBy = nullString(statusSetBy)
		if statusVersion.Valid {
			model.StatusVersion = types.Int64Value(statusVersion.Int64)
		} else {
			model.StatusVersion = types.Int64Null()
		}
	} else {
		model.Status = types.StringNull()
		model.StatusChangedAt = types.StringNull()
		model.StatusSetBy = types.StringNull()
		model.StatusVersion = types.Int64Null()
	}
	return true
}

func diveStatusConfigured(status types.String) bool {
	return !status.IsNull() && !status.IsUnknown()
}

func diveStatusChanged(plan, state types.String) bool {
	return diveStatusConfigured(plan) && !plan.Equal(state)
}

func (r *diveResource) updateDiveStatus(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, id, status string, diags *diag.Diagnostics) bool {
	args := map[string]string{
		"id":     sqlbuild.StringLiteral(id) + "::UUID",
		"status": sqlbuild.StringLiteral(status),
	}
	if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_UPDATE_DIVE_STATUS"+sqlbuild.NamedArgs(args)); err != nil {
		diags.AddError("Unable to update MotherDuck Dive status", err.Error())
		return false
	}
	return true
}

func diveMetadataArgs(plan, state *diveModel, diags *diag.Diagnostics) (map[string]string, bool) {
	args := map[string]string{}
	if !plan.Title.Equal(state.Title) {
		args["title"] = sqlbuild.StringLiteral(plan.Title.ValueString())
	}
	if !plan.Description.Equal(state.Description) {
		if plan.Description.IsNull() && !state.Description.IsNull() {
			diags.AddError(
				"Unable to clear MotherDuck Dive description",
				"The public Dive metadata update API does not currently expose a null-clear operation for description. Set description = \"\" to store an empty description, or replace the Dive resource if you need the live value to be null.",
			)
			return nil, false
		}
		args["description"] = sqlbuild.StringLiteral(plan.Description.ValueString())
	}
	return args, len(args) > 0
}

func diveContentArgs(ctx context.Context, plan, state *diveModel, diags *diag.Diagnostics) (map[string]string, bool) {
	requiredResourcesEqual := optionalListValuesEqual(plan.RequiredResources, state.RequiredResources)
	if plan.Content.Equal(state.Content) && plan.APIVersion.Equal(state.APIVersion) && requiredResourcesEqual {
		return nil, false
	}
	args := map[string]string{"content": sqlbuild.StringLiteral(plan.Content.ValueString())}
	if !plan.APIVersion.IsNull() {
		args["api_version"] = fmt.Sprintf("%d", plan.APIVersion.ValueInt64())
	}
	if !plan.RequiredResources.IsNull() {
		requiredResources, ok := diveRequiredResourcesArg(ctx, plan.RequiredResources, diags)
		if !ok {
			return nil, false
		}
		args["required_resources"] = requiredResources
	} else if !requiredResourcesEqual {
		args["required_resources"] = "NULL"
	}
	return args, true
}

func optionalListValuesEqual(left, right types.List) bool {
	if left.IsNull() && right.IsNull() {
		return true
	}
	return left.Equal(right)
}

func diveRequiredResourcesArg(ctx context.Context, value types.List, diags *diag.Diagnostics) (string, bool) {
	resources := []diveRequiredResourceModel{}
	diags.Append(value.ElementsAs(ctx, &resources, false)...)
	if diags.HasError() {
		return "", false
	}
	if len(resources) == 0 {
		return "[]::STRUCT(alias VARCHAR, url VARCHAR)[]", true
	}
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		parts = append(parts, fmt.Sprintf(
			"{alias: %s, url: %s}",
			sqlbuild.StringLiteral(resource.Alias.ValueString()),
			sqlbuild.StringLiteral(resource.URL.ValueString()),
		))
	}
	return "[" + strings.Join(parts, ", ") + "]", true
}

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

type flightRunResource struct{ baseResource }

type flightRunModel struct {
	ID                  types.String `tfsdk:"id"`
	FlightID            types.String `tfsdk:"flight_id"`
	Config              types.Map    `tfsdk:"config"`
	RunNumber           types.Int64  `tfsdk:"run_number"`
	Status              types.String `tfsdk:"status"`
	FlightVersion       types.Int64  `tfsdk:"flight_version"`
	CancelOnDestroy     types.Bool   `tfsdk:"cancel_on_destroy"`
	WaitForStatus       types.String `tfsdk:"wait_for_status"`
	PollIntervalSeconds types.Int64  `tfsdk:"poll_interval_seconds"`
	TimeoutSeconds      types.Int64  `tfsdk:"timeout_seconds"`
	CreatedAt           types.String `tfsdk:"created_at"`
}

const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))

func NewFlightRunResource() resource.Resource { return &flightRunResource{} }

func (r *flightRunResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flight_run"
}

func (r *flightRunResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Action-like resource that triggers an on-demand MotherDuck Flight run.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stable Terraform ID for this Flight run, derived from the Flight ID and run number.",
			},
			"flight_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Flight ID. Must be a UUID with no leading or trailing whitespace.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          uuidValidators(),
			},
			"config": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional string configuration for this on-demand Flight run. Keys must already exist on the Flight definition and must be valid Flight config names.",
				PlanModifiers:       mapRequiresReplace(),
				Validators:          flightConfigMapValidators(),
			},
			"run_number": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "MotherDuck run number assigned to this Flight run.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest Flight run status observed by the provider.",
			},
			"flight_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Flight version used by this run.",
			},
			"cancel_on_destroy": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, provider destroy attempts to cancel this Flight run.",
			},
			"wait_for_status": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional terminal status to wait for after triggering the run. The only supported value is `succeeded`; if the run reaches a failure status, the provider fails the apply without copying potentially sensitive Flight logs into diagnostics. Inspect logs separately with `motherduck_flight_logs`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          flightRunWaitStatusValidators(),
			},
			"poll_interval_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Polling interval in seconds when `wait_for_status` is set. Defaults to 10 seconds.",
				PlanModifiers:       int64RequiresReplace(),
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck Flight run poll interval", 1, maxDurationSeconds)},
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum number of seconds to wait when `wait_for_status` is set. Defaults to 600 seconds.",
				PlanModifiers:       int64RequiresReplace(),
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck Flight run timeout", 1, maxDurationSeconds)},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Flight run creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *flightRunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_run_flight", "motherduck_flight_run") {
		return
	}
	var plan flightRunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if waitForFlightRun(plan.WaitForStatus) {
		if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_flight_runs", "motherduck_flight_run") {
			return
		}
	}
	args := map[string]string{"flight_id": sqlbuild.StringLiteral(plan.FlightID.ValueString()) + "::UUID"}
	if !plan.Config.IsNull() {
		config := map[string]string{}
		resp.Diagnostics.Append(plan.Config.ElementsAs(ctx, &config, false)...)
		args["config"] = sqlbuild.MapLiteral(config)
	}
	var runID, status, created string
	var runNumber, version int64
	query := "SELECT run_id::VARCHAR, status, run_number, flight_version, created_at::VARCHAR FROM MD_RUN_FLIGHT" + sqlbuild.NamedArgs(args)
	if err := client.QueryRow(ctx, query).Scan(&runID, &status, &runNumber, &version, &created); err != nil {
		resp.Diagnostics.AddError("Unable to run MotherDuck Flight", err.Error())
		return
	}
	plan.ID = types.StringValue(runID)
	plan.Status = types.StringValue(status)
	plan.RunNumber = types.Int64Value(runNumber)
	plan.FlightVersion = types.Int64Value(version)
	plan.CreatedAt = types.StringValue(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if waitForFlightRun(plan.WaitForStatus) {
		r.waitForFlightRun(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func waitForFlightRun(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != ""
}

func (r *flightRunResource) waitForFlightRun(ctx context.Context, model *flightRunModel, diags *diag.Diagnostics) {
	wantStatus := normalizeFlightRunStatus(model.WaitForStatus.ValueString())
	pollInterval := time.Duration(int64ValueOrDefault(model.PollIntervalSeconds, 10)) * time.Second
	timeout := time.Duration(int64ValueOrDefault(model.TimeoutSeconds, 600)) * time.Second
	deadline := time.Now().Add(timeout)

	for {
		status := normalizeFlightRunStatus(model.Status.ValueString())
		if status == wantStatus {
			return
		}
		if flightRunFailed(status) {
			detail := fmt.Sprintf("Flight run %d reached status %q while waiting for %q. Inspect logs with the sensitive motherduck_flight_logs data source; logs are not copied into diagnostics because they can contain credentials or other sensitive output.", model.RunNumber.ValueInt64(), model.Status.ValueString(), wantStatus)
			diags.AddError("MotherDuck Flight run failed", detail)
			return
		}
		if !time.Now().Before(deadline) {
			detail := fmt.Sprintf("Timed out after %d seconds waiting for Flight run %d to reach %q. Last status was %q. Inspect logs with the sensitive motherduck_flight_logs data source; logs are not copied into diagnostics because they can contain credentials or other sensitive output.", int64ValueOrDefault(model.TimeoutSeconds, 600), model.RunNumber.ValueInt64(), wantStatus, model.Status.ValueString())
			diags.AddError("Timed out waiting for MotherDuck Flight run", detail)
			return
		}

		sleepFor := pollInterval
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor <= 0 {
			continue
		}
		if err := retry.Sleep(ctx, sleepFor); err != nil {
			diags.AddError("Interrupted while waiting for MotherDuck Flight run", err.Error())
			return
		}
		if found := r.readFlightRunStatus(ctx, model, diags); !found && !diags.HasError() {
			continue
		}
		if diags.HasError() {
			return
		}
	}
}

func (r *flightRunResource) readFlightRunStatus(ctx context.Context, model *flightRunModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var runID, status, created string
	var runNumber, version int64
	query := fmt.Sprintf(
		"SELECT run_id::VARCHAR, status, run_number, flight_version, created_at::VARCHAR FROM MD_LIST_FLIGHT_RUNS(flight_id := %s::UUID) WHERE run_number = %d",
		sqlbuild.StringLiteral(model.FlightID.ValueString()),
		model.RunNumber.ValueInt64(),
	)
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(&runID, &status, &runNumber, &version, &created)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Flight run", err.Error())
		return false
	}
	model.ID = types.StringValue(runID)
	model.Status = types.StringValue(status)
	model.RunNumber = types.Int64Value(runNumber)
	model.FlightVersion = types.Int64Value(version)
	model.CreatedAt = types.StringValue(created)
	return true
}

func flightRunFailed(status string) bool {
	switch normalizeFlightRunStatus(status) {
	case "failed", "error", "errored", "canceled", "cancelled", "timed_out", "timeout":
		return true
	default:
		return false
	}
}

func normalizeFlightRunStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	return strings.TrimPrefix(status, "run_status_")
}

func int64ValueOrDefault(value types.Int64, defaultValue int64) int64 {
	if value.IsNull() || value.IsUnknown() {
		return defaultValue
	}
	return value.ValueInt64()
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

func (r *flightRunResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state flightRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_flight_runs", "motherduck_flight_run") {
		return
	}
	if found := r.readFlightRunStatus(ctx, &state, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *flightRunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state flightRunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.RunNumber = state.RunNumber
	plan.Status = state.Status
	plan.FlightVersion = state.FlightVersion
	plan.CreatedAt = state.CreatedAt
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *flightRunResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state flightRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.CancelOnDestroy.IsNull() || !state.CancelOnDestroy.ValueBool() {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_cancel_flight_run", "motherduck_flight_run") {
		return
	}
	query := fmt.Sprintf("CALL MD_CANCEL_FLIGHT_RUN(flight_id := %s::UUID, run_number := %d)", sqlbuild.StringLiteral(state.FlightID.ValueString()), state.RunNumber.ValueInt64())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) && !strings.Contains(strings.ToLower(err.Error()), "terminal") {
		resp.Diagnostics.AddError("Unable to cancel MotherDuck Flight run", err.Error())
	}
}

func (r *flightRunResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError("Import is not supported", "Flight runs are action-like resources and should be recreated instead of imported.")
}

func importUUIDID(ctx context.Context, id string, resp *resource.ImportStateResponse) {
	if !validateUUIDString(id, "Import ID", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
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

func addOptionalStringUpdate(args map[string]string, name string, plan, state types.String, nullValue string) {
	if plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		if nullValue != "" {
			args[name] = nullValue
		}
		return
	}
	args[name] = sqlbuild.StringLiteral(plan.ValueString())
}

func optionalStringFromLive(current types.String, live stdsql.NullString) types.String {
	if !live.Valid {
		return types.StringNull()
	}
	if live.String == "" && current.IsNull() {
		return types.StringNull()
	}
	return types.StringValue(live.String)
}

func optionalConfigOwnedStringFromLive(current types.String, live stdsql.NullString) types.String {
	if current.IsNull() {
		return types.StringNull()
	}
	return optionalStringFromLive(current, live)
}

func optionalConfigOwnedLowerStringFromLive(current types.String, live stdsql.NullString) types.String {
	if current.IsNull() {
		return types.StringNull()
	}
	return lowerNullString(live)
}

func optionalStringListFromJSON(ctx context.Context, current types.List, raw stdsql.NullString, field string, diags *diag.Diagnostics) types.List {
	values := []string{}
	if !decodeNullableJSON(raw, &values, "Unable to parse MotherDuck Flight "+field, diags) {
		return current
	}
	if len(values) == 0 && current.IsNull() {
		return types.ListNull(types.StringType)
	}
	value, valueDiags := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return current
	}
	return value
}

func optionalStringMapFromJSON(ctx context.Context, current types.Map, raw stdsql.NullString, field string, diags *diag.Diagnostics) types.Map {
	values := map[string]string{}
	if !decodeNullableJSON(raw, &values, "Unable to parse MotherDuck Flight "+field, diags) {
		return current
	}
	if len(values) == 0 && current.IsNull() {
		return types.MapNull(types.StringType)
	}
	value, valueDiags := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return current
	}
	return value
}
