package resources

import (
	"context"
	"fmt"
	"strings"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &roleResource{}
	_ resource.ResourceWithConfigure   = &roleResource{}
	_ resource.ResourceWithImportState = &roleResource{}
	_ resource.Resource                = &roleGrantResource{}
	_ resource.ResourceWithConfigure   = &roleGrantResource{}
	_ resource.ResourceWithImportState = &roleGrantResource{}
)

type roleResource struct{ baseResource }

type roleModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	RoleType      types.String `tfsdk:"role_type"`
	IncludedRoles types.List   `tfsdk:"included_roles"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

func NewRoleResource() resource.Resource { return &roleResource{} }

func (r *roleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a custom MotherDuck role.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Role resource ID. This is the role name.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Custom role name. Role names are stored lowercase and must be 3 to 255 characters, start with a letter, and contain only letters, digits, hyphens, and underscores. The reserved role names `admin`, `builder`, and `explorer` are not allowed.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          roleNameValidators(),
			},
			"role_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Role type reported by MotherDuck.",
			},
			"included_roles": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Roles directly included in this role.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Role creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := client.Exec(ctx, "CREATE ROLE "+sqlbuild.QuoteIdentifier(plan.Name.ValueString())); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck role", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	if !r.readRole(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck role", "Role was created but was not returned by SHOW ALL ROLES.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.readRole(ctx, &state, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Role updates are not supported", "Replace the role to change its name. Manage role membership with motherduck_role_grant resources.")
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := retry.SQL(ctx, func() error {
		return client.Exec(ctx, "DROP ROLE IF EXISTS "+sqlbuild.QuoteIdentifier(state.Name.ValueString()))
	}); err != nil {
		resp.Diagnostics.AddError("Unable to drop MotherDuck role", err.Error())
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if detail, ok := validateRoleNameValue(req.ID); !ok {
		resp.Diagnostics.AddError("Invalid MotherDuck role import ID", detail)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
}

func (r *roleResource) readRole(ctx context.Context, model *roleModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	rows, err := showRows(ctx, client, "SHOW ALL ROLES")
	if err != nil {
		if isNotFound(err) {
			return false
		}
		if mdsql.IsUnsupportedCommand(err) {
			diags.AddError(
				"MotherDuck SQL command unavailable",
				"SHOW ALL ROLES is not supported by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_role resource.",
			)
			return false
		}
		diags.AddError("Unable to read MotherDuck role", err.Error())
		return false
	}
	row, ok := findRoleRow(rows, model.Name.ValueString())
	if !ok {
		return false
	}
	model.ID = types.StringValue(model.Name.ValueString())
	model.RoleType = optionalLowerRowString(row["role_type"])
	model.IncludedRoles = stringListFromAny(ctx, row["included_roles"], "included_roles", diags)
	model.CreatedAt = optionalRowString(row["created_at"])
	return !diags.HasError()
}

func findRoleRow(rows []map[string]any, name string) (map[string]any, bool) {
	for _, row := range rows {
		if roleName, ok := row["role_name"].(string); ok && strings.EqualFold(roleName, name) {
			return row, true
		}
	}
	return nil, false
}

func stringListFromAny(ctx context.Context, value any, field string, diags *diag.Diagnostics) types.List {
	if value == nil {
		return types.ListNull(types.StringType)
	}
	items, ok := value.([]any)
	if !ok {
		diags.AddError("Unable to parse MotherDuck "+field, fmt.Sprintf("Expected a list, got %T.", value))
		return types.ListNull(types.StringType)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			diags.AddError("Unable to parse MotherDuck "+field, fmt.Sprintf("Expected a list of strings, got element %T.", item))
			return types.ListNull(types.StringType)
		}
		values = append(values, text)
	}
	if len(values) == 0 {
		return types.ListNull(types.StringType)
	}
	list, listDiags := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(listDiags...)
	if listDiags.HasError() {
		return types.ListNull(types.StringType)
	}
	return list
}

func optionalRowString(value any) types.String {
	if text, ok := value.(string); ok {
		return types.StringValue(text)
	}
	return types.StringNull()
}

func optionalLowerRowString(value any) types.String {
	if text, ok := value.(string); ok {
		return types.StringValue(strings.ToLower(text))
	}
	return types.StringNull()
}

type roleGrantResource struct{ baseResource }

type roleGrantModel struct {
	ID          types.String `tfsdk:"id"`
	RoleName    types.String `tfsdk:"role_name"`
	GranteeName types.String `tfsdk:"grantee_name"`
	GranteeType types.String `tfsdk:"grantee_type"`
	GrantedAt   types.String `tfsdk:"granted_at"`
}

func NewRoleGrantResource() resource.Resource { return &roleGrantResource{} }

func (r *roleGrantResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_grant"
}

func (r *roleGrantResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Grants one MotherDuck role directly to a user or another role.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Role grant ID in `<role_name>/<grantee_type>/<grantee_name>` form.",
			},
			"role_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Role to grant. Uses the lowercase MotherDuck role-name form: 3 to 255 characters, starting with a letter, containing only letters, digits, hyphens, and underscores.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          roleNameValidators(),
			},
			"grantee_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User, service-account, or role principal receiving the role.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareGrantPrincipalValidators(),
			},
			"grantee_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("user"),
				MarkdownDescription: "Principal type: `user` or `role`. Defaults to `user`.",
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
				Validators:          roleGranteeTypeValidators(),
			},
			"granted_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Grant creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *roleGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan roleGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := roleGrantStatement("GRANT", plan.RoleName.ValueString(), "TO", plan.GranteeType.ValueString(), plan.GranteeName.ValueString())
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to grant MotherDuck role", err.Error())
		return
	}
	plan.ID = roleGrantID(plan)
	if !r.readRoleGrant(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck role grant", "Role was granted but the direct membership was not returned by MotherDuck.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.readRoleGrant(ctx, &state, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Role grant updates are not supported", "Replace the grant to change its role or principal.")
}

func (r *roleGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state roleGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := roleGrantStatement("REVOKE", state.RoleName.ValueString(), "FROM", state.GranteeType.ValueString(), state.GranteeName.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to revoke MotherDuck role", err.Error())
	}
}

func (r *roleGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, "/", 3, "`<role_name>/<grantee_type>/<grantee_name>`", &resp.Diagnostics)
	if !ok {
		return
	}
	if detail, valid := validateRoleNameValue(parts[0]); !valid {
		resp.Diagnostics.AddError("Invalid MotherDuck role import ID", detail)
		return
	}
	granteeType := strings.ToLower(parts[1])
	if granteeType != "user" && granteeType != "role" {
		resp.Diagnostics.AddError("Invalid MotherDuck role grant import ID", "Grantee type must be `user` or `role`.")
		return
	}
	if detail, valid := validateShareGrantPrincipalValue(parts[2]); !valid {
		resp.Diagnostics.AddError("Invalid MotherDuck role grant import ID", detail)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("role_name"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("grantee_type"), granteeType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("grantee_name"), parts[2])...)
}

func (r *roleGrantResource) readRoleGrant(ctx context.Context, model *roleGrantModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	query := showRolesToStatement(model.GranteeType.ValueString(), model.GranteeName.ValueString())
	rows, err := showRows(ctx, client, query)
	if err != nil {
		if isNotFound(err) {
			return false
		}
		if mdsql.IsUnsupportedCommand(err) {
			diags.AddError(
				"MotherDuck SQL command unavailable",
				"SHOW ROLES TO USER/ROLE is not supported by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_role_grant resource.",
			)
			return false
		}
		diags.AddError("Unable to read MotherDuck role grant", err.Error())
		return false
	}
	grantedAt, ok := findDirectRoleGrant(rows, model.RoleName.ValueString())
	if !ok {
		return false
	}
	model.ID = roleGrantID(*model)
	model.GrantedAt = grantedAt
	return true
}

func showRolesToStatement(granteeType, granteeName string) string {
	keyword := "USER"
	if granteeType == "role" {
		keyword = "ROLE"
	}
	return "SHOW ROLES TO " + keyword + " " + sqlbuild.QuoteIdentifier(granteeName)
}

func findDirectRoleGrant(rows []map[string]any, roleName string) (types.String, bool) {
	for _, row := range rows {
		name, ok := row["role_name"].(string)
		if !ok || !strings.EqualFold(name, roleName) {
			continue
		}
		if !truthyRowValue(row["is_direct"]) {
			continue
		}
		return optionalRowString(row["granted_at"]), true
	}
	return types.StringNull(), false
}

func truthyRowValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func roleGrantID(model roleGrantModel) types.String {
	return types.StringValue(model.RoleName.ValueString() + "/" + model.GranteeType.ValueString() + "/" + model.GranteeName.ValueString())
}

func roleGrantStatement(action, roleName, preposition, granteeType, granteeName string) string {
	return fmt.Sprintf(
		"%s ROLE %s %s %s %s",
		action,
		sqlbuild.QuoteIdentifier(roleName),
		preposition,
		strings.ToUpper(granteeType),
		sqlbuild.QuoteIdentifier(granteeName),
	)
}
