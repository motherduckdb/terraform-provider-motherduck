package ephemeral

import (
	"context"
	"fmt"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tokencontext"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfephemeral "github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ tfephemeral.EphemeralResource              = &shortLivedTokenEphemeralResource{}
	_ tfephemeral.EphemeralResourceWithConfigure = &shortLivedTokenEphemeralResource{}
)

const shortLivedTokenTypeReadScaling = "read_scaling"

type shortLivedTokenEphemeralResource struct {
	provider *providerctx.Context
}

type shortLivedTokenModel struct {
	TokenType types.String `tfsdk:"token_type"`
	Context   types.Map    `tfsdk:"context"`
	Token     types.String `tfsdk:"token"`
	ExpireAt  types.String `tfsdk:"expire_at"`
}

func NewShortLivedTokenEphemeralResource() tfephemeral.EphemeralResource {
	return &shortLivedTokenEphemeralResource{}
}

func (r *shortLivedTokenEphemeralResource) Metadata(ctx context.Context, req tfephemeral.MetadataRequest, resp *tfephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_short_lived_token"
}

func (r *shortLivedTokenEphemeralResource) Configure(ctx context.Context, req tfephemeral.ConfigureRequest, resp *tfephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	providerData, ok := req.ProviderData.(*providerctx.Context)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerctx.Context, got %T", req.ProviderData))
		return
	}
	r.provider = providerData
}

func (r *shortLivedTokenEphemeralResource) Schema(ctx context.Context, req tfephemeral.SchemaRequest, resp *tfephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		MarkdownDescription: "Mints a read scaling short-lived token without writing it to Terraform state. MotherDuck mints the token for the identity behind the provider's `admin_token` and cannot mint one for another user. The token expires after a day. When `admin_token` is an access token, the minted token expires no later than that access token, and deleting the access token revokes it. A read scaling token cannot mint another token.",
		Attributes: map[string]ephschema.Attribute{
			"token_type": ephschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Type of the token. `read_scaling` is the only value MotherDuck accepts.",
				Validators:          []validator.String{tokenTypeValidator{}},
			},
			"context": ephschema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional key/value context that the token carries. Secure view policies read a value with `session_context('key')`, which returns NULL for a key the context does not set. A token's value takes precedence over a value the client sets for the same key on its connection. " + tokencontext.Description + " The key `name` sets the session name and routes the token's requests to a read scaling replica. Its value must be 1 to 64 characters of letters, digits, `.`, `_` and `-`, starting with a letter or digit.",
				Validators:          tokencontext.Validators(),
			},
			"token": ephschema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Read scaling short-lived token returned by MotherDuck. Terraform keeps this value ephemeral and does not persist it in state.",
			},
			"expire_at": ephschema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Time the token expires, as MotherDuck reports it.",
			},
		},
	}
}

type tokenTypeValidator struct{}

func (tokenTypeValidator) Description(context.Context) string {
	return fmt.Sprintf("must be %q", shortLivedTokenTypeReadScaling)
}

func (v tokenTypeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (tokenTypeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || req.ConfigValue.ValueString() == shortLivedTokenTypeReadScaling {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck short-lived token type", fmt.Sprintf("token_type must be %q, got %q.", shortLivedTokenTypeReadScaling, req.ConfigValue.ValueString()))
}

func (r *shortLivedTokenEphemeralResource) Open(ctx context.Context, req tfephemeral.OpenRequest, resp *tfephemeral.OpenResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var config shortLivedTokenModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Values unknown during planning skip the plan-time validators.
	if config.TokenType.ValueString() != shortLivedTokenTypeReadScaling {
		resp.Diagnostics.AddError("Invalid MotherDuck short-lived token type", fmt.Sprintf("token_type must be %q, got %q.", shortLivedTokenTypeReadScaling, config.TokenType.ValueString()))
		return
	}
	values, err := tokencontext.FromMap(config.Context)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MotherDuck token context", err.Error()+".")
		return
	}
	token, err := client.CreateShortLivedToken(ctx, mdrest.ShortLivedTokenRequest{TokenType: shortLivedTokenTypeReadScaling, Context: values})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck short-lived token", err.Error())
		return
	}
	config.Token = types.StringValue(token.Token)
	config.ExpireAt = types.StringValue(token.ExpireAt)
	resp.Diagnostics.Append(resp.Result.Set(ctx, &config)...)
}

func (r *shortLivedTokenEphemeralResource) rest(diags *diag.Diagnostics) *mdrest.Client {
	client, err := r.provider.AvailableRESTClient()
	if err != nil {
		diags.AddError("MotherDuck admin token required", err.Error())
		return nil
	}
	return client
}
