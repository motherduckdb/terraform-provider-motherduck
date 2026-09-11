package resources

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
