package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRoleNameValidator(t *testing.T) {
	v := roleNameValidator{}
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "ascii", value: "analytics_readers"},
		{name: "unicode", value: "équipe_2"},
		{name: "hyphenated", value: "analytics-readers"},
		{name: "minimum length", value: "abc"},
		{name: "too short", value: "ab", wantErr: true},
		{name: "starts with digit", value: "2readers", wantErr: true},
		{name: "starts with hyphen", value: "-readers", wantErr: true},
		{name: "punctuation", value: "analytics.readers", wantErr: true},
		{name: "uppercase", value: "Analytics_Readers", wantErr: true},
		{name: "reserved admin", value: "admin", wantErr: true},
		{name: "reserved builder", value: "builder", wantErr: true},
		{name: "reserved explorer", value: "explorer", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("name"),
				ConfigValue: types.StringValue(tc.value),
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestRoleGrantSchemaDefaultsToUser(t *testing.T) {
	var resp resource.SchemaResponse
	NewRoleGrantResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	granteeType, ok := resp.Schema.Attributes["grantee_type"].(schema.StringAttribute)
	if !ok || !granteeType.Optional || !granteeType.Computed || granteeType.Default == nil {
		t.Fatalf("grantee_type schema = %#v", resp.Schema.Attributes["grantee_type"])
	}
}

func TestRoleGrantImportRejectsInvalidGranteeType(t *testing.T) {
	var resp resource.ImportStateResponse
	NewRoleGrantResource().(resource.ResourceWithImportState).ImportState(
		t.Context(),
		resource.ImportStateRequest{ID: "analytics_readers/group/reporting_readers"},
		&resp,
	)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected invalid grantee type diagnostics")
	}
}

func TestFindRoleRow(t *testing.T) {
	rows := []map[string]any{
		{"role_name": "admin", "role_type": "SYSTEM", "included_roles": nil, "created_at": nil},
		{"role_name": "Analytics_Readers", "role_type": "CUSTOM", "included_roles": []any{"builder"}, "created_at": "2026-01-01T00:00:00Z"},
	}

	row, ok := findRoleRow(rows, "analytics_readers")
	if !ok {
		t.Fatal("expected case-insensitive role match")
	}
	if row["role_type"] != "CUSTOM" {
		t.Fatalf("row = %#v, want the analytics_readers row", row)
	}

	if _, ok := findRoleRow(rows, "missing_role"); ok {
		t.Fatal("expected no match for a missing role")
	}
	if _, ok := findRoleRow(nil, "analytics_readers"); ok {
		t.Fatal("expected no match for empty rows")
	}
}

func TestFindDirectRoleGrant(t *testing.T) {
	rows := []map[string]any{
		{"role_name": "transitive_role", "is_direct": false, "granted_at": nil},
		{"role_name": "string_bool_role", "is_direct": "true", "granted_at": "2026-02-02T00:00:00Z"},
		{"role_name": "Direct_Role", "is_direct": true, "granted_at": "2026-01-01T00:00:00Z"},
		{"role_name": "null_granted_role", "is_direct": true, "granted_at": nil},
	}

	grantedAt, ok := findDirectRoleGrant(rows, "direct_role")
	if !ok || grantedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Fatalf("findDirectRoleGrant(direct_role) = %v, %v", grantedAt, ok)
	}

	grantedAt, ok = findDirectRoleGrant(rows, "string_bool_role")
	if !ok || grantedAt.ValueString() != "2026-02-02T00:00:00Z" {
		t.Fatalf("findDirectRoleGrant(string_bool_role) = %v, %v", grantedAt, ok)
	}

	grantedAt, ok = findDirectRoleGrant(rows, "null_granted_role")
	if !ok || !grantedAt.IsNull() {
		t.Fatalf("findDirectRoleGrant(null_granted_role) = %v, %v, want null granted_at", grantedAt, ok)
	}

	if _, ok := findDirectRoleGrant(rows, "transitive_role"); ok {
		t.Fatal("transitive membership must not match a direct grant")
	}
	if _, ok := findDirectRoleGrant(rows, "missing_role"); ok {
		t.Fatal("missing role must not match")
	}
}

func TestShowRolesToStatementQuotesGrantee(t *testing.T) {
	if got, want := showRolesToStatement("user", `first.last@example.com`), `SHOW ROLES TO USER "first.last@example.com"`; got != want {
		t.Fatalf("showRolesToStatement() = %q, want %q", got, want)
	}
	if got, want := showRolesToStatement("role", `weird"role`), `SHOW ROLES TO ROLE "weird""role"`; got != want {
		t.Fatalf("showRolesToStatement() = %q, want %q", got, want)
	}
}

func TestRoleGrantStatementsQuoteDottedUserAsOneIdentifier(t *testing.T) {
	const username = "first.last@example.com"
	if got, want := roleGrantStatement("GRANT", "analytics_readers", "TO", "user", username), `GRANT ROLE "analytics_readers" TO USER "first.last@example.com"`; got != want {
		t.Fatalf("roleGrantStatement() = %q, want %q", got, want)
	}
	if got, want := roleGrantStatement("REVOKE", "analytics_readers", "FROM", "user", username), `REVOKE ROLE "analytics_readers" FROM USER "first.last@example.com"`; got != want {
		t.Fatalf("roleGrantStatement() = %q, want %q", got, want)
	}
}
