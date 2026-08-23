package resources

import (
	"context"
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

func TestAccessTokenTokenTypeSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewAccessTokenResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["token_type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("token_type attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["token_type"])
	}
	if !attr.Optional || !attr.Computed {
		t.Fatalf("token_type Optional=%v Computed=%v, want both true", attr.Optional, attr.Computed)
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("token_type should keep replacement plan modifiers")
	}
}

func TestAccessTokenImportRejectsEmptySegments(t *testing.T) {
	var resp resource.ImportStateResponse
	NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
		ID: "svc_reader/",
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import diagnostics for empty token ID segment")
	}
}

func TestAccessTokenImportRejectsInvalidUsername(t *testing.T) {
	var resp resource.ImportStateResponse
	NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
		ID: strings.Repeat("a", 256) + "/token-id",
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import diagnostics for oversized username segment")
	}
}

func TestAccessTokenImportRejectsWhitespace(t *testing.T) {
	// #nosec G101 -- import IDs are non-secret test fixtures.
	tests := map[string]string{
		"leading username":  " svc_reader/token-id",
		"leading token id":  "svc_reader/ token-id",
		"trailing token id": "svc_reader/token-id ",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for whitespace in access token import ID")
			}
		})
	}
}

func TestServiceAccountImportRejectsInvalidUsername(t *testing.T) {
	tests := map[string]string{
		"blank":         " ",
		"leading space": " svc_reader",
		"hyphen":        "svc-reader",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewServiceAccountResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for invalid service account username")
			}
		})
	}
}

// TestServiceAccountReadProbesRealEndpoint pins the two halves of the refresh
// bug: the endpoint the read probes, and what it does with a 404 it cannot
// attribute to a missing account.
//
// A read aimed at GET /v1/users/{username}, which MotherDuck does not route,
// used to return a plain-text 404 that read as "account deleted", so every
// refresh emptied state and the next apply tried to recreate a live account.
func TestServiceAccountReadProbesRealEndpoint(t *testing.T) {
	const username = "svc_reader"
	tests := map[string]struct {
		status      int
		body        string
		wantRemoved bool
		wantErr     bool
	}{
		"live account keeps state": {
			status: http.StatusOK,
			body:   `{"read_write":{"instance_size":"standard"},"read_scaling":{"instance_size":"standard","flock_size":4}}`,
		},
		"deleted account drops state": {
			status:      http.StatusNotFound,
			body:        `{"message":"entity not found","code":"NOT_FOUND"}`,
			wantRemoved: true,
		},
		// An unrouted path must raise an error, never quietly empty state.
		"unrouted 404 errors": {
			status:  http.StatusNotFound,
			body:    "Not Found",
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := mdrest.New(server.URL, "admin-token")
			if err != nil {
				t.Fatal(err)
			}
			res := NewServiceAccountResource().(*serviceAccountResource)
			var configureResp resource.ConfigureResponse
			res.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerctx.Context{REST: client}}, &configureResp)
			if configureResp.Diagnostics.HasError() {
				t.Fatalf("configure diagnostics: %v", configureResp.Diagnostics)
			}

			var schemaResp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
			}
			priorState := tfsdk.State{Schema: schemaResp.Schema}
			if diags := priorState.Set(ctx, serviceAccountModel{
				ID:       types.StringValue(username),
				Username: types.StringValue(username),
			}); diags.HasError() {
				t.Fatalf("seeding prior state: %v", diags)
			}

			readResp := resource.ReadResponse{State: priorState}
			res.Read(ctx, resource.ReadRequest{State: priorState}, &readResp)

			if want := "/v1/users/" + username + "/instances"; gotPath != want {
				t.Fatalf("probed path = %q, want %q", gotPath, want)
			}
			if gotErr := readResp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("read error = %t, want %t: %v", gotErr, tc.wantErr, readResp.Diagnostics)
			}
			if gotRemoved := readResp.State.Raw.IsNull(); gotRemoved != tc.wantRemoved {
				t.Fatalf("state removed = %t, want %t", gotRemoved, tc.wantRemoved)
			}
		})
	}
}

func TestDucklingConfigImportRejectsInvalidUsername(t *testing.T) {
	tests := map[string]string{
		"blank":         " ",
		"leading space": " svc_reader",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewDucklingConfigResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for invalid username")
			}
		})
	}
}

func TestServiceAccountUsernameValidator(t *testing.T) {
	ctx := context.Background()
	v := serviceAccountUsernameValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"simple":       {value: types.StringValue("svc_reader_1"), wantErr: false},
		"unicode":      {value: types.StringValue("équipe_1"), wantErr: true},
		"starts digit": {value: types.StringValue("1_reader"), wantErr: true},
		"hyphen":       {value: types.StringValue("svc-reader"), wantErr: true},
		"space":        {value: types.StringValue("svc reader"), wantErr: true},
		"blank":        {value: types.StringValue(" "), wantErr: true},
		"unknown":      {value: types.StringUnknown(), wantErr: false},
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

func TestRESTStringEnumValidators(t *testing.T) {
	ctx := context.Background()
	v := stringEnumValidator{name: "MotherDuck access token type", values: []string{"read_write", "read_scaling"}}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"read write": {value: types.StringValue("read_write"), wantErr: false},
		"case":       {value: types.StringValue("READ_SCALING"), wantErr: true},
		"invalid":    {value: types.StringValue("read_only"), wantErr: true},
		"empty":      {value: types.StringValue(""), wantErr: true},
		"unknown":    {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("token_type"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRESTRangeValidators(t *testing.T) {
	ctx := context.Background()

	intValidator := accessTokenTTLValidators()[0]
	for name, tc := range map[string]struct {
		value   types.Int64
		wantErr bool
	}{
		"min":     {value: types.Int64Value(300), wantErr: false},
		"max":     {value: types.Int64Value(31536000), wantErr: false},
		"low":     {value: types.Int64Value(299), wantErr: true},
		"high":    {value: types.Int64Value(31536001), wantErr: true},
		"unknown": {value: types.Int64Unknown(), wantErr: false},
	} {
		t.Run("int64 "+name, func(t *testing.T) {
			var resp validator.Int64Response
			intValidator.ValidateInt64(ctx, validator.Int64Request{
				Path:        path.Root("ttl"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}

	floatValidator := float64RangeValidator{name: "MotherDuck read-scaling flock size", min: 0, max: 64}
	for name, tc := range map[string]struct {
		value   types.Float64
		wantErr bool
	}{
		"zero":    {value: types.Float64Value(0), wantErr: false},
		"max":     {value: types.Float64Value(64), wantErr: false},
		"low":     {value: types.Float64Value(-0.1), wantErr: true},
		"high":    {value: types.Float64Value(64.1), wantErr: true},
		"unknown": {value: types.Float64Unknown(), wantErr: false},
	} {
		t.Run("float64 "+name, func(t *testing.T) {
			var resp validator.Float64Response
			floatValidator.ValidateFloat64(ctx, validator.Float64Request{
				Path:        path.Root("read_scaling_flock_size"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestValidateDucklingCooldownsDefersUnknownValues(t *testing.T) {
	model := ducklingConfigModel{
		ReadWriteInstanceSize:      types.StringValue("pulse"),
		ReadWriteCooldownSeconds:   types.Int64Unknown(),
		ReadScalingInstanceSize:    types.StringUnknown(),
		ReadScalingCooldownSeconds: types.Int64Value(300),
	}
	var diags diag.Diagnostics
	if !validateDucklingCooldowns(&model, &diags) {
		t.Fatalf("unknown cross-field values should defer validation: %v", diags)
	}
}
