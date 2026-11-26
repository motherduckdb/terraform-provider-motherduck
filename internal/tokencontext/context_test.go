package tokencontext

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestValidateMatchesMotherDuckRules pins the rules of the MotherDuck token
// context API, so a context it rejects fails at plan time.
func TestValidateMatchesMotherDuckRules(t *testing.T) {
	manyKeys := map[string]string{}
	for i := 0; i <= MaxKeys; i++ {
		manyKeys[fmt.Sprintf("k%d", i)] = "v"
	}
	maxKeys := map[string]string{}
	for i := 0; i < MaxKeys; i++ {
		maxKeys[fmt.Sprintf("k%d", i)] = "v"
	}
	for name, tc := range map[string]struct {
		values  map[string]string
		wantErr string
	}{
		"empty":               {values: map[string]string{}},
		"tenant":              {values: map[string]string{"tenant": "acme", "region.eu": "1", "A_B": ""}},
		"max keys":            {values: maxKeys},
		"too many keys":       {values: manyKeys, wantErr: "at most 64 keys, got 65"},
		"space in key":        {values: map[string]string{"ten ant": "acme"}, wantErr: `context key "ten ant" may only contain`},
		"dash in key":         {values: map[string]string{"tenant-id": "acme"}, wantErr: "may only contain"},
		"non-ASCII key":       {values: map[string]string{"tenänt": "acme"}, wantErr: "may only contain"},
		"empty key":           {values: map[string]string{"": "acme"}, wantErr: "may only contain"},
		"proto":               {values: map[string]string{"__PROTO__": "x"}, wantErr: "__proto__ cannot be set"},
		"case-only duplicate": {values: map[string]string{"Tenant": "a", "tenant": "b"}, wantErr: `"Tenant" and "tenant" differ only in letter case`},
		"NUL value":           {values: map[string]string{"tenant": "a\x00b"}, wantErr: "contains a NUL character"},
		"invalid UTF-8":       {values: map[string]string{"tenant": "\xff"}, wantErr: "not valid UTF-8"},
		"session name":        {values: map[string]string{"name": "viewer-42.eu_1"}},
		"upper session name":  {values: map[string]string{"NAME": "viewer"}},
		"name leading dash":   {values: map[string]string{"Name": "-viewer"}, wantErr: `context key "Name" must be 1 to 64 characters`},
		"name too long":       {values: map[string]string{"name": "a" + strings.Repeat("b", 64)}, wantErr: "must be 1 to 64 characters"},
		"name empty":          {values: map[string]string{"name": ""}, wantErr: "must be 1 to 64 characters"},
		"at byte limit":       {values: map[string]string{"k": strings.Repeat("v", MaxBytes-1)}},
		"over byte limit":     {values: map[string]string{"k": strings.Repeat("v", MaxBytes)}, wantErr: "at most 4096 bytes, got 4097"},
		"multibyte bytes":     {values: map[string]string{"k": strings.Repeat("é", MaxBytes/2)}, wantErr: "got 4097"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Validate(tc.values)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestMapValidatorHandlesUnknownValues(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		elements map[string]attr.Value
		wantErr  bool
	}{
		// An unknown value can still break a key rule, but not the size limit.
		"unknown value":          {elements: map[string]attr.Value{"tenant": types.StringUnknown(), "k": types.StringValue(strings.Repeat("v", MaxBytes))}},
		"unknown value, bad key": {elements: map[string]attr.Value{"ten ant": types.StringUnknown()}, wantErr: true},
		"unknown name":           {elements: map[string]attr.Value{"name": types.StringUnknown()}},
		"null value":             {elements: map[string]attr.Value{"tenant": types.StringNull()}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			value := types.MapValueMust(types.StringType, tc.elements)
			var diags []string
			for _, v := range Validators() {
				var resp validator.MapResponse
				v.ValidateMap(ctx, validator.MapRequest{Path: path.Root("context"), ConfigValue: value}, &resp)
				for _, d := range resp.Diagnostics.Errors() {
					diags = append(diags, d.Detail())
				}
			}
			if (len(diags) > 0) != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", diags, tc.wantErr)
			}
		})
	}
	if _, err := FromMap(types.MapValueMust(types.StringType, map[string]attr.Value{"tenant": types.StringNull()})); err == nil {
		t.Fatal("FromMap accepted a null value")
	}
	if got, err := FromMap(types.MapNull(types.StringType)); err != nil || got != nil {
		t.Fatalf("FromMap(null) = %v, %v, want nil, nil", got, err)
	}
}

func TestHasSessionNameIgnoresCase(t *testing.T) {
	if !HasSessionName(types.MapValueMust(types.StringType, map[string]attr.Value{"Name": types.StringValue("x")})) {
		t.Fatal("HasSessionName missed the key Name")
	}
	if HasSessionName(types.MapValueMust(types.StringType, map[string]attr.Value{"names": types.StringValue("x")})) {
		t.Fatal("HasSessionName matched the key names")
	}
	if HasSessionName(types.MapUnknown(types.StringType)) {
		t.Fatal("HasSessionName matched an unknown map")
	}
}
