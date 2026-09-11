package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestShareGrantErrorDetail(t *testing.T) {
	detail := shareGrantErrorDetail(errors.New("Catalog Error: Unable to find user reader_user"))
	if !strings.Contains(detail, "grantable MotherDuck user or service-account principal") {
		t.Fatalf("expected grantable-principal guidance, got %q", detail)
	}
	if !strings.Contains(detail, "Unable to find user reader_user") {
		t.Fatalf("expected original error to be preserved, got %q", detail)
	}

	other := shareGrantErrorDetail(errors.New("Catalog Error: something else"))
	if other != "Catalog Error: something else" {
		t.Fatalf("unexpected detail rewrite: %q", other)
	}
}

func TestShareGrantReadDecision(t *testing.T) {
	shareDropped := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Database share example_share not found"}
	otherMissing := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table Function with name md_list_share_grantees does not exist!"}
	other := errors.New("network unreachable")
	tests := map[string]struct {
		exists     bool
		err        error
		wantRemove bool
		wantErr    error
	}{
		"grant exists":                {exists: true, wantRemove: false},
		"grant revoked out of band":   {exists: false, wantRemove: true},
		"share dropped out of band":   {exists: false, err: shareDropped, wantRemove: true},
		"other object missing":        {exists: false, err: otherMissing, wantRemove: false, wantErr: otherMissing},
		"other errors surface as-is":  {exists: false, err: other, wantRemove: false, wantErr: other},
		"error wins over stale exist": {exists: true, err: other, wantRemove: false, wantErr: other},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			remove, err := shareGrantReadDecision("example_share", tc.exists, tc.err)
			if remove != tc.wantRemove {
				t.Fatalf("remove = %t, want %t", remove, tc.wantRemove)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestShareGrantImportRejectsEmptySegments(t *testing.T) {
	tests := map[string]string{
		"empty share":         "/svc_reader",
		"empty username":      "analytics_share/",
		"leading username":    "analytics_share/ svc_reader",
		"missing slash":       "analytics_share",
		"trailing username":   "analytics_share/svc_reader ",
		"whitespace username": "analytics_share/ ",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewShareGrantResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics")
			}
		})
	}
}

func TestShareGrantImportAllowsEmailPrincipal(t *testing.T) {
	var diags diag.Diagnostics
	parts, ok := splitImportID("analytics_share/first.last+reader@example.com", "/", 2, "`<share>/<username>`", &diags)
	if !ok {
		t.Fatalf("unexpected split diagnostics for email-like principal: %v", diags)
	}
	if !validateSQLImportIDPart(parts[0], "`<share>/<username>`", &diags) {
		t.Fatalf("unexpected share diagnostics for email-like principal: %v", diags)
	}
	if !validateShareGrantPrincipalImportID(parts[1], "`<share>/<username>`", &diags) {
		t.Fatalf("unexpected username diagnostics for email-like principal: %v", diags)
	}
}

func TestShareGrantPrincipalValidator(t *testing.T) {
	ctx := context.Background()
	v := shareGrantPrincipalValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"service account":      {value: types.StringValue("svc_reader"), wantErr: false},
		"email":                {value: types.StringValue("user@example.com"), wantErr: false},
		"dotted plus email":    {value: types.StringValue("first.last+reader@example.com"), wantErr: false},
		"hyphenated principal": {value: types.StringValue("reader-tenant"), wantErr: false},
		"blank":                {value: types.StringValue("  "), wantErr: true},
		"leading whitespace":   {value: types.StringValue(" reader"), wantErr: true},
		"trailing whitespace":  {value: types.StringValue("reader "), wantErr: true},
		"unknown":              {value: types.StringUnknown(), wantErr: false},
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

func TestShareGrantStatementsQuoteDottedUserAsOneIdentifier(t *testing.T) {
	const username = "first.last@example.com"
	if got, want := shareGrantStatement("GRANT", "analytics", "TO", "user", username), `GRANT READ ON SHARE "analytics" TO USER "first.last@example.com"`; got != want {
		t.Fatalf("shareGrantStatement() = %q, want %q", got, want)
	}
	if got, want := shareGrantStatement("REVOKE", "analytics", "FROM", "user", username), `REVOKE READ ON SHARE "analytics" FROM USER "first.last@example.com"`; got != want {
		t.Fatalf("shareGrantStatement() = %q, want %q", got, want)
	}
}
