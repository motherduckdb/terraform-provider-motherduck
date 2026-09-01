//go:build contract

package ephemeral

import (
	"context"
	"errors"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/diveembed"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	tfephemeral "github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDiveEmbedSessionEphemeralContract(t *testing.T) {
	ctx := context.Background()
	client := &embedSessionREST{session: "md_embed_contract_session"}
	res := NewDiveEmbedSessionEphemeralResource().(*diveEmbedSessionEphemeralResource)
	var configureResp tfephemeral.ConfigureResponse
	res.Configure(ctx, tfephemeral.ConfigureRequest{ProviderData: &providerctx.Context{REST: client}}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", configureResp.Diagnostics)
	}

	var schemaResp tfephemeral.SchemaResponse
	res.Schema(ctx, tfephemeral.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	sessionAttr, ok := schemaResp.Schema.Attributes["session"].(ephschema.StringAttribute)
	if !ok || !sessionAttr.Sensitive || !sessionAttr.Computed {
		t.Fatalf("session schema = %#v, want sensitive computed string", schemaResp.Schema.Attributes["session"])
	}

	config := embedSessionConfig(schemaResp.Schema)
	resp := tfephemeral.OpenResponse{
		Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema},
	}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("open diagnostics: %v", resp.Diagnostics)
	}
	if client.diveID != "00000000-0000-0000-0000-000000000123" {
		t.Fatalf("dive id = %q", client.diveID)
	}
	if client.request.Username != "contract_reader" || client.request.SessionHint != "stable-reader" {
		t.Fatalf("embed request = %#v", client.request)
	}
	var result diveembed.Model
	if diags := resp.Result.Get(ctx, &result); diags.HasError() {
		t.Fatalf("result diagnostics: %v", diags)
	}
	if got := result.Session.ValueString(); got != "md_embed_contract_session" {
		t.Fatalf("session = %q, want contract session", got)
	}

	client.err = errors.New("embed backend unavailable")
	errorResp := tfephemeral.OpenResponse{
		Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema},
	}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &errorResp)
	if !errorResp.Diagnostics.HasError() || errorResp.Diagnostics[0].Summary() != "Unable to create MotherDuck Dive embed session" {
		t.Fatalf("error diagnostics = %v", errorResp.Diagnostics)
	}
}

func embedSessionConfig(schema ephschema.Schema) tfsdk.Config {
	attributeTypes := map[string]tftypes.Type{
		"dive_id":      tftypes.String,
		"username":     tftypes.String,
		"session_hint": tftypes.String,
		"session":      tftypes.String,
	}
	return tfsdk.Config{
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: attributeTypes}, map[string]tftypes.Value{
			"dive_id":      tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000123"),
			"username":     tftypes.NewValue(tftypes.String, "contract_reader"),
			"session_hint": tftypes.NewValue(tftypes.String, "stable-reader"),
			"session":      tftypes.NewValue(tftypes.String, nil),
		}),
		Schema: schema,
	}
}

type embedSessionREST struct {
	session string
	err     error
	diveID  string
	request mdrest.EmbedSessionRequest
}

func (*embedSessionREST) Available() bool { return true }

func (c *embedSessionREST) CreateDiveEmbedSession(_ context.Context, diveID string, request mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error) {
	c.diveID = diveID
	c.request = request
	if c.err != nil {
		return nil, c.err
	}
	return &mdrest.EmbedSessionResponse{Session: c.session}, nil
}

func (*embedSessionREST) ActiveAccounts(context.Context) (*mdrest.ActiveAccountsResponse, error) {
	return nil, errors.New("unexpected ActiveAccounts call")
}
func (*embedSessionREST) CreateServiceAccount(context.Context, string) (*mdrest.ServiceAccount, error) {
	return nil, errors.New("unexpected CreateServiceAccount call")
}
func (*embedSessionREST) CreateToken(context.Context, string, mdrest.CreateTokenRequest) (*mdrest.Token, error) {
	return nil, errors.New("unexpected CreateToken call")
}
func (*embedSessionREST) DeleteToken(context.Context, string, string) error {
	return errors.New("unexpected DeleteToken call")
}
func (*embedSessionREST) DeleteUser(context.Context, string) error {
	return errors.New("unexpected DeleteUser call")
}
func (*embedSessionREST) GetDucklingConfig(context.Context, string) (*mdrest.DucklingConfig, error) {
	return nil, errors.New("unexpected GetDucklingConfig call")
}
func (*embedSessionREST) ListTokens(context.Context, string) ([]mdrest.Token, error) {
	return nil, errors.New("unexpected ListTokens call")
}
func (*embedSessionREST) SetDucklingConfig(context.Context, string, mdrest.DucklingConfig) (*mdrest.DucklingConfig, error) {
	return nil, errors.New("unexpected SetDucklingConfig call")
}
