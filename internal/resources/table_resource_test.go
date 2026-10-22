package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

func TestColumnDDLSortsAndQuotes(t *testing.T) {
	got := columnDDL(map[string]string{"b": "VARCHAR", `a.name`: "INTEGER", `a"name`: "BOOLEAN"})
	want := `"a""name" BOOLEAN, "a.name" INTEGER, "b" VARCHAR`
	if got != want {
		t.Fatalf("columnDDL() = %q, want %q", got, want)
	}
}

func TestValidateTableColumns(t *testing.T) {
	ctx := context.Background()
	tests := map[string]struct {
		columns map[string]string
		wantErr bool
	}{
		"valid":               {columns: map[string]string{"id": "INTEGER", "amount": "DECIMAL(18,2)"}, wantErr: false},
		"quoted comment text": {columns: map[string]string{"status": "ENUM('a--b', 'c')"}, wantErr: false},
		"empty":               {columns: map[string]string{}, wantErr: true},
		"blank name":          {columns: map[string]string{" ": "INTEGER"}, wantErr: true},
		"blank type":          {columns: map[string]string{"id": " "}, wantErr: true},
		"semicolon":           {columns: map[string]string{"id": "INTEGER; DROP TABLE other"}, wantErr: true},
		"comment escape":      {columns: map[string]string{"id": "INTEGER)) FROM read_csv('https://example.test') --"}, wantErr: true},
		"unbalanced":          {columns: map[string]string{"id": "DECIMAL(18,2"}, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			value, valueDiags := types.MapValueFrom(ctx, types.StringType, tc.columns)
			if valueDiags.HasError() {
				t.Fatalf("building map value: %v", valueDiags)
			}
			var diags diag.Diagnostics
			validateTableColumns(ctx, value, &diags)
			if gotErr := diags.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, diags)
			}
		})
	}
}

type fakeScalarStringer struct {
	values map[string]string
}

func TestCanonicalTableColumnsUsesParsedServerTypes(t *testing.T) {
	ctx := context.Background()
	client := fakeScalarStringer{values: map[string]string{
		"SELECT typeof(CAST(NULL AS INT))":           "INTEGER",
		"SELECT typeof(CAST(NULL AS DECIMAL(18,2)))": "DECIMAL(18,2)",
	}}
	var diags diag.Diagnostics
	got := canonicalTableColumns(ctx, client, map[string]string{
		"id":     "INT",
		"amount": "DECIMAL(18,2)",
	}, &diags)
	if diags.HasError() {
		t.Fatalf("canonicalTableColumns diagnostics: %v", diags)
	}
	want := map[string]string{"id": "INTEGER", "amount": "DECIMAL(18,2)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonicalTableColumns() = %#v, want %#v", got, want)
	}
}

func TestCanonicalColumnTypeRejectsQueryEscapeBeforeExecution(t *testing.T) {
	client := fakeScalarStringer{values: map[string]string{}}
	_, err := canonicalColumnType(context.Background(), client, "INTEGER)) FROM read_csv('https://example.test') WHERE ((1=1")
	if err == nil || !strings.Contains(err.Error(), "balanced") {
		t.Fatalf("canonicalColumnType() error = %v, want balanced delimiter error", err)
	}
}

func (f fakeScalarStringer) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	if value, ok := f.values[query]; ok {
		return value, nil
	}
	return "", fmt.Errorf("unexpected scalar query %q", query)
}

func TestTableColumnsSemanticallyEqualKeepsConfiguredAliases(t *testing.T) {
	ctx := context.Background()
	configured, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"id":      "INT",
		"created": "TIMESTAMPTZ",
	})
	if valueDiags.HasError() {
		t.Fatalf("building map value: %v", valueDiags)
	}
	client := fakeScalarStringer{values: map[string]string{
		"SELECT typeof(CAST(NULL AS INT))":         "INTEGER",
		"SELECT typeof(CAST(NULL AS TIMESTAMPTZ))": "TIMESTAMP WITH TIME ZONE",
	}}
	liveColumns := map[string]string{
		"id":      "INTEGER",
		"created": "TIMESTAMP WITH TIME ZONE",
	}
	var diags diag.Diagnostics
	if !tableColumnsSemanticallyEqual(ctx, client, configured, liveColumns, &diags) {
		t.Fatalf("expected semantic equality, diagnostics: %v", diags)
	}
}

func TestTableColumnsSemanticallyEqualDetectsSemanticDrift(t *testing.T) {
	ctx := context.Background()
	configured, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"id": "INT"})
	if valueDiags.HasError() {
		t.Fatalf("building map value: %v", valueDiags)
	}
	client := fakeScalarStringer{values: map[string]string{"SELECT typeof(CAST(NULL AS INT))": "INTEGER"}}
	var diags diag.Diagnostics
	if tableColumnsSemanticallyEqual(ctx, client, configured, map[string]string{"id": "BIGINT"}, &diags) {
		t.Fatal("expected semantic drift")
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

// embeddedTypeStringer resolves types with DuckDB rather than duplicating its parser.
type embeddedTypeStringer struct{ db *stdsql.DB }

func (s embeddedTypeStringer) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&value)
	return value, err
}

func TestColumnTypesSemanticallyEqualPreservesQuotedCase(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	client := embeddedTypeStringer{db: db}
	for _, tc := range []struct {
		name, configured, live string
		want                   bool
	}{
		{"alias", "int", "INTEGER", true},
		{"enum spelling", "enum('open', 'closed')", "ENUM('open', 'closed')", true},
		{"enum label drift", "ENUM('open', 'closed')", "ENUM('OPEN', 'closed')", false},
		{"nested enum drift", "STRUCT(status ENUM('open', 'closed'))[]", "STRUCT(status ENUM('OPEN', 'closed'))[]", false},
		{"escaped label drift", "ENUM('it''s open')", "ENUM('IT''S OPEN')", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonicalLive, err := canonicalColumnType(t.Context(), client, tc.live)
			if err != nil {
				t.Fatal(err)
			}
			got, err := columnTypesSemanticallyEqual(t.Context(), client, tc.configured, canonicalLive)
			if err != nil || got != tc.want {
				t.Fatalf("equivalent = %v, error = %v, want %v (live %q)", got, err, tc.want, canonicalLive)
			}
		})
	}
}

// typeResolvingClient resolves column types with a local DuckDB.
type typeResolvingClient struct {
	recordingSQLClient
	db *stdsql.DB
}

func (c *typeResolvingClient) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	return embeddedTypeStringer{db: c.db}.ScalarString(ctx, query, args...)
}

// CREATE TABLE resolves an unqualified user-defined type in the table's own
// database, but the plan-time comparison resolves types in the connection's
// current database. The plan error must name the column, keep DuckDB's
// detail, and explain how to spell such a type instead of telling the user to
// retry a plan that fails the same way every time.
func TestTableModifyPlanExplainsUnresolvableColumnType(t *testing.T) {
	ctx := context.Background()
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, statement := range []string{`ATTACH ':memory:' AS analytics`, `CREATE TYPE analytics.main.mood AS ENUM ('ok', 'sad')`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	r := &tableResource{baseResource: baseResource{provider: &providerctx.Context{SQL: &typeResolvingClient{db: db}}}}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	model := func(columnType string) tableModel {
		return tableModel{
			ID: types.StringValue("analytics.main.facts"), Database: types.StringValue("analytics"),
			Schema: types.StringValue("main"), Name: types.StringValue("facts"),
			Columns: types.MapValueMust(types.StringType, map[string]attr.Value{"m": types.StringValue(columnType)}),
		}
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, model("ENUM('ok', 'sad')")); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(ctx, model("mood")); diags.HasError() {
		t.Fatal(diags)
	}
	resp := &resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, resp)
	if len(resp.RequiresReplace) != 0 || resp.Diagnostics.ErrorsCount() != 1 {
		t.Fatalf("diagnostics = %v, replace = %v, want one error and no replacement", resp.Diagnostics, resp.RequiresReplace)
	}
	got := resp.Diagnostics.Errors()[0]
	withPath, ok := got.(diag.DiagnosticWithPath)
	if !ok || !withPath.Path().Equal(path.Root("columns").AtMapKey("m")) {
		t.Errorf("error path = %v, want columns[\"m\"]", got)
	}
	detail := got.Detail()
	if strings.Contains(detail, "Retry the plan") {
		t.Errorf("detail tells the user to retry: %s", detail)
	}
	for _, want := range []string{"Type with name mood does not exist", "not in the table's database", "analytics.main.mood", "ENUM('ok', 'sad')"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail does not mention %q: %s", want, detail)
		}
	}
}
