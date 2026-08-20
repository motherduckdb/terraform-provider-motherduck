package resources

import (
	"context"
	"strings"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &serviceAccountResource{}
	_ resource.ResourceWithConfigure      = &serviceAccountResource{}
	_ resource.ResourceWithImportState    = &serviceAccountResource{}
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
	ID       types.String `tfsdk:"id"`
	Username types.String `tfsdk:"username"`
}

func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

func (r *serviceAccountResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Creates a MotherDuck service account through the public REST API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Service account ID. This is the service account username returned by MotherDuck.",
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Unique service account username. Must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 255 characters.",
				Validators:          serviceAccountUsernameValidators(),
				PlanModifiers:       stringRequiresReplace(),
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
	created, err := client.CreateServiceAccount(ctx, plan.Username.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck service account", err.Error())
		return
	}
	plan.ID = types.StringValue(created.Username)
	plan.Username = types.StringValue(created.Username)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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
	// MotherDuck has no GET /v1/users/{username}, so probe existence through the
	// per-username Duckling config endpoint instead. The unrouted path returned 404
	// on every refresh, which isNotFound() could not distinguish from a missing
	// service account.
	if _, err := client.GetDucklingConfig(ctx, state.Username.ValueString()); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read MotherDuck service account", err.Error())
	}
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Service account updates are not supported", "Change the username by replacing the resource.")
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
	if err := client.DeleteUser(ctx, state.Username.ValueString()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck service account", err.Error())
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	detail, ok := validateServiceAccountUsernameValue(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid import ID", "Use `<username>`. "+detail)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

type accessTokenResource struct {
	baseResource
}

type accessTokenModel struct {
	ID        types.String `tfsdk:"id"`
	Username  types.String `tfsdk:"username"`
	Name      types.String `tfsdk:"name"`
	TTL       types.Int64  `tfsdk:"ttl"`
	TokenType types.String `tfsdk:"token_type"`
	Token     types.String `tfsdk:"token"`
	ExpireAt  types.String `tfsdk:"expire_at"`
	CreatedTS types.String `tfsdk:"created_ts"`
	ReadOnly  types.Bool   `tfsdk:"read_only"`
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
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User or service account username. Must be non-blank and 1-255 characters.",
				Validators:          restUsernameValidators(),
				PlanModifiers:       stringRequiresReplace(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Access token label. Must be non-blank and 1-255 characters.",
				Validators:          accessTokenNameValidators(),
				PlanModifiers:       stringRequiresReplace(),
			},
			"ttl": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional token lifetime in seconds. Must be between 300 and 31536000. Changing this value replaces the token.",
				Validators:          accessTokenTTLValidators(),
				PlanModifiers:       int64RequiresReplace(),
			},
			"token_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("read_write"),
				MarkdownDescription: "`read_write` or `read_scaling`. Defaults to `read_write`. Changing this value replaces the token.",
				Validators:          accessTokenTypeValidators(),
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
			},
			"token": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Token secret, returned only at creation time.",
			},
			"expire_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Token expiration timestamp reported by MotherDuck.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Token creation timestamp reported by MotherDuck.",
			},
			"read_only": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the token is read-only according to MotherDuck.",
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

	tokenType := "read_write"
	if !plan.TokenType.IsNull() && plan.TokenType.ValueString() != "" {
		tokenType = strings.ToLower(strings.TrimSpace(plan.TokenType.ValueString()))
	}
	createReq := mdrest.CreateTokenRequest{Name: plan.Name.ValueString(), TokenType: tokenType}
	if !plan.TTL.IsNull() {
		ttl := plan.TTL.ValueInt64()
		createReq.TTL = &ttl
	}
	created, err := client.CreateToken(ctx, plan.Username.ValueString(), createReq)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck access token", err.Error())
		return
	}
	setTokenModelFromREST(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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
		if token.ID == state.ID.ValueString() {
			secret := state.Token
			setTokenModelFromREST(&state, &token)
			state.Token = secret
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *accessTokenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Access token updates are not supported", "Change token arguments by replacing the resource.")
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func setTokenModelFromREST(model *accessTokenModel, token *mdrest.Token) {
	model.ID = types.StringValue(token.ID)
	model.TokenType = types.StringValue(token.TokenType)
	model.CreatedTS = types.StringValue(token.CreatedTS)
	model.ReadOnly = types.BoolValue(token.ReadOnly)
	if token.Name != "" {
		model.Name = types.StringValue(token.Name)
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
				MarkdownDescription: "Duckling configuration ID. This is the configured username.",
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User or service account username. Must be non-blank and 1-255 characters.",
				Validators:          restUsernameValidators(),
				PlanModifiers:       stringRequiresReplace(),
			},
			"read_write_instance_size": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Read-write Duckling instance size. Must be one of `pulse`, `standard`, `jumbo`, `mega`, or `giga`.",
				Validators:          ducklingInstanceSizeValidators(),
			},
			"read_write_cooldown_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional read-write cooldown in seconds. Must be between 60 and 86400. Pulse instances do not support cooldown seconds.",
				Validators:          ducklingCooldownValidators(),
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
				MarkdownDescription: "Optional read-scaling cooldown in seconds. Must be between 60 and 86400. Pulse instances do not support cooldown seconds.",
				Validators:          ducklingCooldownValidators(),
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
	if !plan.ReadWriteCooldownSeconds.IsNull() {
		v := plan.ReadWriteCooldownSeconds.ValueInt64()
		cfg.ReadWrite.CooldownSeconds = &v
	}
	if !plan.ReadScalingCooldownSeconds.IsNull() {
		v := plan.ReadScalingCooldownSeconds.ValueInt64()
		cfg.ReadScaling.CooldownSeconds = &v
	}
	updated, err := client.SetDucklingConfig(ctx, plan.Username.ValueString(), cfg)
	if err != nil {
		diags.AddError("Unable to set MotherDuck Duckling config", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Username.ValueString())
	setDucklingModelFromREST(&plan, updated)
	diags.Append(stateSetter.Set(ctx, &plan)...)
}

func setDucklingModelFromREST(model *ducklingConfigModel, cfg *mdrest.DucklingConfig) {
	model.ID = types.StringValue(model.Username.ValueString())
	model.ReadWriteInstanceSize = types.StringValue(cfg.ReadWrite.InstanceSize)
	if cfg.ReadWrite.CooldownSeconds != nil {
		model.ReadWriteCooldownSeconds = types.Int64Value(*cfg.ReadWrite.CooldownSeconds)
	} else {
		model.ReadWriteCooldownSeconds = types.Int64Null()
	}
	model.ReadScalingInstanceSize = types.StringValue(cfg.ReadScaling.InstanceSize)
	model.ReadScalingFlockSize = types.Float64Value(cfg.ReadScaling.FlockSize)
	if cfg.ReadScaling.CooldownSeconds != nil {
		model.ReadScalingCooldownSeconds = types.Int64Value(*cfg.ReadScaling.CooldownSeconds)
	} else {
		model.ReadScalingCooldownSeconds = types.Int64Null()
	}
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
	return true
}
