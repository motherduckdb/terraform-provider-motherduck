package datasources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestRESTDataSourceStringLengthValidator(t *testing.T) {
	ctx := context.Background()
	v := tfvalidators.StringLength("test value", 1, 3)

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"min":      {value: types.StringValue("a"), wantErr: false},
		"max":      {value: types.StringValue("abc"), wantErr: false},
		"empty":    {value: types.StringValue(""), wantErr: true},
		"blank":    {value: types.StringValue("  "), wantErr: true},
		"too long": {value: types.StringValue("abcd"), wantErr: true},
		"unicode":  {value: types.StringValue("åß"), wantErr: false},
		"unknown":  {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("username"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRESTDataSourceUUIDValidator(t *testing.T) {
	ctx := context.Background()
	v := tfvalidators.UUID()

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"uuid v4":             {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000"), wantErr: false},
		"uuid v7":             {value: types.StringValue("01890f7e-7c6b-7cc2-98c4-dc0c0c07398f"), wantErr: false},
		"leading whitespace":  {value: types.StringValue(" 123e4567-e89b-42d3-a456-426614174000"), wantErr: true},
		"trailing whitespace": {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000 "), wantErr: true},
		"bad":                 {value: types.StringValue("not-a-uuid"), wantErr: true},
		"short":               {value: types.StringValue("123e4567"), wantErr: true},
		"unknown":             {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("dive_id"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRESTInventoryJSONDataSourcesAreSensitive(t *testing.T) {
	tests := map[string]struct {
		dataSource datasource.DataSource
		attr       string
	}{
		"active_accounts": {dataSource: NewActiveAccountsDataSource(), attr: "accounts_json"},
		"user_tokens":     {dataSource: NewUserTokensDataSource(), attr: "tokens_json"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp datasource.SchemaResponse
			tc.dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
			}
			attr, ok := resp.Schema.Attributes[tc.attr].(schema.StringAttribute)
			if !ok {
				t.Fatalf("%s attribute = %T, want schema.StringAttribute", tc.attr, resp.Schema.Attributes[tc.attr])
			}
			if !attr.Sensitive {
				t.Fatalf("%s should be sensitive", tc.attr)
			}
		})
	}
}

func TestRESTDataSourceSchemasHaveAttributeDescriptions(t *testing.T) {
	for _, ds := range []datasource.DataSource{
		NewActiveAccountsDataSource(),
		NewUserTokensDataSource(),
		NewDiveEmbedSessionDataSource(),
	} {
		var resp datasource.SchemaResponse
		ds.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
		}
		assertDataSourceAttributeDescriptions(t, resp.Schema.Attributes)
	}
}

func TestUserTokensDataSourceExposesTypedSensitiveTokens(t *testing.T) {
	var resp datasource.SchemaResponse
	NewUserTokensDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["tokens"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("tokens attribute = %T, want schema.ListNestedAttribute", resp.Schema.Attributes["tokens"])
	}
	if !attr.Sensitive {
		t.Fatal("tokens should be sensitive")
	}
}

func TestActiveAccountsDataSourceReadUsesRESTClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/active_accounts"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(mdrest.ActiveAccountsResponse{
			Accounts: []mdrest.ActiveAccount{{Username: "svc"}},
		})
	}))
	defer server.Close()

	client, err := mdrest.New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	ds := NewActiveAccountsDataSource().(*activeAccountsDataSource)
	var configureResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerctx.Context{REST: client}}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", configureResp.Diagnostics)
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	readResp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}

	var state activeAccountsModel
	readResp.Diagnostics.Append(readResp.State.Get(context.Background(), &state)...)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("state diagnostics: %v", readResp.Diagnostics)
	}
	if got, want := state.AccountsJSON.ValueString(), `[{"username":"svc","ducklings":null}]`; got != want {
		t.Fatalf("accounts_json = %q, want %q", got, want)
	}
}

func TestUserTokensDataSourceReadSetsTypedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/users/svc/tokens"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(mdrest.ListTokensResponse{
			Tokens: []mdrest.Token{{
				Token:     "raw-secret-that-must-not-reach-state",
				ID:        "tok_1",
				Name:      "ci",
				CreatedTS: "2026-01-01T00:00:00Z",
				ReadOnly:  true,
				TokenType: "read_scaling",
			}},
		})
	}))
	defer server.Close()

	client, err := mdrest.New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	ds := NewUserTokensDataSource().(*userTokensDataSource)
	var configureResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerctx.Context{REST: client}}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configure diagnostics: %v", configureResp.Diagnostics)
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	tokenObjectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id":         tftypes.String,
		"name":       tftypes.String,
		"expire_at":  tftypes.String,
		"created_ts": tftypes.String,
		"read_only":  tftypes.Bool,
		"token_type": tftypes.String,
	}}
	tokensType := tftypes.List{ElementType: tokenObjectType}
	configType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"username":    tftypes.String,
		"tokens_json": tftypes.String,
		"tokens":      tokensType,
	}}
	config := tftypes.NewValue(configType, map[string]tftypes.Value{
		"username":    tftypes.NewValue(tftypes.String, "svc"),
		"tokens_json": tftypes.NewValue(tftypes.String, nil),
		"tokens":      tftypes.NewValue(tokensType, nil),
	})
	readResp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	ds.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: config}}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}

	var state userTokensModel
	readResp.Diagnostics.Append(readResp.State.Get(context.Background(), &state)...)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("state diagnostics: %v", readResp.Diagnostics)
	}
	if state.Tokens.IsNull() || len(state.Tokens.Elements()) != 1 {
		t.Fatalf("tokens = %#v, want one typed token", state.Tokens)
	}
	if got, want := state.TokensJSON.ValueString(), `[{"id":"tok_1","name":"ci","created_ts":"2026-01-01T00:00:00Z","read_only":true,"token_type":"read_scaling"}]`; got != want {
		t.Fatalf("tokens_json = %q, want %q", got, want)
	}
	if strings.Contains(state.TokensJSON.ValueString(), "raw-secret") {
		t.Fatal("tokens_json persisted a raw token returned by the list endpoint")
	}
}

func TestDiveEmbedSessionDataSourceIsDeprecated(t *testing.T) {
	var resp datasource.SchemaResponse
	NewDiveEmbedSessionDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if resp.Schema.DeprecationMessage == "" {
		t.Fatal("dive embed session data source should point users to the ephemeral resource")
	}
}
