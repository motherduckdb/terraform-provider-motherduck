package datasources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func typedRow(t *testing.T, spec rowSpec, rowsJSON string) map[string]types.String {
	t.Helper()
	rows, diags := spec.typedRowsValue(rowsJSON)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if len(rows.Elements()) != 1 {
		t.Fatalf("typed rows = %d, want 1", len(rows.Elements()))
	}
	values := map[string]types.String{}
	for name, value := range rows.Elements()[0].(types.Object).Attributes() {
		values[name] = value.(types.String)
	}
	return values
}

func typedRowAttr(t *testing.T, spec rowSpec, name string) typedRowAttribute {
	t.Helper()
	for _, attr := range spec.typedRows {
		if attr.name == name {
			return attr
		}
	}
	t.Fatalf("spec %s has no typed row %q", spec.name, name)
	return typedRowAttribute{}
}

// The live SHARED_WITH_ME catalog has no source column. source_database must
// stay in the schema so existing configurations keep working, but it is
// deprecated and always null.
func TestSharedWithMeRowsMirrorLiveColumns(t *testing.T) {
	spec := findSpec(t, "shared_with_me")
	row := typedRow(t, spec, `[{"name":"tenant","url":"md:_share/tenant/abc","owner":"owner","visibility":"DISCOVERABLE","created_ts":"2026-01-01T00:00:00Z","updated_ts":"2026-02-01T00:00:00Z","update_mode":"AUTOMATIC","access":"ORGANIZATION"}]`)
	want := map[string]types.String{
		"name":            types.StringValue("tenant"),
		"source_database": types.StringNull(),
		"owner":           types.StringValue("owner"),
		"url":             types.StringValue("md:_share/tenant/abc"),
		"access":          types.StringValue("ORGANIZATION"),
		"visibility":      types.StringValue("DISCOVERABLE"),
		"update_mode":     types.StringValue("AUTOMATIC"),
		"created_ts":      types.StringValue("2026-01-01T00:00:00Z"),
		"updated_ts":      types.StringValue("2026-02-01T00:00:00Z"),
	}
	if len(row) != len(want) {
		t.Fatalf("typed columns = %v, want %v", row, want)
	}
	for name, value := range want {
		if !row[name].Equal(value) {
			t.Fatalf("%s = %v, want %v", name, row[name], value)
		}
	}
	source := typedRowAttr(t, spec, "source_database")
	if source.deprecation == "" || !strings.Contains(source.deprecation, "motherduck_share") {
		t.Fatalf("source_database deprecation = %q, want a pointer to motherduck_share", source.deprecation)
	}
	var resp datasource.SchemaResponse
	(&rowsDataSource{spec: spec}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	nested := resp.Schema.Attributes["rows"].(schema.ListNestedAttribute).NestedObject.Attributes
	if nested["source_database"].GetDeprecationMessage() == "" {
		t.Fatal("rows.source_database must carry a deprecation message")
	}
	if !nested["url"].IsSensitive() {
		t.Fatal("rows.url must stay sensitive")
	}
}

func TestDatabaseSnapshotRowsExposeStorageColumns(t *testing.T) {
	row := typedRow(t, findSpec(t, "database_snapshots"), `[{"database_name":"analytics","database_id":"00000000-0000-0000-0000-000000000001","snapshot_id":"00000000-0000-0000-0000-000000000002","snapshot_name":null,"created_ts":"2026-01-01T00:00:00","active_bytes":18446744073709551615,"bytes_written":10,"bytes_deleted":0,"user_name":"writer","user_id":"00000000-0000-0000-0000-000000000003"}]`)
	for name, want := range map[string]types.String{
		"database_id":   types.StringValue("00000000-0000-0000-0000-000000000001"),
		"active_bytes":  types.StringValue("18446744073709551615"),
		"bytes_written": types.StringValue("10"),
		"bytes_deleted": types.StringValue("0"),
		"user_name":     types.StringValue("writer"),
		"user_id":       types.StringValue("00000000-0000-0000-0000-000000000003"),
		"snapshot_name": types.StringNull(),
	} {
		if !row[name].Equal(want) {
			t.Fatalf("%s = %v, want %v", name, row[name], want)
		}
	}
}

func TestOwnedShareRowsExposeSourceDatabaseUUID(t *testing.T) {
	row := typedRow(t, findSpec(t, "owned_shares"), `[{"name":"analytics","source_db_name":"db","source_db_uuid":"00000000-0000-0000-0000-0000000000a1"}]`)
	if !row["source_database_uuid"].Equal(types.StringValue("00000000-0000-0000-0000-0000000000a1")) {
		t.Fatalf("source_database_uuid = %v", row["source_database_uuid"])
	}
}

// Columns that carry content, configuration, or URLs must stay sensitive in
// typed rows because rows_json is not the only way to read them.
func TestTypedRowsKeepContentConfigAndURLsSensitive(t *testing.T) {
	sensitive := map[string]bool{
		"url": true, "storage_url": true, "version_storage_url": true,
		"content": true, "config": true, "line": true,
		"required_resources": true, "version_required_resources": true, "references": true,
	}
	seen := map[string]bool{}
	for _, spec := range rowSpecs() {
		for _, attr := range spec.typedRows {
			if sensitive[attr.name] {
				seen[attr.name] = true
				if !attr.sensitive {
					t.Errorf("%s.rows.%s must be sensitive", spec.name, attr.name)
				}
			}
			if strings.Contains(attr.name, "source_code") || strings.Contains(attr.name, "secret_string") {
				t.Errorf("%s.rows.%s must not be exposed as a typed row", spec.name, attr.name)
			}
		}
	}
	for name := range sensitive {
		if !seen[name] {
			t.Errorf("no typed row named %q, update this test when the column moves", name)
		}
	}
}

func TestApplicationRowsMirrorLiveColumns(t *testing.T) {
	cases := map[string][]string{
		"dive":            {"id", "title", "current_version", "status", "version_id", "version_storage_url", "version_required_resources", "content"},
		"dives":           {"id", "title", "owner_name", "status_applies_to_version", "required_resources"},
		"dive_versions":   {"id", "version", "storage_url", "description", "created_at", "api_version", "required_resources"},
		"flight":          {"flight_id", "flight_name", "schedule_cron", "current_version", "owner_name"},
		"flight_versions": {"version_id", "flight_id", "flight_version", "access_token_name", "flight_secret_names", "config", "max_runtime_sec", "instance_type"},
		"flight_runs":     {"run_id", "flight_version", "config", "run_number", "is_scheduled", "status", "exit_code"},
		"guide":           {"id", "topic", "title", "version", "version_change_comment", "version_external_id", "version_created_at", "references", "content"},
		"guide_versions":  {"version", "change_comment", "external_id", "created_at"},
		"attached_databases": {
			"alias", "is_share", "is_readonly", "is_error_catalog", "error_message", "is_ducklake",
		},
		"files":               {"name", "type", "size", "last_modified"},
		"buckets_for_secret":  {"name", "creation_date", "region", "arn"},
		"current_user_tokens": {"token_name", "token_type", "created_ts", "expire_at"},
		"storage_info": {
			"database_name", "database_id", "created_ts", "deleted_ts", "user_name", "active_bytes", "historical_bytes",
			"retained_for_clone_bytes", "failsafe_bytes", "transient", "historical_snapshot_retention", "computed_ts",
		},
	}
	for name, columns := range cases {
		spec := findSpec(t, name)
		for _, column := range columns {
			typedRowAttr(t, spec, column)
		}
	}
	// flight_versions never returns source code. Only MD_GET_FLIGHT_VERSION does.
	for _, attr := range findSpec(t, "flight_versions").typedRows {
		if attr.name == "source_code" || attr.name == "requirements_txt" {
			t.Fatalf("flight_versions must not expose %s", attr.name)
		}
	}
}

func TestFlightVersionsSortNewestFirst(t *testing.T) {
	sortVersions := findSpec(t, "flight_versions").postProcess
	got := encodeRows(t, sortVersions(decodeRows(t, `[{"flight_version":2},{"flight_version":10},{"flight_version":1}]`)))
	if got != `[{"flight_version":10},{"flight_version":2},{"flight_version":1}]` {
		t.Fatalf("flight version order = %s", got)
	}
}

func TestDiveVersionsPassesPagingNatively(t *testing.T) {
	spec := findSpec(t, "dive_versions")
	id := "123e4567-e89b-42d3-a456-426614174000"
	query, err := spec.build(rowsModel{DiveID: types.StringValue(id), Limit: types.Int64Null(), Offset: types.Int64Null()})
	if err != nil || query != "SELECT * FROM MD_LIST_DIVE_VERSIONS(id := '"+id+"'::UUID)" {
		t.Fatalf("unpaged dive_versions = %q, %v", query, err)
	}
	query, err = spec.build(rowsModel{DiveID: types.StringValue(id), Limit: types.Int64Value(5), Offset: types.Int64Value(10)})
	if err != nil || query != `SELECT * FROM MD_LIST_DIVE_VERSIONS("limit" := 5, "offset" := 10, id := '`+id+`'::UUID)` {
		t.Fatalf("paged dive_versions = %q, %v", query, err)
	}
}

func TestGuideReadsOptionalVersion(t *testing.T) {
	spec := findSpec(t, "guide")
	id := "123e4567-e89b-42d3-a456-426614174000"
	query, err := spec.build(rowsModel{GuideID: types.StringValue(id), Version: types.Int64Null()})
	if err != nil || query != "SELECT * FROM MD_GET_GUIDE(id := '"+id+"'::UUID)" {
		t.Fatalf("current guide = %q, %v", query, err)
	}
	query, err = spec.build(rowsModel{GuideID: types.StringValue(id), Version: types.Int64Value(3)})
	if err != nil || query != "SELECT * FROM MD_GET_GUIDE(id := '"+id+"'::UUID, version := 3)" {
		t.Fatalf("versioned guide = %q, %v", query, err)
	}
	version, ok := rowAttribute("version", false).(schema.Int64Attribute)
	if !ok || !version.Optional || len(version.Validators) == 0 {
		t.Fatalf("version attribute = %#v, want optional with a minimum", rowAttribute("version", false))
	}
}

func TestCurrentUserTokensListsSessionTokens(t *testing.T) {
	spec := findSpec(t, "current_user_tokens")
	query, err := spec.build(rowsModel{})
	if err != nil || query != "SELECT * FROM md_access_tokens()" {
		t.Fatalf("current_user_tokens = %q, %v", query, err)
	}
	if len(spec.attrs) != 0 {
		t.Fatalf("current_user_tokens attrs = %v, want none", spec.attrs)
	}
	sorted := encodeRows(t, spec.postProcess(decodeRows(t, `[{"token_name":"b"},{"token_name":"a"}]`)))
	if sorted != `[{"token_name":"a"},{"token_name":"b"}]` {
		t.Fatalf("token order = %s", sorted)
	}
	if strings.Contains(spec.description, "token_id") {
		t.Fatal("description must not promise token IDs")
	}
}

func TestStorageInfoQueries(t *testing.T) {
	spec := findSpec(t, "storage_info")
	cases := []struct {
		model rowsModel
		want  string
	}{
		{rowsModel{}, "SELECT * FROM MD_INFORMATION_SCHEMA.STORAGE_INFO ORDER BY database_name, database_id, computed_ts"},
		{rowsModel{IncludeHistory: types.BoolValue(false), DatabaseName: types.StringValue("tenant's db")}, "SELECT * FROM MD_INFORMATION_SCHEMA.STORAGE_INFO WHERE database_name = 'tenant''s db' ORDER BY database_name, database_id, computed_ts"},
		{rowsModel{IncludeHistory: types.BoolValue(true), Limit: types.Int64Value(10), Offset: types.Int64Value(20)}, "SELECT * FROM MD_INFORMATION_SCHEMA.STORAGE_INFO_HISTORY ORDER BY database_name, database_id, computed_ts LIMIT 10 OFFSET 20"},
	}
	for _, tc := range cases {
		query, err := spec.build(tc.model)
		if err != nil || query != tc.want {
			t.Fatalf("storage_info build = %q, %v, want %q", query, err, tc.want)
		}
	}
}

func TestStorageInfoExplainsAdminOnlyErrors(t *testing.T) {
	ds := &rowsDataSource{spec: findSpec(t, "storage_info")}
	var diags diag.Diagnostics
	_, ok := ds.queryRows(context.Background(), fakeRowsClient{err: errors.New("Invalid Error: MDExternalException: User not authorized. This is an admin only function.")}, "SELECT 1", &diags)
	if ok || !diags.HasError() {
		t.Fatal("admin-only failure must fail the read")
	}
	if diags[0].Summary() != "MotherDuck organization admin permission required" || !strings.Contains(diags[0].Detail(), "organization admin") || !strings.Contains(diags[0].Detail(), "admin only function") {
		t.Fatalf("unexpected diagnostic: %s: %s", diags[0].Summary(), diags[0].Detail())
	}

	// Other data sources keep the generic read error for the same message.
	var generic diag.Diagnostics
	(&rowsDataSource{spec: findSpec(t, "databases")}).queryRows(context.Background(), fakeRowsClient{err: errors.New("admin only function")}, "SELECT 1", &generic)
	if !generic.HasError() || generic[0].Summary() != "Unable to read MotherDuck rows data source" {
		t.Fatalf("unexpected generic diagnostic: %v", generic)
	}
}
