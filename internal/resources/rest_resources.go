package resources

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &serviceAccountResource{}
	_ resource.ResourceWithConfigure      = &serviceAccountResource{}
	_ resource.ResourceWithImportState    = &serviceAccountResource{}
	_ resource.ResourceWithModifyPlan     = &serviceAccountResource{}
	_ resource.Resource                   = &accessTokenResource{}
	_ resource.ResourceWithConfigure      = &accessTokenResource{}
	_ resource.ResourceWithImportState    = &accessTokenResource{}
	_ resource.Resource                   = &ducklingConfigResource{}
	_ resource.ResourceWithConfigure      = &ducklingConfigResource{}
	_ resource.ResourceWithImportState    = &ducklingConfigResource{}
	_ resource.ResourceWithValidateConfig = &ducklingConfigResource{}
)

type serviceAccountResource struct {
	baseResource
}

type serviceAccountModel struct {
	ID          types.String `tfsdk:"id"`
	Username    types.String `tfsdk:"username"`
	Description types.String `tfsdk:"description"`
}

// serviceAccountDescriptionUnverified marks state with no recorded
// description that may adopt the configured one once without a plan-time
// error. No MotherDuck API returns a service account description. Import sets
// it to serviceAccountDescriptionImported, and removing the description from
// the configuration sets it to serviceAccountDescriptionRemoved.
const (
	serviceAccountDescriptionUnverified = "description_unverified"
	serviceAccountDescriptionImported   = "true"
	serviceAccountDescriptionRemoved    = `"removed"`
)

func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

func (r *serviceAccountResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Creates a MotherDuck service account through the public REST API. Deleting the account permanently deletes the account and all data it owns, so destroy dependent databases and other data first. The provider manages only service accounts. Refresh, import, and destroy fail when the username belongs to a human user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Service account ID. This is the service account username returned by MotherDuck.",
				PlanModifiers:       []planmodifier.String{idFollowsUsernameOnUpdate{}},
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Unique service account username. Must be 3-255 UTF-16 code units, start with a letter, and contain only letters, ASCII digits, and underscores. An existing service account with a legacy username that also contains `-`, `@`, or `.`, such as `mdw-writer@motherduck-com`, can be imported and managed, but MotherDuck does not create new accounts with these characters, so a plan that would create one fails. MotherDuck compares usernames case-insensitively, so a change in letter case updates the resource in place. Any other change replaces the account.",
				Validators:          serviceAccountUsernameValidators(),
				PlanModifiers:       usernameRequiresReplace(),
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional free-form notes on the account's purpose. Must be non-blank and 1-1000 characters. MotherDuck sets the description only when it creates the account and has no API to read or change it, so changing it after creation fails at plan time instead of replacing the account and deleting its data. Removing it from the configuration only clears it from state.",
				Validators:          serviceAccountDescriptionValidators(),
			},
		},
	}
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	createReq := mdrest.CreateServiceAccountRequest{Username: plan.Username.ValueString()}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		createReq.Description = plan.Description.ValueString()
	}
	created, err := client.CreateServiceAccount(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck service account", err.Error())
		return
	}
	plan.ID = types.StringValue(created.Username)
	plan.Username = types.StringValue(created.Username)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if createReq.Description != "" && created.Description != createReq.Description {
		// An error here would taint the account, and replacing a service
		// account deletes its data, so the mismatch is only a warning.
		resp.Diagnostics.AddAttributeWarning(
			path.Root("description"),
			"MotherDuck did not confirm the service account description",
			"MotherDuck created service account "+created.Username+" but did not return the requested description. The MotherDuck region may not support service account descriptions yet. Terraform recorded the configured description, but MotherDuck may not have stored it.",
		)
	}
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// MotherDuck has no GET /v1/users/{username}, so the organization listing
	// is the only source that says whether a username is a service account.
	// The listing pages by offset and can skip a live user, so a username
	// absent from it is removed only after a probe confirms it is gone.
	user, err := findUser(ctx, client, state.Username.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck service account", err.Error())
		return
	}
	if user == nil {
		gone, err := confirmUserGone(ctx, client, state.Username.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Unable to read MotherDuck service account", err.Error())
			return
		}
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(serviceAccountUnconfirmedDiagnostic(state.Username.ValueString()))
		return
	}
	if !user.IsServiceAccount {
		resp.Diagnostics.Append(notServiceAccountDiagnostic(user.Username))
	}
}

// ModifyPlan rejects a new account whose username MotherDuck would refuse,
// such as a legacy username that only an existing account can have.
//
// It also rejects a description change. MotherDuck cannot change a
// service account description, and replacing the account to apply one would
// delete its data. Removing the description only clears it from state. An
// imported account, or one whose description was removed from the
// configuration, has no recorded description, so it may adopt the configured
// one.
func (r *serviceAccountResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.warnMissingAdminToken(req, resp)
	if req.Plan.Raw.IsNull() {
		return
	}
	var plannedUsername, currentUsername types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("username"), &plannedUsername)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("username"), &currentUsername)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	// A rename already replaces the account, and the new account is created
	// with the new username and description.
	creates := req.State.Raw.IsNull() || !strings.EqualFold(plannedUsername.ValueString(), currentUsername.ValueString())
	if creates {
		validateNewServiceAccountUsername(plannedUsername, &resp.Diagnostics)
		return
	}
	var planned, current types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("description"), &planned)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("description"), &current)...)
	if resp.Diagnostics.HasError() || planned.IsUnknown() || planned.IsNull() || planned.Equal(current) {
		return
	}
	if current.IsNull() {
		unverified, diags := req.Private.GetKey(ctx, serviceAccountDescriptionUnverified)
		resp.Diagnostics.Append(diags...)
		if len(unverified) > 0 {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(
		path.Root("description"),
		"Service account description cannot change",
		"MotherDuck sets a service account description only when it creates the account and has no API to change it. "+
			"Replacing the account to apply a new description would delete all data the account owns, so the provider does not plan that. "+
			"Restore the previous description in the configuration. To record the new description without changing the account or its data, remove the resource from state with `terraform state rm` and import it again with `terraform import`. "+
			"MotherDuck keeps the description the account was created with. To recreate the account with the new description and lose its data, destroy it first with `terraform destroy -target`.",
	)
}

// validateNewServiceAccountUsername applies the MotherDuck creation rule at
// plan time. The configuration validator also accepts the legacy usernames of
// existing accounts, which MotherDuck refuses for a new account.
func validateNewServiceAccountUsername(username types.String, diags *diag.Diagnostics) {
	if username.IsUnknown() || username.IsNull() {
		return
	}
	if detail, ok := validateServiceAccountUsernameValue(username.ValueString()); !ok {
		diags.AddAttributeError(
			path.Root("username"),
			"Invalid MotherDuck service account username",
			"MotherDuck cannot create service account "+username.ValueString()+". "+detail+
				" A legacy username with other characters, such as hyphens, at signs, or periods, is accepted only for an existing service account. Import the account with `terraform import`, or choose a username that follows the rule.",
		)
	}
}

// Update applies only changes that need no API call. The plan replaces the
// account for any username change other than letter case, and ModifyPlan
// rejects description changes except a removal or the adoption of a
// description by state that has none recorded.
func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !strings.EqualFold(plan.Username.ValueString(), state.Username.ValueString()) {
		resp.Diagnostics.AddError("Service account updates are not supported", "Change the username by replacing the resource.")
		return
	}
	plan.ID = plan.Username
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if plan.Description.Equal(state.Description) {
		return
	}
	if plan.Description.IsNull() {
		// Removal clears only the state. The account keeps its description, so
		// a description added later is adopted like one after import.
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, serviceAccountDescriptionUnverified, []byte(serviceAccountDescriptionRemoved))...)
		return
	}
	marker, diags := req.Private.GetKey(ctx, serviceAccountDescriptionUnverified)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.AddAttributeWarning(path.Root("description"), "Service account description recorded without verification", serviceAccountAdoptionDetail(marker))
	// The adopted description is now the recorded one, so later changes fail
	// at plan time like any other description change.
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, serviceAccountDescriptionUnverified, nil)...)
}

// serviceAccountAdoptionDetail explains a description recorded without an API
// call. Only state written by import is called an imported account.
func serviceAccountAdoptionDetail(marker []byte) string {
	if string(marker) == serviceAccountDescriptionImported {
		return "MotherDuck has no API to read or change a service account description, so Terraform recorded the configured description for this imported account without checking it against MotherDuck."
	}
	return "MotherDuck has no API to read or change a service account description, so Terraform recorded the configured description without checking it against MotherDuck. The account keeps the description it was created with."
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// DELETE /v1/users/{username} deletes any user and the data it owns, and
	// deleting the last human user drops the organization. Confirm the account
	// is still a service account right before deleting it.
	user, err := findUser(ctx, client, state.Username.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to verify MotherDuck service account before deletion", err.Error())
		return
	}
	if user == nil {
		gone, err := confirmUserGone(ctx, client, state.Username.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Unable to verify MotherDuck service account before deletion", err.Error())
			return
		}
		if !gone {
			resp.Diagnostics.Append(serviceAccountUnconfirmedDiagnostic(state.Username.ValueString()))
		}
		return
	}
	if !user.IsServiceAccount {
		resp.Diagnostics.Append(notServiceAccountDiagnostic(user.Username))
		return
	}
	if err := client.DeleteUser(ctx, state.Username.ValueString()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck service account", err.Error())
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The creation rule does not apply to an existing account, whose legacy
	// username can contain other characters. The Read that follows import
	// confirms the username is a service account.
	if !validateRESTUsernameImportID(req.ID, "`<username>`", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, serviceAccountDescriptionUnverified, []byte(serviceAccountDescriptionImported))...)
}

// findUser returns the organization member named username, or nil when the
// listing has none. GET /v1/users has no username filter, so it first walks
// only the service accounts, which finds a managed account without listing
// every human user. It then walks the human users, so a human user with the
// name is still found and refused. Callers check IsServiceAccount on the
// result, so a server that ignores the filter stays safe. MotherDuck compares
// usernames case-insensitively, so an exact match wins and a match that
// differs only in letter case is accepted.
func findUser(ctx context.Context, client *mdrest.Client, username string) (*mdrest.User, error) {
	for _, serviceAccounts := range []bool{true, false} {
		users, err := client.ListUsers(ctx, mdrest.ListUsersFilter{IsServiceAccount: &serviceAccounts})
		if err != nil {
			return nil, err
		}
		if user := matchUsername(users, username); user != nil {
			return user, nil
		}
	}
	return nil, nil
}

func matchUsername(users []mdrest.User, username string) *mdrest.User {
	var folded *mdrest.User
	for i := range users {
		if users[i].Username == username {
			return &users[i]
		}
		if folded == nil && strings.EqualFold(users[i].Username, username) {
			folded = &users[i]
		}
	}
	return folded
}

// confirmUserGone probes GET /v1/users/{username}/instances for a username the
// listing did not return. GET /v1/users pages by offset, so a user created or
// deleted during the walk can shift a live account past a page boundary.
// Only a NOT_FOUND answer proves the user is gone.
func confirmUserGone(ctx context.Context, client *mdrest.Client, username string) (bool, error) {
	_, err := client.GetDucklingConfig(ctx, username)
	if err == nil {
		return false, nil
	}
	if isNotFound(err) {
		return true, nil
	}
	return false, err
}

func serviceAccountUnconfirmedDiagnostic(username string) diag.Diagnostic {
	return diag.NewAttributeErrorDiagnostic(
		path.Root("username"),
		"Unable to confirm MotherDuck service account",
		"MotherDuck user "+username+" exists, but the organization user listing did not include it, so Terraform could not confirm that it is a service account. "+
			"The listing can skip a user while other users are created or deleted. Terraform kept the resource in state and made no change. Retry the operation.",
	)
}

func notServiceAccountDiagnostic(username string) diag.Diagnostic {
	return diag.NewAttributeErrorDiagnostic(
		path.Root("username"),
		"MotherDuck user is not a service account",
		"MotherDuck user "+username+" is a human user, not a service account. motherduck_service_account manages only service accounts, because deleting a user permanently deletes the user and the data it owns. "+
			"Terraform kept the resource in state and made no change. Remove it from state with `terraform state rm` and manage the user outside Terraform.",
	)
}

// idFollowsUsernameOnUpdate plans an existing resource's ID as its planned
// username, so a case-only username change updates the ID in the same plan.
type idFollowsUsernameOnUpdate struct{}

func (idFollowsUsernameOnUpdate) Description(context.Context) string {
	return "Plans the ID of an existing resource as its username."
}

func (m idFollowsUsernameOnUpdate) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (idFollowsUsernameOnUpdate) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var username types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("username"), &username)...)
	if resp.Diagnostics.HasError() || username.IsUnknown() || username.IsNull() {
		return
	}
	resp.PlanValue = username
}

type accessTokenResource struct {
	baseResource
}

type accessTokenModel struct {
	ID          types.String `tfsdk:"id"`
	Username    types.String `tfsdk:"username"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	TTL         types.Int64  `tfsdk:"ttl"`
	TokenType   types.String `tfsdk:"token_type"`
	Token       types.String `tfsdk:"token"`
	ExpireAt    types.String `tfsdk:"expire_at"`
	CreatedTS   types.String `tfsdk:"created_ts"`
	ReadOnly    types.Bool   `tfsdk:"read_only"`
}

func NewAccessTokenResource() resource.Resource { return &accessTokenResource{} }

func (r *accessTokenResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_token"
}

func (r *accessTokenResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Creates and revokes a MotherDuck access token for a user or service account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Access token ID assigned by MotherDuck.",
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Your own username or a service account username. MotherDuck refuses tokens for other human users. Must be non-blank and 1-255 characters. MotherDuck compares usernames case-insensitively, so a change in letter case updates the resource in place. Any other change replaces the token.",
				Validators:          restUsernameValidators(),
				PlanModifiers:       usernameRequiresReplace(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Access token label. Must be non-blank and 1-255 characters, counted in UTF-16 code units. It must be unique among the user's unexpired tokens. `MotherDuck Extension` and `MotherDuck Flights` are reserved.",
				Validators:          accessTokenNameValidators(),
				PlanModifiers:       stringRequiresReplace(),
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Optional free-form notes on the token's purpose. Must be non-blank and 1-1000 characters. MotherDuck cannot edit a description, so changing a configured value replaces the token. When omitted, Terraform keeps whatever description MotherDuck reports, including one set outside Terraform.",
				Validators:          accessTokenDescriptionValidators(),
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
			},
			"ttl": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional token lifetime in seconds. Must be between 300 and 31536000. Changing this value replaces the token.",
				Validators:          accessTokenTTLValidators(),
				PlanModifiers:       int64RequiresReplace(),
			},
			// No static default: a default would plan an in-place change to
			// read_write for a read_scaling token whose configuration omits the
			// type, such as an imported token, and the type cannot change in place.
			"token_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "`read_write` or `read_scaling`. A new token is `read_write` when this is omitted. Changing a configured value replaces the token. When omitted, Terraform keeps the type MotherDuck reports, so removing it from the configuration or importing a `read_scaling` token without it plans no change.",
				Validators:          accessTokenTypeValidators(),
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
			},
			"token": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Token secret, returned only at creation time.",
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"expire_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Token expiration timestamp reported by MotherDuck.",
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Token creation timestamp reported by MotherDuck.",
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"read_only": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the token is read-only according to MotherDuck.",
				PlanModifiers:       boolUseStateForUnknown(),
			},
		},
	}
}

func (r *accessTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var plan accessTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An omitted token type is unknown in the plan, and a new token then
	// defaults to read_write.
	tokenType := "read_write"
	if !plan.TokenType.IsNull() && !plan.TokenType.IsUnknown() && plan.TokenType.ValueString() != "" {
		tokenType = strings.ToLower(strings.TrimSpace(plan.TokenType.ValueString()))
	}
	createReq := mdrest.CreateTokenRequest{Name: plan.Name.ValueString(), TokenType: tokenType}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		createReq.Description = plan.Description.ValueString()
	}
	if !plan.TTL.IsNull() {
		ttl := plan.TTL.ValueInt64()
		createReq.TTL = &ttl
	}
	created, err := client.CreateToken(ctx, plan.Username.ValueString(), createReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck access token", err.Error())
		return
	}
	requestedDescription := createReq.Description
	setTokenModelFromREST(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if requestedDescription != "" && created.Description != requestedDescription {
		// The token exists and its state is saved, so Terraform can revoke or
		// replace it. The API silently drops fields it does not know, which
		// happens in a region that has not yet received token descriptions.
		resp.Diagnostics.AddAttributeError(
			path.Root("description"),
			"MotherDuck did not store the access token description",
			"MotherDuck created access token "+created.ID+" but did not return the requested description. The MotherDuck region may not support token descriptions yet. Remove description from the configuration, or retry after the region is updated.",
		)
	}
}

func (r *accessTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var state accessTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tokens, err := client.ListTokens(ctx, state.Username.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to list MotherDuck access tokens", err.Error())
		return
	}
	for _, token := range tokens {
		// Token IDs are UUIDs, which compare without regard to letter case.
		if strings.EqualFold(token.ID, state.ID.ValueString()) {
			secret := state.Token
			setTokenModelFromREST(&state, &token)
			state.Token = secret
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	// The token is absent from a listing the client walked to the last page,
	// so it was deleted or expired out of band and must leave state. The API
	// has no per-token GET to confirm, and the listing shape is unspecified
	// enough that a future server-side cap could hide live tokens. A token
	// past its recorded expire_at is expected to be gone. Anything else is
	// surfaced as a warning so a replacement never happens silently.
	if !accessTokenExpired(state.ExpireAt, time.Now()) {
		resp.Diagnostics.AddWarning(
			"MotherDuck access token not found",
			"Access token "+state.ID.ValueString()+" for user "+state.Username.ValueString()+" was not returned by the MotherDuck token listing and has been removed from state. "+
				"The next apply will create a replacement token with a new secret. If the token still exists in MotherDuck, import it again instead of applying.",
		)
	}
	resp.State.RemoveResource(ctx)
}

// accessTokenExpired reports whether expire_at is a known timestamp in the past.
// Unknown, null, or unparseable values count as not expired so the caller stays
// on the cautious path.
func accessTokenExpired(expireAt types.String, now time.Time) bool {
	if expireAt.IsNull() || expireAt.IsUnknown() {
		return false
	}
	raw := strings.TrimSpace(expireAt.ValueString())
	if raw == "" {
		return false
	}
	// The REST API returns expire_at as RFC 3339. RFC3339Nano also accepts
	// values without fractional seconds.
	when, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && when.Before(now)
}

// Update records a case-only username change, the only change the plan does
// not turn into a replacement. MotherDuck matches usernames case-insensitively,
// so the token needs no API call.
func (r *accessTokenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state accessTokenModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !strings.EqualFold(plan.Username.ValueString(), state.Username.ValueString()) {
		resp.Diagnostics.AddError("Access token updates are not supported", "Change token arguments by replacing the resource.")
		return
	}
	state.Username = plan.Username
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *accessTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var state accessTokenModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := client.DeleteToken(ctx, state.Username.ValueString(), state.ID.ValueString()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck access token", err.Error())
	}
}

func (r *accessTokenResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitImportID(req.ID, "/", 2, "`<username>/<token_id>`", &resp.Diagnostics)
	if !ok {
		return
	}
	if !validateRESTUsernameImportID(parts[0], "`<username>/<token_id>`", &resp.Diagnostics) {
		return
	}
	if !validateRESTImportIDPart(parts[1], "`<username>/<token_id>`", "access token ID", &resp.Diagnostics) {
		return
	}
	// MotherDuck token IDs are UUIDs. The delete endpoint treats any other
	// value as a token name, so a name must never reach state as an ID.
	if !validateUUIDString(parts[1], "MotherDuck access token ID import segment", &resp.Diagnostics) {
		return
	}
	// UUIDs are case-insensitive. MotherDuck stores them as PostgreSQL UUIDs,
	// which it reports in lower case.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.ToLower(parts[1]))...)
}

func setTokenModelFromREST(model *accessTokenModel, token *mdrest.Token) {
	model.ID = types.StringValue(token.ID)
	model.TokenType = normalizedTokenType(model.TokenType, token.TokenType)
	model.CreatedTS = types.StringValue(token.CreatedTS)
	model.ReadOnly = types.BoolValue(token.ReadOnly)
	if token.Name != "" {
		model.Name = types.StringValue(token.Name)
	}
	// MotherDuck stores an empty description as null and omits it on read.
	if token.Description != "" {
		model.Description = types.StringValue(token.Description)
	} else {
		model.Description = types.StringNull()
	}
	if token.Token != "" {
		model.Token = types.StringValue(token.Token)
	}
	if token.ExpireAt != "" {
		model.ExpireAt = types.StringValue(token.ExpireAt)
	} else {
		model.ExpireAt = types.StringNull()
	}
}

// normalizedTokenType lowercases the reported token type. An empty report
// keeps the known prior value, or the read_write default, so a listing that
// omits the type never plans an unsupported in-place update.
func normalizedTokenType(current types.String, reported string) types.String {
	tokenType := strings.ToLower(strings.TrimSpace(reported))
	if tokenType != "" {
		return types.StringValue(tokenType)
	}
	if !current.IsNull() && !current.IsUnknown() && current.ValueString() != "" {
		return current
	}
	return types.StringValue("read_write")
}

type ducklingConfigResource struct {
	baseResource
}

type ducklingConfigModel struct {
	ID                         types.String  `tfsdk:"id"`
	Username                   types.String  `tfsdk:"username"`
	ReadWriteInstanceSize      types.String  `tfsdk:"read_write_instance_size"`
	ReadWriteCooldownSeconds   types.Int64   `tfsdk:"read_write_cooldown_seconds"`
	ReadScalingInstanceSize    types.String  `tfsdk:"read_scaling_instance_size"`
	ReadScalingFlockSize       types.Float64 `tfsdk:"read_scaling_flock_size"`
	ReadScalingCooldownSeconds types.Int64   `tfsdk:"read_scaling_cooldown_seconds"`
}

func NewDucklingConfigResource() resource.Resource { return &ducklingConfigResource{} }

const ducklingUnsetCooldownDescription = "Pulse instances do not support cooldown seconds. " +
	"When unset on create, Terraform sends no cooldown, so MotherDuck applies its default and replaces any cooldown set outside Terraform. " +
	"After that, an unset cooldown is not managed: Terraform records the live value, keeps it while the instance size stays the same, and lets MotherDuck apply its default when the instance size changes. " +
	"With a Pulse instance size an unset cooldown is cleared, including one MotherDuck still reports after a plan limit lowered the size to Pulse. " +
	"Removing a configured value keeps the current cooldown. Set a value to change it."

func (r *ducklingConfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_duckling_config"
}

func (r *ducklingConfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages read-write and read-scaling Duckling configuration for a user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{idFollowsUsernameOnUpdate{}},
				MarkdownDescription: "Duckling configuration ID. This is the configured username.",
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User or service account username. Must be non-blank and 1-255 characters. MotherDuck compares usernames case-insensitively, so a change in letter case updates the resource in place. Any other change replaces it.",
				Validators:          restUsernameValidators(),
				PlanModifiers:       usernameRequiresReplace(),
			},
			"read_write_instance_size": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Read-write Duckling instance size. Must be one of `pulse`, `standard`, `jumbo`, `mega`, or `giga`.",
				Validators:          ducklingInstanceSizeValidators(),
			},
			"read_write_cooldown_seconds": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Optional read-write cooldown in seconds. Must be between 60 and 86400. " + ducklingUnsetCooldownDescription,
				Validators:          ducklingCooldownValidators(),
				PlanModifiers:       []planmodifier.Int64{ducklingCooldownPlanModifier{sizeAttribute: "read_write_instance_size"}},
			},
			"read_scaling_instance_size": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Read-scaling Duckling instance size. Must be one of `pulse`, `standard`, `jumbo`, `mega`, or `giga`.",
				Validators:          ducklingInstanceSizeValidators(),
			},
			"read_scaling_flock_size": schema.Float64Attribute{
				Required:            true,
				MarkdownDescription: "Read-scaling flock size. Must be between 0 and 64.",
				Validators:          ducklingFlockSizeValidators(),
			},
			"read_scaling_cooldown_seconds": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Optional read-scaling cooldown in seconds. Must be between 60 and 86400. " + ducklingUnsetCooldownDescription,
				Validators:          ducklingCooldownValidators(),
				PlanModifiers:       []planmodifier.Int64{ducklingCooldownPlanModifier{sizeAttribute: "read_scaling_instance_size"}},
			},
		},
	}
}

func (r *ducklingConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ducklingConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateDucklingCooldowns(&config, &resp.Diagnostics)
}

func (r *ducklingConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.putDucklingConfig(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ducklingConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var state ducklingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, err := client.GetDucklingConfig(ctx, state.Username.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read MotherDuck Duckling config", err.Error())
		return
	}
	setDucklingModelFromREST(&state, cfg)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ducklingConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.putDucklingConfig(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *ducklingConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// The public REST API exposes replacement updates but no delete/reset endpoint.
	resp.Diagnostics.AddWarning(
		"MotherDuck Duckling config was not reset",
		"The public REST API exposes replacement updates but no delete/reset endpoint. Terraform removed this resource from state, but the live Duckling configuration remains unchanged.",
	)
}

func (r *ducklingConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validateRESTUsernameImportID(req.ID, "`<username>`", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *ducklingConfigResource) putDucklingConfig(ctx context.Context, planGetter interface {
	Get(context.Context, any) diag.Diagnostics
}, stateSetter interface {
	Set(context.Context, any) diag.Diagnostics
}, diags *diag.Diagnostics) {
	client := r.rest(diags)
	if client == nil {
		return
	}
	var plan ducklingConfigModel
	diags.Append(planGetter.Get(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	if !validateDucklingCooldowns(&plan, diags) {
		return
	}
	cfg := mdrest.DucklingConfig{
		ReadWrite: mdrest.DucklingReadWriteConfig{
			InstanceSize: strings.ToLower(strings.TrimSpace(plan.ReadWriteInstanceSize.ValueString())),
		},
		ReadScaling: mdrest.DucklingReadScalingConfig{
			InstanceSize: strings.ToLower(strings.TrimSpace(plan.ReadScalingInstanceSize.ValueString())),
			FlockSize:    plan.ReadScalingFlockSize.ValueFloat64(),
		},
	}
	if !plan.ReadWriteCooldownSeconds.IsNull() && !plan.ReadWriteCooldownSeconds.IsUnknown() {
		v := plan.ReadWriteCooldownSeconds.ValueInt64()
		cfg.ReadWrite.CooldownSeconds = &v
	}
	if !plan.ReadScalingCooldownSeconds.IsNull() && !plan.ReadScalingCooldownSeconds.IsUnknown() {
		v := plan.ReadScalingCooldownSeconds.ValueInt64()
		cfg.ReadScaling.CooldownSeconds = &v
	}
	// A cooldown the configuration leaves unset is unknown on create and is
	// not sent. MotherDuck stores the configuration exactly as written, so it
	// applies its default and drops a cooldown set outside Terraform. Reading
	// the live cooldown instead would turn a default that GET fills in, or an
	// organization-level cooldown, into a stored user-level override.
	// MotherDuck rejects any cooldown for Pulse.
	if isPulseSize(cfg.ReadWrite.InstanceSize) {
		cfg.ReadWrite.CooldownSeconds = nil
	}
	if isPulseSize(cfg.ReadScaling.InstanceSize) {
		cfg.ReadScaling.CooldownSeconds = nil
	}
	updated, err := client.SetDucklingConfig(ctx, plan.Username.ValueString(), cfg)
	if err != nil {
		diags.AddError("Unable to set MotherDuck Duckling config", ducklingConfigErrorDetail(err))
		return
	}
	plan.ID = types.StringValue(plan.Username.ValueString())
	planned := plan
	setDucklingModelFromREST(&plan, updated)
	// A write response that omits a planned cooldown does not mean the write
	// was ignored. Keep the planned value so apply stays consistent and let the
	// next refresh report real drift.
	plan.ReadWriteCooldownSeconds = plannedCooldownOrLive(planned.ReadWriteCooldownSeconds, plan.ReadWriteCooldownSeconds)
	plan.ReadScalingCooldownSeconds = plannedCooldownOrLive(planned.ReadScalingCooldownSeconds, plan.ReadScalingCooldownSeconds)
	// The plan has no cooldown for Pulse, and the write sent none.
	if isPulseSize(cfg.ReadWrite.InstanceSize) {
		plan.ReadWriteCooldownSeconds = types.Int64Null()
	}
	if isPulseSize(cfg.ReadScaling.InstanceSize) {
		plan.ReadScalingCooldownSeconds = types.Int64Null()
	}
	diags.Append(stateSetter.Set(ctx, &plan)...)
}

// ducklingTierLimits are the default Duckling limits for each MotherDuck plan,
// keyed by the plan name the service puts in its tier error. They only make
// that error readable. The service enforces the limits, and MotherDuck can set
// different limits for an organization or a user.
var ducklingTierLimits = []struct{ tier, plan, limits string }{
	{"free", "Free", "read-write `pulse`, read-scaling `pulse`, and a flock size of at most 4"},
	{"lite", "Lite", "read-write `pulse`, read-scaling `pulse`, and a flock size of 0"},
	{"trial_not_started", "not yet started trial", "read-write up to `jumbo`, read-scaling up to `jumbo`, and a flock size of at most 4"},
	{"free_trial", "free trial", "read-write up to `mega`, read-scaling up to `jumbo`, and a flock size of at most 16"},
	{"business", "Business", "read-write up to `giga`, read-scaling up to `jumbo`, and a flock size of at most 16"},
}

var ducklingTierErrorPattern = regexp.MustCompile(`Invalid config for tier ([A-Za-z_]+)`)

// ducklingConfigErrorDetail explains a plan-limit rejection. MotherDuck
// reports it as a 400 whose message names the plan but renders the rejected
// configuration as "[object Object]".
func ducklingConfigErrorDetail(err error) string {
	var apiErr *mdrest.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return err.Error()
	}
	match := ducklingTierErrorPattern.FindStringSubmatch(apiErr.Error())
	if match == nil {
		return err.Error()
	}
	tier := strings.ToLower(match[1])
	detail := "MotherDuck rejected the configuration because it exceeds the Duckling limits for the organization's plan (`" + tier + "`)."
	known := false
	for _, limit := range ducklingTierLimits {
		if limit.tier == tier {
			detail += " By default the " + limit.plan + " plan allows " + limit.limits + "."
			known = true
		}
	}
	detail += " MotherDuck can set different limits for an organization or a user. Choose smaller instance sizes or a smaller flock size, or ask MotherDuck to raise the limits."
	if !known {
		detail += "\n\nDefault limits by plan:"
		for _, limit := range ducklingTierLimits {
			detail += "\n- " + limit.plan + ": " + limit.limits + "."
		}
	}
	return detail + "\n\nMotherDuck response: " + err.Error()
}

// plannedCooldownOrLive prefers a known planned cooldown over a write
// response that omits it, and otherwise uses the value MotherDuck reported.
func plannedCooldownOrLive(planned, live types.Int64) types.Int64 {
	if live.IsNull() && !planned.IsNull() && !planned.IsUnknown() {
		return planned
	}
	return live
}

// setDucklingModelFromREST copies the live configuration into the model,
// including cooldowns MotherDuck applied by default, so refresh and import
// always record what is live.
func setDucklingModelFromREST(model *ducklingConfigModel, cfg *mdrest.DucklingConfig) {
	model.ID = types.StringValue(model.Username.ValueString())
	model.ReadWriteInstanceSize = types.StringValue(cfg.ReadWrite.InstanceSize)
	model.ReadWriteCooldownSeconds = int64FromLive(cfg.ReadWrite.CooldownSeconds)
	model.ReadScalingInstanceSize = types.StringValue(cfg.ReadScaling.InstanceSize)
	model.ReadScalingFlockSize = types.Float64Value(cfg.ReadScaling.FlockSize)
	model.ReadScalingCooldownSeconds = int64FromLive(cfg.ReadScaling.CooldownSeconds)
}

func int64FromLive(live *int64) types.Int64 {
	if live == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*live)
}

// ducklingCooldownPlanModifier plans an unset cooldown. A configured value
// always wins. An unset cooldown on a Pulse size is null, because Pulse has
// none. Otherwise it keeps its state value as long as the matching instance
// size is unchanged, and the write sends that value so MotherDuck keeps it.
// When the instance size changes, or state has no value, the value is unknown
// because MotherDuck applies the default for the size.
type ducklingCooldownPlanModifier struct {
	sizeAttribute string
}

func (m ducklingCooldownPlanModifier) Description(context.Context) string {
	return "Keeps an unset cooldown at its current value while the instance size is unchanged."
}

func (m ducklingCooldownPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m ducklingCooldownPlanModifier) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if !req.ConfigValue.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planned types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root(m.sizeAttribute), &planned)...)
	if resp.Diagnostics.HasError() || planned.IsUnknown() || planned.IsNull() {
		return
	}
	// Pulse has no cooldown, and the write sends none for it. MotherDuck can
	// still report the cooldown stored for a larger size after a plan limit
	// lowered the size to Pulse, so the state value is not carried forward.
	if isPulseSize(planned.ValueString()) {
		resp.PlanValue = types.Int64Null()
		return
	}
	if req.State.Raw.IsNull() {
		return
	}
	var current types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root(m.sizeAttribute), &current)...)
	if resp.Diagnostics.HasError() || current.IsNull() {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(planned.ValueString()), strings.TrimSpace(current.ValueString())) {
		return
	}
	// A null cooldown on a non-Pulse size comes from state written before
	// cooldowns were recorded. The write omits it and MotherDuck fills in its
	// default, so the value stays unknown until apply.
	if req.StateValue.IsNull() {
		return
	}
	resp.PlanValue = req.StateValue
}

func isPulseSize(size string) bool {
	return strings.EqualFold(strings.TrimSpace(size), "pulse")
}

func validateDucklingCooldowns(model *ducklingConfigModel, diags *diag.Diagnostics) bool {
	ok := true
	if !model.ReadWriteInstanceSize.IsUnknown() &&
		strings.EqualFold(model.ReadWriteInstanceSize.ValueString(), "pulse") &&
		!model.ReadWriteCooldownSeconds.IsNull() &&
		!model.ReadWriteCooldownSeconds.IsUnknown() {
		diags.AddAttributeError(
			path.Root("read_write_cooldown_seconds"),
			"Invalid Duckling cooldown configuration",
			"MotherDuck Pulse instances do not support cooldown_seconds. Remove read_write_cooldown_seconds or choose a non-Pulse read_write_instance_size.",
		)
		ok = false
	}
	if !model.ReadScalingInstanceSize.IsUnknown() &&
		strings.EqualFold(model.ReadScalingInstanceSize.ValueString(), "pulse") &&
		!model.ReadScalingCooldownSeconds.IsNull() &&
		!model.ReadScalingCooldownSeconds.IsUnknown() {
		diags.AddAttributeError(
			path.Root("read_scaling_cooldown_seconds"),
			"Invalid Duckling cooldown configuration",
			"MotherDuck Pulse instances do not support cooldown_seconds. Remove read_scaling_cooldown_seconds or choose a non-Pulse read_scaling_instance_size.",
		)
		ok = false
	}
	return ok
}

func validateRESTUsernameImportID(id, usage string, diags *diag.Diagnostics) bool {
	trimmed := strings.TrimSpace(id)
	length := len([]rune(id))
	if trimmed == "" || length < 1 || length > 255 {
		diags.AddError("Invalid import ID", "Use "+usage+" with a MotherDuck username between 1 and 255 characters.")
		return false
	}
	if id != trimmed {
		diags.AddError("Invalid import ID", "MotherDuck username import segments must not include leading or trailing whitespace.")
		return false
	}
	if detail, ok := tfvalidators.ValidateRESTPathSegmentValue(id, "MotherDuck username import segment"); !ok {
		diags.AddError("Invalid import ID", detail)
		return false
	}
	return true
}

func validateRESTImportIDPart(part, usage, label string, diags *diag.Diagnostics) bool {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		diags.AddError("Invalid import ID", "Use "+usage+" with non-empty "+label+" segments.")
		return false
	}
	if part != trimmed {
		diags.AddError("Invalid import ID", "MotherDuck "+label+" import segments must not include leading or trailing whitespace.")
		return false
	}
	if detail, ok := tfvalidators.ValidateRESTPathSegmentValue(part, "MotherDuck "+label+" import segment"); !ok {
		diags.AddError("Invalid import ID", detail)
		return false
	}
	return true
}

// ModifyPlan warns when the plan needs the admin token and none is configured.
func (r *accessTokenResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.warnMissingAdminToken(req, resp)
}

// ModifyPlan warns when the plan needs the admin token and none is configured.
func (r *ducklingConfigResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.warnMissingAdminToken(req, resp)
}
