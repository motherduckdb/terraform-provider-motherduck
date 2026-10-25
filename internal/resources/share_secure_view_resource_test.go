package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestShareSecureViewStatement(t *testing.T) {
	tests := map[string]struct {
		replaces  bool
		orReplace bool
		want      string
	}{
		"derived add":      {false, false, "ALTER SHARE \"tenant share\" ADD SECURE VIEW \"app\".\"acme \"\"only\"\"\" AS (\nSELECT 1 -- note\n)"},
		"replacing add":    {true, false, "ALTER SHARE \"tenant share\" ADD SECURE VIEW REPLACES \"app\".\"acme \"\"only\"\"\" AS (\nSELECT 1 -- note\n)"},
		"replacing update": {true, true, "ALTER SHARE \"tenant share\" ADD OR REPLACE SECURE VIEW REPLACES \"app\".\"acme \"\"only\"\"\" AS (\nSELECT 1 -- note\n)"},
		"derived update":   {false, true, "ALTER SHARE \"tenant share\" ADD OR REPLACE SECURE VIEW \"app\".\"acme \"\"only\"\"\" AS (\nSELECT 1 -- note\n)"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := shareSecureViewStatement("tenant share", "app", `acme "only"`, tc.replaces, "SELECT 1 -- note", tc.orReplace)
			if got != tc.want {
				t.Fatalf("statement = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateSecureViewQuery(t *testing.T) {
	for name, tc := range map[string]struct {
		query   string
		wantErr string
	}{
		"select":            {"SELECT id FROM app.t WHERE tenant = current_user", ""},
		"multiline comment": {"SELECT id\nFROM app.t -- trailing comment\n", ""},
		"blank":             {"  \n ", "must not be empty"},
		"semicolon":         {"SELECT 1); DROP SHARE s; SELECT (1", "must not contain semicolons"},
		"trailing semi":     {"SELECT 1;", "must not contain semicolons"},
	} {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateSecureViewQuery(tc.query, &diags)
			if tc.wantErr == "" {
				if diags.HasError() {
					t.Fatalf("unexpected error: %v", diags)
				}
				return
			}
			if !diags.HasError() || !strings.Contains(diags[0].Detail(), tc.wantErr) {
				t.Fatalf("diagnostics = %v, want %q", diags, tc.wantErr)
			}
		})
	}
}

func TestParseShareSecureViewRow(t *testing.T) {
	view, err := parseShareSecureViewRow(map[string]any{
		"schema_name":    "app",
		"view_name":      "daily_usage",
		"sql":            "SELECT tenant_id FROM app.daily_usage",
		"replaces_table": true,
		"columns":        []any{"tenant_id", "usage_date"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Schema != "app" || view.Name != "daily_usage" || !view.Replaces || strings.Join(view.Columns, ",") != "tenant_id,usage_date" {
		t.Fatalf("unexpected view %#v", view)
	}

	for name, row := range map[string]map[string]any{
		"missing schema":  {"view_name": "v", "sql": "SELECT 1"},
		"missing sql":     {"schema_name": "main", "view_name": "v"},
		"string replaces": {"schema_name": "main", "view_name": "v", "sql": "SELECT 1", "replaces_table": "true"},
		"scalar columns":  {"schema_name": "main", "view_name": "v", "sql": "SELECT 1", "columns": "x"},
		"numeric column":  {"schema_name": "main", "view_name": "v", "sql": "SELECT 1", "columns": []any{1.0}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseShareSecureViewRow(row); err == nil {
				t.Fatal("expected an error for a malformed SHOW SECURE VIEWS row")
			}
		})
	}
}

func TestApplyShareSecureViewKeepsConfiguredWhitespace(t *testing.T) {
	ctx := context.Background()
	model := shareSecureViewModel{
		Share:  types.StringValue("usage_share"),
		Schema: types.StringValue("app"),
		Name:   types.StringValue("daily_usage"),
		Query:  types.StringValue("\n  SELECT tenant_id FROM app.daily_usage\n"),
	}
	view := shareSecureView{Schema: "app", Name: "daily_usage", SQL: "SELECT tenant_id FROM app.daily_usage", Replaces: true, Columns: []string{"tenant_id"}}
	if diags := applyShareSecureView(ctx, &model, view); diags.HasError() {
		t.Fatal(diags)
	}
	if model.Query.ValueString() != "\n  SELECT tenant_id FROM app.daily_usage\n" {
		t.Fatalf("configured query spelling was not kept: %q", model.Query.ValueString())
	}
	if model.ID.ValueString() != "usage_share.app.daily_usage" || !model.Replaces.ValueBool() || len(model.Columns.Elements()) != 1 {
		t.Fatalf("unexpected model %#v", model)
	}

	view.SQL = "SELECT tenant_id FROM app.daily_usage WHERE false"
	if diags := applyShareSecureView(ctx, &model, view); diags.HasError() {
		t.Fatal(diags)
	}
	if model.Query.ValueString() != view.SQL {
		t.Fatalf("drifted query was not read back: %q", model.Query.ValueString())
	}
}

func TestSecureViewErrorDetail(t *testing.T) {
	unsupported := secureViewErrorDetail(errString(`Parser Error: syntax error at or near "SECURE"`))
	if !strings.Contains(unsupported, "DuckDB 1.5.6") {
		t.Fatalf("missing support hint: %s", unsupported)
	}
	missing := secureViewErrorDetail(errString(`Catalog Error: Secure view "app.t" is declared with REPLACES, but no table or view of that name exists in "db"`))
	if !strings.Contains(missing, "replaces = true") {
		t.Fatalf("missing REPLACES hint: %s", missing)
	}
	if got := secureViewErrorDetail(errString("other")); got != "other" {
		t.Fatalf("unrelated error changed: %s", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
