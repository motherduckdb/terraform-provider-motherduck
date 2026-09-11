package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"
)

type shareResource struct{ baseResource }

type shareModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	SourceDatabase types.String `tfsdk:"source_database"`
	Access         types.String `tfsdk:"access"`
	Visibility     types.String `tfsdk:"visibility"`
	UpdateMode     types.String `tfsdk:"update_mode"`
	IncludePattern types.List   `tfsdk:"include_pattern"`
	URL            types.String `tfsdk:"url"`
	CreatedTS      types.String `tfsdk:"created_ts"`
}

func NewShareResource() resource.Resource { return &shareResource{} }

func (r *shareResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share"
}

func (r *shareResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck database share.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Share resource ID. This is the share name.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Share name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"source_database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database to share.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"access": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Share access mode: `organization`, `restricted`, or `unrestricted`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareAccessValidators(),
			},
			"visibility": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Share visibility mode: `discoverable` or `hidden`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareVisibilityValidators(),
			},
			"update_mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Share update mode: `manual` or `automatic`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareUpdateModeValidators(),
			},
			"include_pattern": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional preview catalog include patterns. Null shares the entire database. An empty list shares no objects. Changes are applied in place. The MotherDuck client must have filtered shares enabled.",
			},
			"url": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Share URL reported by MotherDuck. This is sensitive because unrestricted share URLs can grant access.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *shareResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config shareModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.IncludePattern.IsNull() || config.IncludePattern.IsUnknown() {
		return
	}
	validateShareIncludePattern(config.IncludePattern, &resp.Diagnostics)
}

func validateShareIncludePattern(includePattern types.List, diags *diag.Diagnostics) {
	totalLength := 0
	for _, element := range includePattern.Elements() {
		pattern, ok := element.(types.String)
		if !ok || pattern.IsUnknown() {
			return
		}
		if pattern.IsNull() {
			diags.AddAttributeError(path.Root("include_pattern"), "Invalid MotherDuck share include pattern", "Include-pattern entries must not be null.")
			return
		}
		totalLength += len(pattern.ValueString())
	}
	if totalLength > 16384 {
		diags.AddAttributeError(
			path.Root("include_pattern"),
			"Invalid MotherDuck share include pattern",
			fmt.Sprintf("The combined include-pattern length is %d characters. MotherDuck accepts at most 16384.", totalLength),
		)
	}
}

func (r *shareResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	options := []string{}
	if !plan.Access.IsNull() && plan.Access.ValueString() != "" {
		options = append(options, "ACCESS "+strings.ToUpper(plan.Access.ValueString()))
	}
	if !plan.Visibility.IsNull() && plan.Visibility.ValueString() != "" {
		options = append(options, "VISIBILITY "+strings.ToUpper(plan.Visibility.ValueString()))
	}
	if !plan.UpdateMode.IsNull() && plan.UpdateMode.ValueString() != "" {
		options = append(options, "UPDATE "+strings.ToUpper(plan.UpdateMode.ValueString()))
	}
	if !plan.IncludePattern.IsNull() {
		pattern, ok := shareIncludePattern(ctx, plan.IncludePattern, &resp.Diagnostics)
		if !ok {
			return
		}
		options = append(options, "INCLUDE_PATTERN "+sqlbuild.StringLiteral(pattern))
	}
	if err := client.AttachDatabase(ctx, plan.SourceDatabase.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck source database", err.Error())
		return
	}
	query := "CREATE SHARE " + sqlbuild.QuoteIdentifier(plan.Name.ValueString()) + " FROM " + sqlbuild.QuoteIdentifier(plan.SourceDatabase.ValueString()) + sqlbuild.Options(options...)
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck share", err.Error())
		return
	}
	prepareShareCreateState(&plan)
	found := r.readShare(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck share", "Share was created but was not visible in MD_INFORMATION_SCHEMA.OWNED_SHARES.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readShare(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *shareResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan, state shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	if !plan.IncludePattern.Equal(state.IncludePattern) {
		query := "ALTER SHARE " + sqlbuild.QuoteIdentifier(state.Name.ValueString())
		if plan.IncludePattern.IsNull() {
			query += " RESET INCLUDE_PATTERN"
		} else {
			pattern, ok := shareIncludePattern(ctx, plan.IncludePattern, &resp.Diagnostics)
			if !ok {
				return
			}
			query += " SET INCLUDE_PATTERN " + sqlbuild.StringLiteral(pattern)
		}
		if err := client.Exec(ctx, query); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck share include pattern", err.Error())
			return
		}
	}
	if !r.readShare(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck share", "Share was updated but was not visible in MD_INFORMATION_SCHEMA.OWNED_SHARES.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "DROP SHARE IF EXISTS " + sqlbuild.QuoteIdentifier(state.Name.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil {
		resp.Diagnostics.AddError("Unable to drop MotherDuck share", err.Error())
	}
}

func (r *shareResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *shareResource) readShare(ctx context.Context, model *shareModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	share, err := sqlcatalog.ReadOwnedShare(ctx, client, model.Name.ValueString())
	if err == stdsql.ErrNoRows {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck share", err.Error())
		return false
	}
	applyOwnedShare(ctx, model, share, diags)
	return true
}

// applyOwnedShare maps an OWNED_SHARES row onto the share model. Share option
// attributes are optional+computed so imports can discover their live values
// and omitted options can retain the server defaults without planning a
// replacement.
func applyOwnedShare(ctx context.Context, model *shareModel, share sqlcatalog.OwnedShare, diags *diag.Diagnostics) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.URL = nullString(share.URL)
	model.SourceDatabase = nullString(share.SourceDatabase)
	model.Access = lowerNullString(share.Access)
	model.Visibility = lowerNullString(share.Visibility)
	model.UpdateMode = lowerNullString(share.UpdateMode)
	model.IncludePattern = optionalStringListFromJSON(ctx, model.IncludePattern, share.IncludePattern, "include_pattern", diags)
	model.CreatedTS = nullString(share.CreatedTS)
}

func prepareShareCreateState(model *shareModel) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.URL = knownString(model.URL)
	model.CreatedTS = knownString(model.CreatedTS)
}

func shareIncludePattern(ctx context.Context, value types.List, diags *diag.Diagnostics) (string, bool) {
	var patterns []string
	diags.Append(value.ElementsAs(ctx, &patterns, false)...)
	if diags.HasError() {
		return "", false
	}
	return strings.Join(patterns, ","), true
}
