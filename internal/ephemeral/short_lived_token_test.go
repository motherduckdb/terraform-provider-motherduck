package ephemeral

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	tfephemeral "github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var shortLivedTokenType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"token_type": tftypes.String,
	"context":    tftypes.Map{ElementType: tftypes.String},
	"token":      tftypes.String,
	"expire_at":  tftypes.String,
}}

func shortLivedTokenConfig(schema ephschema.Schema, tokenType tftypes.Value, contextValue tftypes.Value) tfsdk.Config {
	return tfsdk.Config{
		Raw: tftypes.NewValue(shortLivedTokenType, map[string]tftypes.Value{
			"token_type": tokenType,
			"context":    contextValue,
			"token":      tftypes.NewValue(tftypes.String, nil),
			"expire_at":  tftypes.NewValue(tftypes.String, nil),
		}),
		Schema: schema,
	}
}

func TestShortLivedTokenEphemeralContract(t *testing.T) {
	ctx := context.Background()
	backend := &shortLivedTokenREST{}
	server := httptest.NewServer(http.HandlerFunc(backend.serveHTTP))
	t.Cleanup(server.Close)
	client, err := mdrest.New(server.URL, "contract-admin-token")
	if err != nil {
		t.Fatal(err)
	}
	res := NewShortLivedTokenEphemeralResource().(*shortLivedTokenEphemeralResource)
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
	if token, ok := schemaResp.Schema.Attributes["token"].(ephschema.StringAttribute); !ok || !token.Sensitive || !token.Computed {
		t.Fatalf("token schema = %#v, want sensitive computed string", schemaResp.Schema.Attributes["token"])
	}

	contextValue := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
		"Tenant": tftypes.NewValue(tftypes.String, "acme"),
		"name":   tftypes.NewValue(tftypes.String, "tenant-acme"),
	})
	config := shortLivedTokenConfig(schemaResp.Schema, tftypes.NewValue(tftypes.String, "read_scaling"), contextValue)
	resp := tfephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema}}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("open diagnostics: %v", resp.Diagnostics)
	}
	if want := `{"token_type":"read_scaling","context":{"Tenant":"acme","name":"tenant-acme"}}`; backend.body() != want {
		t.Fatalf("request body = %s\nwant %s", backend.body(), want)
	}
	var result shortLivedTokenModel
	if diags := resp.Result.Get(ctx, &result); diags.HasError() {
		t.Fatalf("result diagnostics: %v", diags)
	}
	if result.Token.ValueString() != "md_slt_contract" || result.ExpireAt.ValueString() != "2026-11-26T12:00:00.000Z" {
		t.Fatalf("result = %#v", result)
	}

	// Without a context the body carries only the token type.
	config = shortLivedTokenConfig(schemaResp.Schema, tftypes.NewValue(tftypes.String, "read_scaling"), tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil))
	resp = tfephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema}}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("open diagnostics: %v", resp.Diagnostics)
	}
	if want := `{"token_type":"read_scaling"}`; backend.body() != want {
		t.Fatalf("request body = %s\nwant %s", backend.body(), want)
	}

	backend.setFailure(true)
	errorResp := tfephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema}}
	res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &errorResp)
	if !errorResp.Diagnostics.HasError() || errorResp.Diagnostics[0].Summary() != "Unable to create MotherDuck short-lived token" {
		t.Fatalf("error diagnostics = %v", errorResp.Diagnostics)
	}
}

// TestShortLivedTokenEphemeralRejectsBadConfigAtApply covers values that were
// unknown during planning, so no plan-time validator ran.
func TestShortLivedTokenEphemeralRejectsBadConfigAtApply(t *testing.T) {
	ctx := context.Background()
	res := &shortLivedTokenEphemeralResource{provider: &providerctx.Context{REST: unreachableREST(t)}}
	var schemaResp tfephemeral.SchemaResponse
	res.Schema(ctx, tfephemeral.SchemaRequest{}, &schemaResp)
	mapType := tftypes.Map{ElementType: tftypes.String}
	for name, tc := range map[string]struct {
		tokenType, context tftypes.Value
		summary            string
	}{
		"read_write": {
			tokenType: tftypes.NewValue(tftypes.String, "read_write"),
			context:   tftypes.NewValue(mapType, nil),
			summary:   "Invalid MotherDuck short-lived token type",
		},
		"bad key": {
			tokenType: tftypes.NewValue(tftypes.String, "read_scaling"),
			context:   tftypes.NewValue(mapType, map[string]tftypes.Value{"ten ant": tftypes.NewValue(tftypes.String, "acme")}),
			summary:   "Invalid MotherDuck token context",
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := shortLivedTokenConfig(schemaResp.Schema, tc.tokenType, tc.context)
			resp := tfephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Raw: config.Raw, Schema: schemaResp.Schema}}
			res.Open(ctx, tfephemeral.OpenRequest{Config: config}, &resp)
			if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != tc.summary {
				t.Fatalf("diagnostics = %v, want %q", resp.Diagnostics, tc.summary)
			}
		})
	}

	var typeResp validator.StringResponse
	tokenTypeValidator{}.ValidateString(ctx, validator.StringRequest{Path: path.Root("token_type"), ConfigValue: types.StringValue("read_write")}, &typeResp)
	if !typeResp.Diagnostics.HasError() {
		t.Fatal("token_type read_write passed plan-time validation")
	}
}

func unreachableREST(t *testing.T) *mdrest.Client {
	t.Helper()
	httpClient := &http.Client{Transport: admissionRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("a rejected configuration reached MotherDuck")
		return nil, io.EOF
	})}
	client, err := mdrest.New("https://127.0.0.1", "admin-token", mdrest.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type shortLivedTokenREST struct {
	mu       sync.Mutex
	fail     bool
	lastBody string
}

func (b *shortLivedTokenREST) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.URL.Path != "/v1/tokens/short-lived" {
		http.Error(w, "unexpected short-lived token request", http.StatusInternalServerError)
		return
	}
	if req.Header.Get("Authorization") != "Bearer contract-admin-token" {
		http.Error(w, "unexpected authorization", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	b.lastBody = string(body)
	fail := b.fail
	b.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "FORBIDDEN", "message": "read scaling tokens cannot mint tokens"})
		return
	}
	_, _ = io.WriteString(w, `{"token":"md_slt_contract","expire_at":"2026-11-26T12:00:00.000Z"}`)
}

func (b *shortLivedTokenREST) body() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastBody
}

func (b *shortLivedTokenREST) setFailure(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = fail
}
