package resources

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestDecodeNullableJSON(t *testing.T) {
	tests := map[string]struct {
		raw       sql.NullString
		wantValue string
		wantError bool
	}{
		"null SQL value": {wantValue: "existing"},
		"empty string":   {raw: sql.NullString{Valid: true}, wantValue: "existing"},
		"JSON null":      {raw: sql.NullString{String: "null", Valid: true}, wantValue: "existing"},
		"JSON value":     {raw: sql.NullString{String: `{"name":"daily"}`, Valid: true}, wantValue: "daily"},
		"invalid JSON":   {raw: sql.NullString{String: "{", Valid: true}, wantValue: "existing", wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			target := struct {
				Name string `json:"name"`
			}{Name: "existing"}
			var diags diag.Diagnostics
			ok := decodeNullableJSON(tc.raw, &target, "Unable to decode test JSON", &diags)
			if gotError := !ok; gotError != tc.wantError {
				t.Fatalf("decodeNullableJSON() error = %t, want %t", gotError, tc.wantError)
			}
			if diags.HasError() != tc.wantError {
				t.Fatalf("diagnostics = %v, want error %t", diags, tc.wantError)
			}
			if target.Name != tc.wantValue {
				t.Fatalf("decoded name = %q, want %q", target.Name, tc.wantValue)
			}
			if tc.wantError && !strings.Contains(diags[0].Summary(), "Unable to decode test JSON") {
				t.Fatalf("diagnostic summary = %q", diags[0].Summary())
			}
		})
	}
}
