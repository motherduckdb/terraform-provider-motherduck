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
			"session_name": ephschema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional name used to reuse the same read-scaling session across embed requests. Must be non-blank when set. Conflicts with `session_hint`.",
				Validators:          diveembed.SessionNameValidators(),
			},
			"session_hint": ephschema.StringAttribute{
				Optional:            true,
				DeprecationMessage:  diveembed.SessionHintDeprecationMessage,
				MarkdownDescription: "Deprecated alias for `session_name`. Must be non-blank when set.",
				Validators:          diveembed.SessionHintValidators(),
			},
			"version": ephschema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive version to embed. Must be a positive integer. Omit it to embed the current version.",
				Validators:          diveembed.VersionValidators(),
			},
			"required_resources": ephschema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Optional override for the databases and shares the Dive renders against. When set, it replaces the Dive's declared required resources for this session. The JSON encoding of the list must be at most 8192 bytes.",
				Validators:          diveembed.RequiredResourcesValidators(),
				NestedObject: ephschema.NestedAttributeObject{
					Attributes: map[string]ephschema.Attribute{
						"url": ephschema.StringAttribute{
							Required:            true,
							Sensitive:           true,
							MarkdownDescription: "MotherDuck share URL the Dive renders against. This value is sensitive and can be sourced from `motherduck_share.url`. Must be non-blank.",
							Validators:          diveembed.RequiredResourceURLValidators(),
						},
						"alias": ephschema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional alias exposed to the Dive content for this resource.",
						},
					},
				},
			},
			"initial_state": ephschema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional JSON object that seeds the embedded Dive's UI state. Each key is read by the matching `useDiveState` call in the Dive. Build it with `jsonencode()`. The JSON encoding must be at most 64 KiB.",
				Validators:          diveembed.InitialStateValidators(),
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
