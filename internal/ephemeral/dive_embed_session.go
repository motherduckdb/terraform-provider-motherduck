package ephemeral

import (
	"context"
	"fmt"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/diveembed"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfephemeral "github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
)

var (
	_ tfephemeral.EphemeralResource              = &diveEmbedSessionEphemeralResource{}
	_ tfephemeral.EphemeralResourceWithConfigure = &diveEmbedSessionEphemeralResource{}
)

type diveEmbedSessionEphemeralResource struct {
	provider *providerctx.Context
}

func All() []func() tfephemeral.EphemeralResource {
	return []func() tfephemeral.EphemeralResource{
		NewDiveEmbedSessionEphemeralResource,
	}
}

func NewDiveEmbedSessionEphemeralResource() tfephemeral.EphemeralResource {
	return &diveEmbedSessionEphemeralResource{}
}

func (r *diveEmbedSessionEphemeralResource) Metadata(ctx context.Context, req tfephemeral.MetadataRequest, resp *tfephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dive_embed_session"
}

func (r *diveEmbedSessionEphemeralResource) Configure(ctx context.Context, req tfephemeral.ConfigureRequest, resp *tfephemeral.ConfigureResponse) {
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

func (r *diveEmbedSessionEphemeralResource) Schema(ctx context.Context, req tfephemeral.SchemaRequest, resp *tfephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		MarkdownDescription: "Creates a short-lived MotherDuck Dive embed session without writing the session credential to Terraform state.",
		Attributes: map[string]ephschema.Attribute{
			"dive_id": ephschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Dive ID. Must be a UUID with no leading or trailing whitespace.",
				Validators:          diveembed.DiveIDValidators(),
			},
			"username": ephschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Service account username. Must be non-blank and 1-255 characters.",
				Validators:          diveembed.UsernameValidators(),
			},
			"session_hint": ephschema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional hint used to reuse the same read-scaling session across embed requests. Must be non-blank when set.",
				Validators:          diveembed.SessionHintValidators(),
			},
			"session": ephschema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Short-lived embed session credential returned by MotherDuck. Terraform keeps this value ephemeral and does not persist it in state.",
			},
		},
	}
}

func (r *diveEmbedSessionEphemeralResource) Open(ctx context.Context, req tfephemeral.OpenRequest, resp *tfephemeral.OpenResponse) {
	client := r.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var config diveembed.Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := diveembed.Create(ctx, client, &config); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck Dive embed session", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.Result.Set(ctx, &config)...)
}

func (r *diveEmbedSessionEphemeralResource) rest(diags *diag.Diagnostics) *mdrest.Client {
	if r.provider == nil || r.provider.REST == nil || !r.provider.REST.Available() {
		diags.AddError("MotherDuck admin token required", mdrest.ErrMissingAdminToken.Error())
		return nil
	}
	return r.provider.REST
}
