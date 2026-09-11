package resources

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type shareGrantResource struct{ baseResource }

type shareGrantModel struct {
	ID          types.String `tfsdk:"id"`
	Share       types.String `tfsdk:"share"`
	Username    types.String `tfsdk:"username"`
	GranteeType types.String `tfsdk:"grantee_type"`
}

func NewShareGrantResource() resource.Resource { return &shareGrantResource{} }

func (r *shareGrantResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share_grant"
}

func (r *shareGrantResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Grants READ on a restricted MotherDuck share to one user or role.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share grant ID in `<share>/<grantee_type>/<username>` form.",
			},
			"share": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Restricted share name to grant.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Grantable MotherDuck user or service-account principal. Must be non-blank and must not include leading or trailing whitespace. Email-like principals are allowed. The PAT email, PAT session name, and `motherduck_current_user` value may not be valid share-grant usernames.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareGrantPrincipalValidators(),
			},
			"grantee_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("user"),
				MarkdownDescription: "Principal type: `user` or `role`. Defaults to `user`. The `username` field stores the principal name for both types.",
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
				Validators:          roleGranteeTypeValidators(),
			},
		},
	}
}

func (r *shareGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan shareGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := shareGrantStatement("GRANT", plan.Share.ValueString(), "TO", plan.GranteeType.ValueString(), plan.Username.ValueString())
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to grant MotherDuck share access", shareGrantErrorDetail(err))
		return
	}
	plan.ID = shareGrantID(plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.GranteeType.IsNull() || state.GranteeType.IsUnknown() {
		state.GranteeType = types.StringValue("user")
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_share_grantees", "motherduck_share_grant") {
		return
	}
	var exists bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		exists, existsErr = client.Exists(ctx, `SELECT count(*) FROM MD_LIST_SHARE_GRANTEES(?) WHERE lower(grantee_name) = lower(?) AND lower(grantee_type) = lower(?) AND lower(privilege) = 'read'`, state.Share.ValueString(), state.Username.ValueString(), state.GranteeType.ValueString())
		return existsErr
	})
	remove, err := shareGrantReadDecision(state.Share.ValueString(), exists, err)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck share grant", err.Error())
		return
	}
	if remove {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = shareGrantID(state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *shareGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Share grant updates are not supported", "Replace the grant to change share or username.")
}

func (r *shareGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.GranteeType.IsNull() || state.GranteeType.IsUnknown() {
		state.GranteeType = types.StringValue("user")
	}
	query := shareGrantStatement("REVOKE", state.Share.ValueString(), "FROM", state.GranteeType.ValueString(), state.Username.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to revoke MotherDuck share access", err.Error())
	}
}

func (r *shareGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 && len(parts) != 3 {
		resp.Diagnostics.AddError("Invalid import ID", "Expected `<share>/<username>` or `<share>/<grantee_type>/<username>`.")
		return
	}
	granteeType := "user"
	usernameIndex := 1
	if len(parts) == 3 {
		granteeType = strings.ToLower(parts[1])
		usernameIndex = 2
	}
	if !validateSQLImportIDPart(parts[0], "`<share>/<grantee_type>/<username>`", &resp.Diagnostics) {
		return
	}
	if granteeType != "user" && granteeType != "role" {
		resp.Diagnostics.AddError("Invalid import ID", "Grantee type must be `user` or `role`.")
		return
	}
	if !validateShareGrantPrincipalImportID(parts[usernameIndex], "`<share>/<grantee_type>/<username>`", &resp.Diagnostics) {
		return
	}
	canonicalID := parts[0] + "/" + granteeType + "/" + parts[usernameIndex]
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), canonicalID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("share"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("grantee_type"), granteeType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), parts[usernameIndex])...)
}

// shareGrantReadDecision classifies a grantee-listing refresh result. A
// not-found error means the share itself was dropped out of band, so the
// grant no longer exists and must be removed from state instead of failing
// every subsequent refresh and destroy.
func shareGrantReadDecision(share string, exists bool, err error) (remove bool, readErr error) {
	if err != nil {
		if isNotFoundFor(err, share) {
			return true, nil
		}
		return false, err
	}
	return !exists, nil
}

func shareGrantID(model shareGrantModel) types.String {
	return types.StringValue(model.Share.ValueString() + "/" + model.GranteeType.ValueString() + "/" + model.Username.ValueString())
}

func shareGrantStatement(action, share, preposition, granteeType, username string) string {
	return action + " READ ON SHARE " + sqlbuild.QuoteIdentifier(share) + " " + preposition + " " + strings.ToUpper(granteeType) + " " + sqlbuild.QuoteIdentifier(username)
}

func shareGrantErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	detail := err.Error()
	if strings.Contains(strings.ToLower(detail), "unable to find user") {
		return detail + "\n\nUse a grantable MotherDuck user or service-account principal. The PAT email, PAT session name, and `motherduck_current_user` value may not be valid share-grant usernames."
	}
	return detail
}
