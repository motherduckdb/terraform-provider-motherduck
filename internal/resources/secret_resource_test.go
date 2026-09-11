package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

func TestValidateSecretConfig(t *testing.T) {
	ctx := context.Background()
	tests := map[string]struct {
		params    map[string]string
		secretSQL types.String
		wantErr   bool
	}{
		"valid params": {
			params:    map[string]string{"key_id": "abc", "region": "us-east-1"},
			secretSQL: types.StringValue("URL_STYLE 'path'"),
			wantErr:   false,
		},
		"bad param key": {
			params:    map[string]string{"key-id": "abc"},
			secretSQL: types.StringNull(),
			wantErr:   true,
		},
		"raw semicolon": {
			params:    map[string]string{},
			secretSQL: types.StringValue("URL_STYLE 'path'; DROP SECRET other"),
			wantErr:   true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			params, valueDiags := types.MapValueFrom(ctx, types.StringType, tc.params)
			if valueDiags.HasError() {
				t.Fatalf("building params map: %v", valueDiags)
			}
			model := secretModel{Params: params, SecretSQL: tc.secretSQL}
			var diags diag.Diagnostics
			validateSecretConfig(model, &diags)
			if gotErr := diags.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, diags)
			}
		})
	}
}

func TestValidateSecretValuesSharedRules(t *testing.T) {
	tests := map[string]struct {
		values    secretValidationValues
		wantPaths []string
	}{
		"valid": {values: secretValidationValues{Type: "s3", Provider: "config", ParamKeys: []string{"key_id", "secret"}, RawSQL: "REGION 'us-east-1'"}},
		"type with injection": {
			values:    secretValidationValues{Type: "s3); DROP DATABASE prod; --"},
			wantPaths: []string{"type"},
		},
		"uppercase type": {
			values:    secretValidationValues{Type: "S3"},
			wantPaths: []string{"type"},
		},
		"provider with space": {
			values:    secretValidationValues{Provider: "config extra"},
			wantPaths: []string{"secret_provider"},
		},
		"empty provider skipped": {values: secretValidationValues{Provider: ""}},
		"param key injection": {
			values:    secretValidationValues{ParamKeys: []string{"key_id", "x) ; DROP"}},
			wantPaths: []string{`params["x) ; DROP"]`},
		},
		"secret_sql semicolon": {
			values:    secretValidationValues{RawSQL: "REGION 'a'; DROP DATABASE prod"},
			wantPaths: []string{"secret_sql"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateSecretValues(tc.values, &diags)
			var gotPaths []string
			for _, d := range diags.Errors() {
				withPath, ok := d.(diag.DiagnosticWithPath)
				if !ok {
					t.Fatalf("diagnostic without path: %v", d)
				}
				gotPaths = append(gotPaths, withPath.Path().String())
			}
			if !reflect.DeepEqual(gotPaths, tc.wantPaths) {
				t.Fatalf("diagnostic paths = %#v, want %#v", gotPaths, tc.wantPaths)
			}
		})
	}
}

func TestDesiredSecretScopeFromParams(t *testing.T) {
	ctx := context.Background()
	params, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"SCOPE": "s3://bucket/path/with'quote/",
	})
	if valueDiags.HasError() {
		t.Fatalf("building params map: %v", valueDiags)
	}
	got, ok := desiredSecretScopeFromParams(params)
	if !ok {
		t.Fatal("expected scope to be detected")
	}
	want := "['s3://bucket/path/with''quote/']"
	if got != want {
		t.Fatalf("desired scope = %q, want %q", got, want)
	}

	withoutScope, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"region": "us-east-1",
	})
	if valueDiags.HasError() {
		t.Fatalf("building params map: %v", valueDiags)
	}
	if got, ok := desiredSecretScopeFromParams(withoutScope); ok || got != "" {
		t.Fatalf("expected no desired scope, got %q", got)
	}
}

func TestSecretApplyRejectsUnsafeValues(t *testing.T) {
	ctx := context.Background()
	params, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"key_id": "abc"})
	if valueDiags.HasError() {
		t.Fatalf("building params map: %v", valueDiags)
	}
	tests := map[string]secretModel{
		"type injection":       {Name: types.StringValue("s"), Type: types.StringValue("s3); DROP DATABASE prod; --"), Params: params},
		"provider injection":   {Name: types.StringValue("s"), Type: types.StringValue("s3"), SecretProvider: types.StringValue("config) --"), Params: params},
		"secret_sql semicolon": {Name: types.StringValue("s"), Type: types.StringValue("s3"), Params: params, SecretSQL: types.StringValue("REGION 'a'; DROP DATABASE prod")},
	}
	for name, plan := range tests {
		t.Run(name, func(t *testing.T) {
			client := &recordingSQLClient{}
			r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			var diags diag.Diagnostics
			r.createSecret(ctx, modelGetter{model: plan}, discardSetter{}, false, &diags)
			if !diags.HasError() {
				t.Fatal("expected apply-time validation error")
			}
			if len(client.execs) != 0 {
				t.Fatalf("expected no SQL to run, got %q", client.execs)
			}
		})
	}
}
