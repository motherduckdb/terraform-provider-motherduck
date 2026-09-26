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

	"github.com/hashicorp/terraform-plugin-framework/diag"
	tfephemeral "github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	if request.Username != "contract_reader" || request.SessionName != "stable-reader" {
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
	return embedSessionConfigWith(schema, map[string]tftypes.Value{
		"session_name": tftypes.NewValue(tftypes.String, "stable-reader"),
	})
}

var embedResourceType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"url":   tftypes.String,
	"alias": tftypes.String,
}}

// embedSessionConfigWith builds a config with every optional argument null
// except the overrides.
func embedSessionConfigWith(schema ephschema.Schema, overrides map[string]tftypes.Value) tfsdk.Config {
	attributeTypes := map[string]tftypes.Type{
		"dive_id":            tftypes.String,
		"username":           tftypes.String,
		"session_name":       tftypes.String,
		"session_hint":       tftypes.String,
		"version":            tftypes.Number,
		"required_resources": tftypes.List{ElementType: embedResourceType},
		"initial_state":      tftypes.String,
		"session":            tftypes.String,
	}
	values := map[string]tftypes.Value{
		"dive_id":  tftypes.NewValue(tftypes.String, "00000000-0000-0000-0000-000000000123"),
		"username": tftypes.NewValue(tftypes.String, "contract_reader"),
	}
	for name, attributeType := range attributeTypes {
		if _, ok := values[name]; !ok {
			values[name] = tftypes.NewValue(attributeType, nil)
		}
	}
	for name, value := range overrides {
		values[name] = value
	}
	return tfsdk.Config{
		Raw:    tftypes.NewValue(tftypes.Object{AttributeTypes: attributeTypes}, values),
		Schema: schema,
	}
}

func TestDiveEmbedSessionEphemeralSendsAllArguments(t *testing.T) {
	ctx := context.Background()
	backend := &embedSessionREST{}
	server := httptest.NewServer(http.HandlerFunc(backend.serveHTTP))
	t.Cleanup(server.Close)
	client, err := mdrest.New(server.URL, "contract-admin-token")
	if err != nil {
		t.Fatal(err)
	}
	res := &diveEmbedSessionEphemeralResource{provider: &providerctx.Context{REST: client}}
	var schemaResp tfephemeral.SchemaResponse
	res.Schema(ctx, tfephemeral.SchemaRequest{}, &schemaResp)

	config := embedSessionConfigWith(schemaResp.Schema, map[string]tftypes.Value{
		"session_hint": tftypes.NewValue(tftypes.String, "legacy-reader"),
		"version":      tftypes.NewValue(tftypes.Number, 7),
		"required_resources": tftypes.NewValue(tftypes.List{ElementType: embedResourceType}, []tftypes.Value{
			tftypes.NewValue(embedResourceType, map[string]tftypes.Value{
				"url":   tftypes.NewValue(tftypes.String, "md:_share/sales/abc"),
				"alias": tftypes.NewValue(tftypes.String, "sales"),
			}),
		}),
		"initial_state": tftypes.NewValue(tftypes.String, `{"region": "emea"}`),
	})
	resp := tfephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema}}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("open diagnostics: %v", resp.Diagnostics)
	}
	_, request := backend.requestSnapshot()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"username":"contract_reader","session_name":"legacy-reader","version":7,"required_resources":[{"url":"md:_share/sales/abc","alias":"sales"}],"initial_state":{"region":"emea"}}`
	if string(body) != want {
		t.Fatalf("embed request = %s\nwant %s", body, want)
	}
}

func TestDiveEmbedSessionEphemeralSchemaDeprecatesSessionHint(t *testing.T) {
	var schemaResp tfephemeral.SchemaResponse
	NewDiveEmbedSessionEphemeralResource().Schema(context.Background(), tfephemeral.SchemaRequest{}, &schemaResp)
	hint, ok := schemaResp.Schema.Attributes["session_hint"].(ephschema.StringAttribute)
	if !ok || hint.DeprecationMessage == "" {
		t.Fatalf("session_hint = %#v, want a deprecated attribute", schemaResp.Schema.Attributes["session_hint"])
	}
	resources, ok := schemaResp.Schema.Attributes["required_resources"].(ephschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("required_resources = %#v, want a list nested attribute", schemaResp.Schema.Attributes["required_resources"])
	}
	if url, ok := resources.NestedObject.Attributes["url"].(ephschema.StringAttribute); !ok || !url.Required || !url.Sensitive {
		t.Fatalf("required_resources.url = %#v, want required sensitive string", resources.NestedObject.Attributes["url"])
	}
}

func TestDiveEmbedSessionEphemeralSessionNameConflictsWithHint(t *testing.T) {
	ctx := context.Background()
	var schemaResp tfephemeral.SchemaResponse
	NewDiveEmbedSessionEphemeralResource().Schema(ctx, tfephemeral.SchemaRequest{}, &schemaResp)

	for name, tc := range map[string]struct {
		hint    tftypes.Value
		wantErr bool
	}{
		"both set":  {hint: tftypes.NewValue(tftypes.String, "legacy"), wantErr: true},
		"name only": {hint: tftypes.NewValue(tftypes.String, nil)},
		// An unknown hint may resolve to null, so it is not a conflict yet.
		"unknown hint": {hint: tftypes.NewValue(tftypes.String, tftypes.UnknownValue)},
	} {
		t.Run(name, func(t *testing.T) {
			config := embedSessionConfigWith(schemaResp.Schema, map[string]tftypes.Value{
				"session_name": tftypes.NewValue(tftypes.String, "current"),
				"session_hint": tc.hint,
			})
			var sessionName types.String
			if diags := config.GetAttribute(ctx, path.Root("session_name"), &sessionName); diags.HasError() {
				t.Fatal(diags)
			}
			var diags diag.Diagnostics
			for _, v := range diveembed.SessionNameValidators() {
				var resp validator.StringResponse
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root("session_name"), ConfigValue: sessionName, Config: config}, &resp)
				diags.Append(resp.Diagnostics...)
			}
			if diags.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", diags, tc.wantErr)
			}
		})
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
