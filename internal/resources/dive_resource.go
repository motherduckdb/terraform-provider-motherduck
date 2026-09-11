package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
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
		MarkdownDescription: "Experimental: manages a MotherDuck Dive through public SQL table functions. Prefer application deployment tooling for Dive content. This surface is outside the provider's stable support commitment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Dive ID assigned by MotherDuck.",
			},
			"title": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Visible Dive title.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive description. Set this to an empty string to clear the visible description. Removing an existing configured value is rejected because the public SQL update surface does not expose a null-clear operation.",
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Dive content source sent to MotherDuck.",
			},
			"api_version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive API version passed to MotherDuck when creating or updating content. Omit this to use the MotherDuck default. The public MD_GET_DIVE output does not report this value, so import cannot recover it. Keep it configured and expect one corrective update after import.",
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
				MarkdownDescription: "Dive governance status. New Dives default to `draft`. Owners can set `draft`, `ready`, or `archived`, while `endorsed` requires organization-admin permission.",
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
		resp.Diagnostics.AddError("Unable to create MotherDuck Dive", sensitiveWriteDiagnostic("Dive", err, diveSensitiveValues(ctx, plan.RequiredResources)))
		return
	}
	plan.ID = types.StringValue(id)
	// Persist the ID before the read-back so a failed or empty MD_GET_DIVE
	// leaves a tainted resource in state instead of an orphaned remote Dive.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.readDive(ctx, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Unable to read MotherDuck Dive", "Dive was created but could not be read through MD_GET_DIVE.")
		}
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
			resp.Diagnostics.AddError("Unable to update MotherDuck Dive content", sensitiveWriteDiagnostic("Dive", err, diveSensitiveValues(ctx, plan.RequiredResources)))
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
	}); err != nil && !isDiveNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck Dive", err.Error())
	}
}

func (r *diveResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importUUIDID(ctx, req.ID, resp)
}

// Dives report a missing ID as a typed Invalid Error, not a Catalog Error.
// Match only that exact service response so permission and dependency failures
// remain diagnostics rather than silently discarding the managed Dive's state.
func isDiveNotFound(err error) bool {
	if isNotFound(err) {
		return true
	}
	var duckErr *duckdb.Error
	return errors.As(err, &duckErr) && duckErr.Type == duckdb.ErrorTypeInvalid &&
		strings.TrimSpace(duckErr.Msg) == "Invalid Error: MDExternalException: Could not find Dive"
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
	if err == stdsql.ErrNoRows || isDiveNotFound(err) {
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
