package datasources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestPostProcessPreservesIntegerPrecision(t *testing.T) {
	ds := &rowsDataSource{spec: rowSpec{name: "roles", postProcess: sortRowsBy("role_name")}}
	var diags diag.Diagnostics
	rowsJSON, ok := ds.queryRows(
		context.Background(),
		fakeRowsClient{rowsJSON: `[{"role_name":"b","n":9007199254740993},{"role_name":"a","n":170141183460469231731687303715884105727}]`},
		"SHOW ALL ROLES",
		&diags,
	)
	if !ok || diags.HasError() {
		t.Fatalf("queryRows failed: %v", diags)
	}
	want := `[{"n":170141183460469231731687303715884105727,"role_name":"a"},{"n":9007199254740993,"role_name":"b"}]`
	if rowsJSON != want {
		t.Fatalf("post-processed rows = %s, want %s", rowsJSON, want)
	}
}

func decodeRows(t *testing.T, raw string) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var rows []map[string]any
	if err := decoder.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func encodeRows(t *testing.T, rows []map[string]any) string {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSortRowsByKeysIsDeterministic(t *testing.T) {
	sortRuns := sortRowsByKeys(rowSortKey{field: "run_number", descending: true})
	first := encodeRows(t, sortRuns(decodeRows(t, `[{"run_number":9},{"run_number":10},{"run_number":2}]`)))
	second := encodeRows(t, sortRuns(decodeRows(t, `[{"run_number":2},{"run_number":9},{"run_number":10}]`)))
	if first != second || first != `[{"run_number":10},{"run_number":9},{"run_number":2}]` {
		t.Fatalf("run order = %s and %s, want numeric newest first regardless of input order", first, second)
	}

	// Rows without any sort column still get a total order.
	byID := sortRowsByKeys(rowSortKey{field: "id"})
	left := encodeRows(t, byID(decodeRows(t, `[{"name":"b"},{"name":"a"}]`)))
	right := encodeRows(t, byID(decodeRows(t, `[{"name":"a"},{"name":"b"}]`)))
	if left != right {
		t.Fatalf("fallback order differs: %s vs %s", left, right)
	}

	// Candidate columns are tried in order.
	versions := sortRowsByKeys(newestVersionFirst()...)
	if got := encodeRows(t, versions(decodeRows(t, `[{"version":1},{"version":3},{"version":2}]`))); got != `[{"version":3},{"version":2},{"version":1}]` {
		t.Fatalf("version order = %s", got)
	}
}

func TestListingSpecsHaveDeterministicOrder(t *testing.T) {
	for _, name := range []string{
		"attached_databases", "buckets_for_secret", "files", "dives", "dive_versions", "flights",
		"flight_versions", "flight_runs", "guides", "guide_versions",
	} {
		if findSpec(t, name).postProcess == nil {
			t.Fatalf("%s must sort its rows", name)
		}
	}
}

func TestUnpagedFunctionsSupportSQLPaging(t *testing.T) {
	cases := []struct {
		spec  string
		model rowsModel
		want  string
	}{
		{"files", rowsModel{Path: types.StringValue("s3://bucket/"), Limit: types.Int64Value(5), Offset: types.Int64Null()}, "SELECT * FROM md_list_files('s3://bucket/') ORDER BY ALL LIMIT 5"},
		{"files", rowsModel{Path: types.StringValue("s3://bucket/"), Limit: types.Int64Null(), Offset: types.Int64Null()}, "SELECT * FROM md_list_files('s3://bucket/')"},
		{"buckets_for_secret", rowsModel{SecretName: types.StringValue("s3"), Limit: types.Int64Value(2), Offset: types.Int64Value(4)}, "SELECT * FROM md_list_buckets_for_secret('s3') ORDER BY ALL LIMIT 2 OFFSET 4"},
	}
	for _, tc := range cases {
		query, err := findSpec(t, tc.spec).build(tc.model)
		if err != nil || query != tc.want {
			t.Fatalf("%s build = %q, %v, want %q", tc.spec, query, err, tc.want)
		}
	}
}

func TestTokenMetadataOnlySortsByID(t *testing.T) {
	tokens := tokenMetadataOnly([]mdrest.Token{{ID: "b", Token: "secret"}, {ID: "a"}})
	if tokens[0].ID != "a" || tokens[1].ID != "b" || tokens[1].Token != "" {
		t.Fatalf("tokens = %#v, want sorted metadata without secrets", tokens)
	}
}
