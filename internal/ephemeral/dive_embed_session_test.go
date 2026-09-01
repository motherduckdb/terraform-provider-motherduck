//go:build contract

package ephemeral

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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
	backend := &embedSessionREST{}
	server := httptest.NewServer(http.HandlerFunc(backend.serveHTTP))
	t.Cleanup(server.Close)
	client, err := mdrest.New(server.URL, "contract-admin-token")
	if err != nil {
		t.Fatal(err)
	}
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
	diveID, request := backend.requestSnapshot()
	if diveID != "00000000-0000-0000-0000-000000000123" {
		t.Fatalf("dive id = %q", diveID)
	}
	if request.Username != "contract_reader" || request.SessionHint != "stable-reader" {
		t.Fatalf("embed request = %#v", request)
	}
	var result diveembed.Model
	if diags := resp.Result.Get(ctx, &result); diags.HasError() {
		t.Fatalf("result diagnostics: %v", diags)
	}
	if got := result.Session.ValueString(); got != "md_embed_contract_session" {
		t.Fatalf("session = %q, want contract session", got)
	}

	backend.setFailure(true)
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
	mu      sync.Mutex
	fail    bool
	diveID  string
	request mdrest.EmbedSessionRequest
}

func (b *embedSessionREST) serveHTTP(w http.ResponseWriter, req *http.Request) {
	const path = "/v1/dives/00000000-0000-0000-0000-000000000123/embed-session"
	if req.Method != http.MethodPost || req.URL.Path != path {
		http.Error(w, "unexpected embed session request", http.StatusInternalServerError)
		return
	}
	if req.Header.Get("Authorization") != "Bearer contract-admin-token" {
		http.Error(w, "unexpected authorization", http.StatusUnauthorized)
		return
	}
	var request mdrest.EmbedSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	b.diveID = "00000000-0000-0000-0000-000000000123"
	b.request = request
	fail := b.fail
	b.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "UNAVAILABLE", "message": "embed backend unavailable"})
		return
	}
	_ = json.NewEncoder(w).Encode(mdrest.EmbedSessionResponse{Session: "md_embed_contract_session"})
}

func (b *embedSessionREST) requestSnapshot() (string, mdrest.EmbedSessionRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.diveID, b.request
}

func (b *embedSessionREST) setFailure(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = fail
}
