package resources

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestServiceAccountUsernameMinimumLength(t *testing.T) {
	for value, wantOK := range map[string]bool{
		"a":                      false,
		"ab":                     false,
		"abc":                    true,
		strings.Repeat("a", 255): true,
		strings.Repeat("a", 256): false,
	} {
		if _, ok := validateServiceAccountUsernameValue(value); ok != wantOK {
			t.Errorf("validateServiceAccountUsernameValue(%q) = %t, want %t", value, ok, wantOK)
		}
	}
}

func TestAccessTokenNameRejectsReservedNames(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		value   string
		wantErr bool
	}{
		"extension": {value: "MotherDuck Extension", wantErr: true},
		"flights":   {value: "MotherDuck Flights", wantErr: true},
		// MotherDuck matches reserved names exactly, so these are accepted.
		"different case":   {value: "motherduck flights"},
		"trailing padding": {value: "MotherDuck Flights "},
		"ordinary":         {value: "ci-loader"},
	} {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			for _, v := range accessTokenNameValidators() {
				var resp validator.StringResponse
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root("name"), ConfigValue: types.StringValue(tc.value)}, &resp)
				diags.Append(resp.Diagnostics...)
			}
			if diags.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", diags, tc.wantErr)
			}
		})
	}
}

func TestAccessTokenDescriptionSchemaAndValidators(t *testing.T) {
	ctx := context.Background()
	var resp resource.SchemaResponse
	NewAccessTokenResource().Schema(ctx, resource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes["description"].(schema.StringAttribute)
	if !ok || !attr.Optional || !attr.Computed || len(attr.PlanModifiers) != 2 {
		t.Fatalf("description = %#v, want optional computed with state and replace plan modifiers", resp.Schema.Attributes["description"])
	}
	for name, tc := range map[string]struct {
		value   string
		wantErr bool
	}{
		"empty":    {value: "", wantErr: true},
		"blank":    {value: "   ", wantErr: true},
		"max":      {value: strings.Repeat("d", 1000)},
		"too long": {value: strings.Repeat("d", 1001), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			for _, v := range attr.Validators {
				var vresp validator.StringResponse
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root("description"), ConfigValue: types.StringValue(tc.value)}, &vresp)
				diags.Append(vresp.Diagnostics...)
			}
			if diags.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", diags, tc.wantErr)
			}
		})
	}
}

func TestAccessTokenDescriptionCreate(t *testing.T) {
	const username = "svc_reader"
	ctx := context.Background()
	tests := map[string]struct {
		planned         types.String
		createResponse  string
		wantBody        string
		wantState       types.String
		wantCreateError bool
	}{
		"description sent and stored": {
			planned:        types.StringValue("CI loader"),
			createResponse: `{"token":"secret","id":"tok_1","name":"ci","description":"CI loader","created_ts":"2026-01-01T00:00:00Z","read_only":false,"token_type":"read_write"}`,
			wantBody:       `"description":"CI loader"`,
			wantState:      types.StringValue("CI loader"),
		},
		"omitted description stays null": {
			planned:        types.StringUnknown(),
			createResponse: `{"token":"secret","id":"tok_1","name":"ci","created_ts":"2026-01-01T00:00:00Z","read_only":false,"token_type":"read_write"}`,
			wantState:      types.StringNull(),
		},
		"dropped description errors after saving state": {
			planned:         types.StringValue("CI loader"),
			createResponse:  `{"token":"secret","id":"tok_1","name":"ci","created_ts":"2026-01-01T00:00:00Z","read_only":false,"token_type":"read_write"}`,
			wantBody:        `"description":"CI loader"`,
			wantState:       types.StringNull(),
			wantCreateError: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/users/"+username+"/tokens" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				_, _ = w.Write([]byte(tc.createResponse))
			}))
			defer server.Close()
			res := accessTokenResourceFor(t, server.URL)
			var schemaResp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := plan.Set(ctx, accessTokenModel{
				ID:          types.StringUnknown(),
				Username:    types.StringValue(username),
				Name:        types.StringValue("ci"),
				Description: tc.planned,
				TTL:         types.Int64Null(),
				TokenType:   types.StringValue("read_write"),
				Token:       types.StringUnknown(),
				ExpireAt:    types.StringUnknown(),
				CreatedTS:   types.StringUnknown(),
				ReadOnly:    types.BoolUnknown(),
			}); diags.HasError() {
				t.Fatal(diags)
			}
			createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
			res.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
			if createResp.Diagnostics.HasError() != tc.wantCreateError {
				t.Fatalf("create diagnostics = %v, want error %t", createResp.Diagnostics, tc.wantCreateError)
			}
			if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
				t.Fatalf("request body = %s, want %s", body, tc.wantBody)
			}
			if tc.wantBody == "" && strings.Contains(body, "description") {
				t.Fatalf("request body = %s, want no description", body)
			}
			var got accessTokenModel
			if diags := createResp.State.Get(ctx, &got); diags.HasError() {
				t.Fatalf("state was not saved: %v", diags)
			}
			if !got.Description.Equal(tc.wantState) || got.ID.ValueString() != "tok_1" {
				t.Fatalf("state description = %v id = %v, want %v and tok_1", got.Description, got.ID, tc.wantState)
			}
		})
	}
}

func TestAccessTokenReadAdoptsReportedDescription(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		listed string
		prior  types.String
		want   types.String
	}{
		"description set outside Terraform": {listed: `,"description":"set in the UI"`, prior: types.StringNull(), want: types.StringValue("set in the UI")},
		"token from before descriptions":    {listed: `,"description":null`, prior: types.StringNull(), want: types.StringNull()},
		"description stays in sync":         {listed: `,"description":"CI loader"`, prior: types.StringValue("CI loader"), want: types.StringValue("CI loader")},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"tokens":[{"id":"tok_1","name":"ci","token_type":"read_write","created_ts":"2026-01-01T00:00:00Z"` + tc.listed + `}]}`))
			}))
			defer server.Close()
			res := accessTokenResourceFor(t, server.URL)
			var schemaResp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			prior := tfsdk.State{Schema: schemaResp.Schema}
			if diags := prior.Set(ctx, accessTokenModel{
				ID:          types.StringValue("tok_1"),
				Username:    types.StringValue("svc_reader"),
				Name:        types.StringValue("ci"),
				Description: tc.prior,
				TTL:         types.Int64Null(),
				TokenType:   types.StringValue("read_write"),
				Token:       types.StringValue("secret"),
				ExpireAt:    types.StringNull(),
				CreatedTS:   types.StringValue("2026-01-01T00:00:00Z"),
				ReadOnly:    types.BoolValue(false),
			}); diags.HasError() {
				t.Fatal(diags)
			}
			readResp := resource.ReadResponse{State: prior}
			res.Read(ctx, resource.ReadRequest{State: prior}, &readResp)
			if readResp.Diagnostics.HasError() {
				t.Fatal(readResp.Diagnostics)
			}
			var got accessTokenModel
			if diags := readResp.State.Get(ctx, &got); diags.HasError() {
				t.Fatal(diags)
			}
			if !got.Description.Equal(tc.want) {
				t.Fatalf("description = %v, want %v", got.Description, tc.want)
			}
		})
	}
}

func accessTokenResourceFor(t *testing.T, baseURL string) *accessTokenResource {
	t.Helper()
	client, err := mdrest.New(baseURL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	return &accessTokenResource{baseResource: baseResource{provider: &providerctx.Context{REST: client}}}
}
