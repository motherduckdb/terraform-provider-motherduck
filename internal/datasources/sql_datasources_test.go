package datasources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRowSpecsCoverPlannedSQLDataSources(t *testing.T) {
	want := map[string]bool{
		"databases":          true,
		"attached_databases": true,
		"database_snapshots": true,
		"owned_shares":       true,
		"shared_with_me":     true,
		"secrets":            true,
		"buckets_for_secret": true,
		"files":              true,
		"roles":              true,
		"role_members":       true,
		"roles_for_user":     true,
		"roles_for_role":     true,
		"dives":              true,
		"dive":               true,
		"dive_versions":      true,
		"flights":            true,
		"flight":             true,
		"flight_versions":    true,
		"flight_runs":        true,
		"flight_logs":        true,
		"guides":             true,
		"guide":              true,
		"guide_grantees":     true,
		"guide_versions":     true,
	}
	got := map[string]bool{}
	for _, spec := range rowSpecs() {
		got[spec.name] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("missing spec %s; all specs: %#v", name, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("row spec count = %d, want %d: %#v", len(got), len(want), got)
	}
}

func TestSQLDataSourceSchemasHaveDescriptions(t *testing.T) {
	for _, ds := range []datasource.DataSource{
		NewCurrentUserDataSource(),
		NewVersionDataSource(),
		NewLiveDucklingSizeDataSource(),
		NewOwnedShareDataSource(),
	} {
		var resp datasource.SchemaResponse
		ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
		if strings.TrimSpace(resp.Schema.MarkdownDescription) == "" {
			t.Fatalf("%T has an empty schema description", ds)
		}
		assertDataSourceAttributeDescriptions(t, resp.Schema.Attributes)
	}

	for _, spec := range rowSpecs() {
		ds := &rowsDataSource{spec: spec}
		var resp datasource.SchemaResponse
		ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
		if strings.TrimSpace(resp.Schema.MarkdownDescription) == "" {
			t.Fatalf("row data source %q has an empty schema description", spec.name)
		}
		assertDataSourceAttributeDescriptions(t, resp.Schema.Attributes)
	}
}

func assertDataSourceAttributeDescriptions(t *testing.T, attrs map[string]schema.Attribute) {
	t.Helper()
	for name, attr := range attrs {
		if strings.TrimSpace(attr.GetMarkdownDescription()) == "" && strings.TrimSpace(attr.GetDescription()) == "" {
			t.Fatalf("attribute %s has an empty description", name)
		}
		if nested, ok := attr.(schema.ListNestedAttribute); ok {
			for nestedName, nestedAttr := range nested.NestedObject.Attributes {
				if strings.TrimSpace(nestedAttr.GetMarkdownDescription()) == "" && strings.TrimSpace(nestedAttr.GetDescription()) == "" {
					t.Fatalf("nested attribute %s.%s has an empty description", name, nestedName)
				}
			}
		}
	}
}

func TestOwnedShareDataSourceSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewOwnedShareDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	name, ok := resp.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("name attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["name"])
	}
	if !name.Required {
		t.Fatal("name should be required")
	}
	url, ok := resp.Schema.Attributes["url"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("url attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["url"])
	}
	if !url.Sensitive {
		t.Fatal("owned share url must be sensitive because unrestricted share URLs can grant access")
	}
}

func TestRowsSpecsDeclareRequiredFunctions(t *testing.T) {
	// #nosec G101 -- function names include "secret" but are not credentials.
	want := map[string]string{
		"attached_databases": "md_attached_databases",
		"buckets_for_secret": "md_list_buckets_for_secret",
		"files":              "md_list_files",
		"role_members":       "md_get_role_members",
		"dives":              "md_list_dives",
		"dive":               "md_get_dive",
		"dive_versions":      "md_list_dive_versions",
		"flights":            "md_list_flights",
		"flight":             "md_get_flight",
		"flight_versions":    "md_list_flight_versions",
		"flight_runs":        "md_list_flight_runs",
		"flight_logs":        "md_get_flight_logs",
		"guides":             "md_list_guides",
		"guide":              "md_get_guide",
		"guide_grantees":     "md_list_guide_grantees",
		"guide_versions":     "md_list_guide_versions",
	}
	for name, fn := range want {
		if got := findSpec(t, name).requiredFunction; got != fn {
			t.Fatalf("spec %s requiredFunction = %q, want %q", name, got, fn)
		}
	}
}

func TestRowsSpecsDeclareRequiredAttributes(t *testing.T) {
	tests := map[string][]string{
		"buckets_for_secret": {"secret_name"},
		"files":              {"path"},
		"role_members":       {"role_name"},
		"roles_for_user":     {"username"},
		"roles_for_role":     {"role_name"},
		"dive":               {"dive_id"},
		"dive_versions":      {"dive_id"},
		"flight":             {"flight_id"},
		"flight_versions":    {"flight_id"},
		"flight_runs":        {"flight_id"},
		"flight_logs":        {"flight_id", "run_number"},
		"guide":              {"guide_id"},
		"guide_grantees":     {"guide_id"},
		"guide_versions":     {"guide_id"},
	}
	for name, requiredAttrs := range tests {
		spec := findSpec(t, name)
		for _, attr := range requiredAttrs {
			if !spec.attrRequired(attr) {
				t.Fatalf("spec %s attrRequired(%s) = false, want true", name, attr)
			}
		}
	}
}

func TestRowsSpecAttributesAreSupportedByConfigAndStateSwitches(t *testing.T) {
	supported := map[string]bool{
		"name":               true,
		"database_name":      true,
		"secret_name":        true,
		"path":               true,
		"dive_id":            true,
		"flight_id":          true,
		"guide_id":           true,
		"role_name":          true,
		"username":           true,
		"topic":              true,
		"reference_type":     true,
		"reference_url":      true,
		"reference_schema":   true,
		"reference_table":    true,
		"reference_column":   true,
		"reference_view":     true,
		"reference_macro":    true,
		"reference_uuid":     true,
		"run_number":         true,
		"limit":              true,
		"offset":             true,
		"include_org_shares": true,
		"owner_only":         true,
	}
	for _, spec := range rowSpecs() {
		for _, attr := range spec.attrs {
			if !supported[attr] {
				t.Fatalf("spec %s uses unsupported attribute %q", spec.name, attr)
			}
		}
		for _, attr := range spec.requiredAttrs {
			if !supported[attr] {
				t.Fatalf("spec %s requires unsupported attribute %q", spec.name, attr)
			}
		}
	}
}

func TestRowsDataSourceFunctionAvailableDiagnostics(t *testing.T) {
	ds := &rowsDataSource{spec: rowSpec{name: "files", requiredFunction: "md_list_files"}}
	var missingDiags diag.Diagnostics
	if ds.functionAvailable(context.Background(), fakeFunctionClient{available: false}, &missingDiags) {
		t.Fatal("missing function should not be available")
	}
	if !missingDiags.HasError() || !strings.Contains(missingDiags[0].Detail(), "md_list_files") {
		t.Fatalf("expected md_list_files diagnostic, got %v", missingDiags)
	}

	var errDiags diag.Diagnostics
	if ds.functionAvailable(context.Background(), fakeFunctionClient{err: errors.New("boom")}, &errDiags) {
		t.Fatal("function inspection error should not be available")
	}
	if !errDiags.HasError() || !strings.Contains(errDiags[0].Summary(), "inspect") {
		t.Fatalf("expected inspection diagnostic, got %v", errDiags)
	}
}

func TestRoleRowsSpecsUseShowCommands(t *testing.T) {
	for _, name := range []string{"roles", "roles_for_user", "roles_for_role"} {
		if fn := findSpec(t, name).requiredFunction; fn != "" {
			t.Fatalf("spec %s requiredFunction = %q, want none because SHOW commands are not functions", name, fn)
		}
		if findSpec(t, name).postProcess == nil {
			t.Fatalf("spec %s should sort rows client-side because SHOW cannot take ORDER BY", name)
		}
	}

	query, err := findSpec(t, "roles").build(rowsModel{})
	if err != nil || query != "SHOW ALL ROLES" {
		t.Fatalf("roles build = %q, %v, want SHOW ALL ROLES", query, err)
	}

	query, err = findSpec(t, "roles_for_user").build(rowsModel{Username: types.StringValue(`weird"user`)})
	if err != nil || query != `SHOW ROLES TO USER "weird""user"` {
		t.Fatalf("roles_for_user build = %q, %v", query, err)
	}
	if _, err := findSpec(t, "roles_for_user").build(rowsModel{}); err == nil {
		t.Fatal("roles_for_user should require username")
	}

	query, err = findSpec(t, "roles_for_role").build(rowsModel{RoleName: types.StringValue("analytics-readers")})
	if err != nil || query != `SHOW ROLES TO ROLE "analytics-readers"` {
		t.Fatalf("roles_for_role build = %q, %v", query, err)
	}
	if _, err := findSpec(t, "roles_for_role").build(rowsModel{}); err == nil {
		t.Fatal("roles_for_role should require role_name")
	}
}

func TestSortRowsBy(t *testing.T) {
	rows := []map[string]any{
		{"role_name": "zeta", "role_type": "custom"},
		{"role_name": nil},
		{"role_name": "alpha", "role_type": "system"},
		{"role_name": "midway"},
	}
	sorted := sortRowsBy("role_name")(rows)
	got := make([]string, 0, len(sorted))
	for _, row := range sorted {
		name, _ := row["role_name"].(string)
		got = append(got, name)
	}
	if strings.Join(got, ",") != ",alpha,midway,zeta" {
		t.Fatalf("sorted role names = %#v", got)
	}
}

func TestRowsDataSourceQueryRowsClassifiesErrors(t *testing.T) {
	ds := &rowsDataSource{spec: rowSpec{name: "roles"}}

	var unsupportedDiags diag.Diagnostics
	if _, ok := ds.queryRows(context.Background(), fakeRowsClient{err: errors.New("Parser Error: syntax error at or near \"SHOW\"")}, "SHOW ALL ROLES", &unsupportedDiags); ok {
		t.Fatal("unsupported command must not succeed")
	}
	if !unsupportedDiags.HasError() ||
		unsupportedDiags[0].Summary() != "MotherDuck SQL command unavailable" ||
		!strings.Contains(unsupportedDiags[0].Detail(), "SHOW ALL ROLES") ||
		!strings.Contains(unsupportedDiags[0].Detail(), "motherduck_roles") ||
		!strings.Contains(unsupportedDiags[0].Detail(), "Confirm the account, region, and client support this feature") {
		t.Fatalf("unexpected unsupported diagnostics: %v", unsupportedDiags)
	}

	var otherDiags diag.Diagnostics
	if _, ok := ds.queryRows(context.Background(), fakeRowsClient{err: errors.New("permission denied")}, "SHOW ALL ROLES", &otherDiags); ok {
		t.Fatal("query errors must not succeed")
	}
	if !otherDiags.HasError() || otherDiags[0].Summary() != "Unable to read MotherDuck rows data source" {
		t.Fatalf("unexpected error diagnostics: %v", otherDiags)
	}
}

func TestRowsDataSourceQueryRowsAppliesPostProcess(t *testing.T) {
	ds := &rowsDataSource{spec: rowSpec{name: "roles", postProcess: sortRowsBy("role_name")}}
	var diags diag.Diagnostics
	rowsJSON, ok := ds.queryRows(
		context.Background(),
		fakeRowsClient{rowsJSON: `[{"role_name":"zeta"},{"role_name":"alpha"}]`},
		"SHOW ALL ROLES",
		&diags,
	)
	if !ok || diags.HasError() {
		t.Fatalf("queryRows failed: %v", diags)
	}
	if rowsJSON != `[{"role_name":"alpha"},{"role_name":"zeta"}]` {
		t.Fatalf("post-processed rows = %s", rowsJSON)
	}
}

func TestRowsSpecFilesRequiresPath(t *testing.T) {
	spec := findSpec(t, "files")
	_, err := spec.build(rowsModel{})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path error, got %v", err)
	}
}

func TestRowsSpecFlightsUsesNamedArgs(t *testing.T) {
	spec := findSpec(t, "flights")
	query, err := spec.build(rowsModel{Limit: types.Int64Value(10), Offset: types.Int64Value(5)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `MD_LIST_FLIGHTS("LIMIT" := 10, "OFFSET" := 5)`) {
		t.Fatalf("unexpected query: %s", query)
	}
}

func TestRowsSpecSecretsFiltersMotherDuckStorage(t *testing.T) {
	spec := findSpec(t, "secrets")
	query, err := spec.build(rowsModel{Name: types.StringValue("__default_s3")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "storage = 'motherduck'") || !strings.Contains(query, "name = '__default_s3'") {
		t.Fatalf("unexpected query: %s", query)
	}
	if strings.Contains(query, "secret_string") {
		t.Fatalf("secrets data source must not expose secret_string: %s", query)
	}
}

func TestRowsSpecSharedWithMeCanFilterByName(t *testing.T) {
	spec := findSpec(t, "shared_with_me")
	query, err := spec.build(rowsModel{Name: types.StringValue("tenant share")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "MD_INFORMATION_SCHEMA.SHARED_WITH_ME") || !strings.Contains(query, "name = 'tenant share'") {
		t.Fatalf("unexpected query: %s", query)
	}
}

func TestRowsSpecSchemasExposeOnlyRelevantAttributes(t *testing.T) {
	tests := map[string][]string{
		"databases":          {"limit", "name", "offset", "rows", "rows_json"},
		"owned_shares":       {"limit", "name", "offset", "rows", "rows_json"},
		"shared_with_me":     {"limit", "name", "offset", "rows", "rows_json"},
		"attached_databases": {"rows_json"},
		"dives":              {"include_org_shares", "limit", "offset", "rows_json"},
		"flight_logs":        {"flight_id", "rows", "rows_json", "run_number"},
		"flights":            {"limit", "offset", "owner_only", "rows", "rows_json"},
		"roles":              {"rows", "rows_json"},
		"role_members":       {"role_name", "rows", "rows_json"},
		"guides":             {"limit", "offset", "reference_column", "reference_macro", "reference_schema", "reference_table", "reference_type", "reference_url", "reference_uuid", "reference_view", "rows", "rows_json", "topic"},
		"guide":              {"guide_id", "rows_json"},
		"guide_grantees":     {"guide_id", "rows", "rows_json"},
		"guide_versions":     {"guide_id", "limit", "offset", "rows_json"},
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			ds := &rowsDataSource{spec: findSpec(t, name)}
			var resp datasource.SchemaResponse
			ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
			got := make([]string, 0, len(resp.Schema.Attributes))
			for attr := range resp.Schema.Attributes {
				got = append(got, attr)
			}
			if strings.Join(sorted(got), ",") != strings.Join(sorted(want), ",") {
				t.Fatalf("attributes = %#v, want %#v", sorted(got), want)
			}
		})
	}
}

func TestRowsSpecGuidesBuildsReferenceFilter(t *testing.T) {
	spec := findSpec(t, "guides")
	query, err := spec.build(rowsModel{
		ReferenceType:   types.StringValue("catalog"),
		ReferenceURL:    types.StringValue("md:analytics"),
		ReferenceSchema: types.StringValue("main"),
		ReferenceTable:  types.StringValue("invoices"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"MD_LIST_GUIDES", "reference :=", "'type': 'catalog'", "'url': 'md:analytics'", "'table': 'invoices'"} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("Guide query %q does not contain %q", query, fragment)
		}
	}
}

func TestRowsSpecGuidesRejectsInvalidCatalogReferenceFilter(t *testing.T) {
	spec := findSpec(t, "guides")
	tests := map[string]rowsModel{
		"column without table": {
			ReferenceType:   types.StringValue("catalog"),
			ReferenceURL:    types.StringValue("md:analytics"),
			ReferenceColumn: types.StringValue("amount"),
		},
		"table without schema": {
			ReferenceType:  types.StringValue("catalog"),
			ReferenceURL:   types.StringValue("md:analytics"),
			ReferenceTable: types.StringValue("invoices"),
		},
		"multiple narrowings": {
			ReferenceType:   types.StringValue("catalog"),
			ReferenceURL:    types.StringValue("md:analytics"),
			ReferenceSchema: types.StringValue("main"),
			ReferenceTable:  types.StringValue("invoices"),
			ReferenceView:   types.StringValue("active_invoices"),
		},
	}
	for name, model := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := spec.build(model); err == nil {
				t.Fatal("expected invalid Guide reference filter error")
			}
		})
	}
}

func TestRowsSpecFlightsSupportsOwnerScope(t *testing.T) {
	spec := findSpec(t, "flights")
	query, err := spec.build(rowsModel{
		Limit:     types.Int64Value(20),
		OwnerOnly: types.BoolValue(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "owner_only := true") {
		t.Fatalf("unexpected query: %s", query)
	}
}

func TestStableCatalogRowsSupportLimitOffset(t *testing.T) {
	spec := findSpec(t, "owned_shares")
	query, err := spec.build(rowsModel{Limit: types.Int64Value(10), Offset: types.Int64Value(5)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "ORDER BY name LIMIT 10 OFFSET 5") {
		t.Fatalf("query did not include stable pagination: %s", query)
	}
}

func TestStableRowsDataSourcesExposeTypedRows(t *testing.T) {
	stable := map[string]bool{
		"databases":          true,
		"database_snapshots": true,
		"owned_shares":       true,
		"shared_with_me":     true,
		"secrets":            true,
		"flights":            true,
		"flight_logs":        true,
		"roles":              true,
		"role_members":       true,
		"roles_for_user":     true,
		"roles_for_role":     true,
		"guides":             true,
		"guide_grantees":     true,
	}
	for _, spec := range rowSpecs() {
		t.Run(spec.name, func(t *testing.T) {
			ds := &rowsDataSource{spec: spec}
			var resp datasource.SchemaResponse
			ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
			attr, hasRows := resp.Schema.Attributes["rows"]
			if stable[spec.name] != hasRows {
				t.Fatalf("rows attribute present = %v, want %v", hasRows, stable[spec.name])
			}
			if !stable[spec.name] {
				return
			}
			rows, ok := attr.(schema.ListNestedAttribute)
			if !ok {
				t.Fatalf("rows attribute = %T, want schema.ListNestedAttribute", attr)
			}
			if !rows.Computed {
				t.Fatal("typed rows should be computed")
			}
			if len(rows.NestedObject.Attributes) == 0 {
				t.Fatal("typed rows should declare nested attributes")
			}
		})
	}
}

func TestTypedRowsValueMapsStableColumns(t *testing.T) {
	spec := findSpec(t, "owned_shares")
	rows, diags := spec.typedRowsValue(`[{"name":"analytics","source_db_name":"db","access":"UNRESTRICTED","visibility":"HIDDEN","update":"MANUAL","url":"md:_share/abc","created_ts":"2026-01-01T00:00:00Z"}]`)
	if diags.HasError() {
		t.Fatalf("typedRowsValue diagnostics: %v", diags)
	}
	if rows.IsNull() || rows.IsUnknown() {
		t.Fatal("typed rows should be known")
	}
}

func TestFlightLogRowsExposeLineOrientedColumns(t *testing.T) {
	spec := findSpec(t, "flight_logs")
	want := map[string]bool{
		"line_number": false,
		"reported_at": false,
		"line":        true,
	}
	if len(spec.typedRows) != len(want) {
		t.Fatalf("typed Flight log columns = %d, want %d", len(spec.typedRows), len(want))
	}
	for _, attr := range spec.typedRows {
		sensitive, ok := want[attr.name]
		if !ok {
			t.Fatalf("unexpected typed Flight log column %q", attr.name)
		}
		if attr.sensitive != sensitive {
			t.Fatalf("Flight log column %q sensitive = %t, want %t", attr.name, attr.sensitive, sensitive)
		}
	}

	rows, diags := spec.typedRowsValue(`[{"line_number":12,"reported_at":"2026-08-28T12:00:00Z","line":"pipeline complete"}]`)
	if diags.HasError() {
		t.Fatalf("typedRowsValue diagnostics: %v", diags)
	}
	if rows.IsNull() || rows.IsUnknown() || len(rows.Elements()) != 1 {
		t.Fatalf("typed Flight log rows = %#v, want one known row", rows)
	}
}

func TestRowsJSONIsSensitive(t *testing.T) {
	for _, spec := range rowSpecs() {
		t.Run(spec.name, func(t *testing.T) {
			ds := &rowsDataSource{spec: spec}
			var resp datasource.SchemaResponse
			ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
			attr, ok := resp.Schema.Attributes["rows_json"].(schema.StringAttribute)
			if !ok {
				t.Fatalf("rows_json attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["rows_json"])
			}
			if !attr.Sensitive {
				t.Fatal("rows_json must be sensitive because raw catalog rows can include share URLs and account metadata")
			}
		})
	}
}

func TestRowsSpecSchemasMarkRequiredAttributes(t *testing.T) {
	ds := &rowsDataSource{spec: findSpec(t, "flight_logs")}
	var resp datasource.SchemaResponse
	ds.Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	flightID, ok := resp.Schema.Attributes["flight_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("flight_id attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["flight_id"])
	}
	runNumber, ok := resp.Schema.Attributes["run_number"].(schema.Int64Attribute)
	if !ok {
		t.Fatalf("run_number attribute = %T, want schema.Int64Attribute", resp.Schema.Attributes["run_number"])
	}
	if !flightID.Required || !runNumber.Required {
		t.Fatalf("flight_id Required=%v run_number Required=%v, want both true", flightID.Required, runNumber.Required)
	}
	if len(runNumber.Validators) == 0 {
		t.Fatal("run_number should have validators")
	}
}

func TestRowsDataSourceNumericValidators(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		attr     string
		required bool
		value    types.Int64
		wantErr  bool
	}{
		"limit allows zero":        {attr: "limit", value: types.Int64Value(0), wantErr: false},
		"limit rejects negative":   {attr: "limit", value: types.Int64Value(-1), wantErr: true},
		"offset allows zero":       {attr: "offset", value: types.Int64Value(0), wantErr: false},
		"offset rejects negative":  {attr: "offset", value: types.Int64Value(-1), wantErr: true},
		"run number starts at one": {attr: "run_number", required: true, value: types.Int64Value(1), wantErr: false},
		"run number rejects zero":  {attr: "run_number", required: true, value: types.Int64Value(0), wantErr: true},
		"unknown passes":           {attr: "limit", value: types.Int64Unknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			attr, ok := rowAttribute(tc.attr, tc.required).(schema.Int64Attribute)
			if !ok {
				t.Fatalf("%s attribute = %T, want schema.Int64Attribute", tc.attr, rowAttribute(tc.attr, tc.required))
			}
			if len(attr.Validators) == 0 {
				t.Fatalf("%s should have validators", tc.attr)
			}

			var resp validator.Int64Response
			for _, v := range attr.Validators {
				v.ValidateInt64(ctx, validator.Int64Request{
					Path:        path.Root(tc.attr),
					ConfigValue: tc.value,
				}, &resp)
			}
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRowsDataSourceUUIDValidators(t *testing.T) {
	ctx := context.Background()

	for _, attrName := range []string{"dive_id", "flight_id"} {
		t.Run(attrName, func(t *testing.T) {
			attr, ok := rowAttribute(attrName, true).(schema.StringAttribute)
			if !ok {
				t.Fatalf("%s attribute = %T, want schema.StringAttribute", attrName, rowAttribute(attrName, true))
			}
			if len(attr.Validators) == 0 {
				t.Fatalf("%s should have UUID validators", attrName)
			}

			tests := map[string]struct {
				value   types.String
				wantErr bool
			}{
				"uuid":                {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000"), wantErr: false},
				"leading whitespace":  {value: types.StringValue(" 123e4567-e89b-42d3-a456-426614174000"), wantErr: true},
				"trailing whitespace": {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000 "), wantErr: true},
				"bad":                 {value: types.StringValue("not-a-uuid"), wantErr: true},
				"unknown":             {value: types.StringUnknown(), wantErr: false},
			}
			for name, tc := range tests {
				t.Run(name, func(t *testing.T) {
					var resp validator.StringResponse
					for _, v := range attr.Validators {
						v.ValidateString(ctx, validator.StringRequest{
							Path:        path.Root(attrName),
							ConfigValue: tc.value,
						}, &resp)
					}
					if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
						t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
					}
				})
			}
		})
	}
}

func TestRowsSpecFlightLogsRequiresRunNumber(t *testing.T) {
	spec := findSpec(t, "flight_logs")
	_, err := spec.build(rowsModel{FlightID: types.StringValue("flight-id")})
	if err == nil || !strings.Contains(err.Error(), "flight_id and run_number are required") {
		t.Fatalf("expected flight log requirement error, got %v", err)
	}
}

func findSpec(t *testing.T, name string) rowSpec {
	t.Helper()
	for _, spec := range rowSpecs() {
		if spec.name == name {
			return spec
		}
	}
	t.Fatalf("missing spec %s", name)
	return rowSpec{}
}

func sorted(values []string) []string {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	return values
}

type fakeFunctionClient struct {
	available bool
	err       error
}

func (f fakeFunctionClient) Exists(context.Context, string, ...any) (bool, error) {
	return f.available, f.err
}

type fakeRowsClient struct {
	rowsJSON string
	err      error
}

func (f fakeRowsClient) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return f.rowsJSON, f.err
}
