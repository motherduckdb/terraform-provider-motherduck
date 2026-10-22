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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const testUUID = "123e4567-e89b-42d3-a456-426614174000"

func int64AttrRejects(t *testing.T, specName, attrName string, value int64) bool {
	t.Helper()
	var resp datasource.SchemaResponse
	(&rowsDataSource{spec: findSpec(t, specName)}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes[attrName].(schema.Int64Attribute)
	if !ok {
		t.Fatalf("%s.%s is not an Int64 attribute", specName, attrName)
	}
	var diags diag.Diagnostics
	for _, v := range attr.Validators {
		var vresp validator.Int64Response
		v.ValidateInt64(t.Context(), validator.Int64Request{Path: path.Root(attrName), ConfigValue: types.Int64Value(value)}, &vresp)
		diags.Append(vresp.Diagnostics...)
	}
	return diags.HasError()
}

func buildSpec(t *testing.T, name string, model rowsModel) string {
	t.Helper()
	query, err := findSpec(t, name).build(model)
	if err != nil {
		t.Fatalf("%s build: %v", name, err)
	}
	return query
}

// rowsConfig builds a configuration for a rows data source with every
// attribute null except values.
func rowsConfig(t *testing.T, spec rowSpec, values map[string]tftypes.Value) tfsdk.Config {
	t.Helper()
	var resp datasource.SchemaResponse
	(&rowsDataSource{spec: spec}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	objectType, ok := resp.Schema.Type().TerraformType(t.Context()).(tftypes.Object)
	if !ok {
		t.Fatal("schema type is not an object")
	}
	all := map[string]tftypes.Value{}
	for name, attrType := range objectType.AttributeTypes {
		all[name] = tftypes.NewValue(attrType, nil)
	}
	for name, value := range values {
		all[name] = value
	}
	return tfsdk.Config{Schema: resp.Schema, Raw: tftypes.NewValue(objectType, all)}
}

// Live, md_list_files on the public Azure path
// az://azureopendatastorage.blob.core.windows.net/nyctlc/green returns one
// row, but with "limit" := 1 it returns none. The first page then holds only
// the blob named like the prefix, which the server drops, and an empty page
// ends the listing.
func TestFilesNativeWindowCoversSkippedPrefixEntry(t *testing.T) {
	query := buildSpec(t, "files", rowsModel{Path: types.StringValue("az://azureopendatastorage.blob.core.windows.net/nyctlc/green"), Limit: types.Int64Value(1)})
	if !strings.Contains(query, `"limit" := 2)`) {
		t.Fatalf("files limit = 1 builds %q, want a native window of 2 that covers the dropped prefix entry", query)
	}
}

// MD_LIST_BUCKETS_FOR_SECRET drops buckets outside the secret scope after the
// object store returns a page, so any page can come back empty and end the
// listing. Paging must not stop the listing early.
func TestBucketsForSecretDoesNotPushLimit(t *testing.T) {
	query := buildSpec(t, "buckets_for_secret", rowsModel{SecretName: types.StringValue("s3"), Limit: types.Int64Value(1), Offset: types.Int64Value(3)})
	if strings.Contains(query, `"limit"`) {
		t.Fatalf("buckets_for_secret pushes a native limit: %q", query)
	}
}

// MD_LIST_FILES narrows its remaining window to UINTEGER. Live, a native
// limit of 4294967296 fails with an internal MotherDuck error.
func TestFilesNativeWindowFitsUInteger(t *testing.T) {
	for _, model := range []rowsModel{
		{Path: types.StringValue("s3://bucket/dir/"), Limit: types.Int64Value(4294967295)},
		{Path: types.StringValue("s3://bucket/dir/"), Limit: types.Int64Value(10), Offset: types.Int64Value(4294967290)},
	} {
		if query := buildSpec(t, "files", model); strings.Contains(query, `"limit" :=`) {
			t.Errorf("files pushes a native limit above 4294967295: %q", query)
		}
	}
}

// MotherDuck slices a page of 2*limit runs for a nonzero offset, so a limit
// of 10 at offset 15 returned 5 runs live. The provider reads the newest
// limit plus offset runs and pages in SQL instead.
func TestFlightRunsAppliesOffsetInSQL(t *testing.T) {
	ids := `flight_id := '` + testUUID + `'::UUID)`
	for _, tc := range []struct {
		name  string
		model rowsModel
		want  string
	}{
		{"limit and offset", rowsModel{Limit: types.Int64Value(10), Offset: types.Int64Value(15)}, `SELECT * FROM MD_LIST_FLIGHT_RUNS("LIMIT" := 25, ` + ids + ` ORDER BY run_number DESC LIMIT 10 OFFSET 15`},
		{"offset only", rowsModel{Offset: types.Int64Value(15)}, `SELECT * FROM MD_LIST_FLIGHT_RUNS("LIMIT" := 65, ` + ids + ` ORDER BY run_number DESC OFFSET 15`},
		{"limit only", rowsModel{Limit: types.Int64Value(10)}, `SELECT * FROM MD_LIST_FLIGHT_RUNS("LIMIT" := 10, ` + ids},
		{"zero offset", rowsModel{Limit: types.Int64Value(10), Offset: types.Int64Value(0)}, `SELECT * FROM MD_LIST_FLIGHT_RUNS("LIMIT" := 10, ` + ids},
		{"unpaged", rowsModel{}, `SELECT * FROM MD_LIST_FLIGHT_RUNS(` + ids},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.model.FlightID = types.StringValue(testUUID)
			if got := buildSpec(t, "flight_runs", tc.model); got != tc.want {
				t.Fatalf("query = %q, want %q", got, tc.want)
			}
		})
	}
}

// The window MD_LIST_FLIGHT_RUNS reads must fit UINTEGER. A larger window
// has no fallback, so it fails at plan time.
func TestFlightRunsWindowFailsAtPlanTime(t *testing.T) {
	spec := findSpec(t, "flight_runs")
	str := func(value string) tftypes.Value { return tftypes.NewValue(tftypes.String, value) }
	num := func(value int64) tftypes.Value { return tftypes.NewValue(tftypes.Number, value) }
	unknown := tftypes.NewValue(tftypes.Number, tftypes.UnknownValue)
	for name, tc := range map[string]struct {
		values  map[string]tftypes.Value
		wantErr bool
	}{
		"fits":                  {map[string]tftypes.Value{"limit": num(4294967285), "offset": num(10)}, false},
		"too large":             {map[string]tftypes.Value{"limit": num(4294967285), "offset": num(11)}, true},
		"offset without limit":  {map[string]tftypes.Value{"offset": num(4294967246)}, true},
		"unknown offset":        {map[string]tftypes.Value{"limit": num(4294967295), "offset": unknown}, false},
		"limit without offset":  {map[string]tftypes.Value{"limit": num(4294967295)}, false},
		"offset at the maximum": {map[string]tftypes.Value{"offset": num(4294967245)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			tc.values["flight_id"] = str(testUUID)
			var resp datasource.ValidateConfigResponse
			(&rowsDataSource{spec: spec}).ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: rowsConfig(t, spec, tc.values)}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
		})
	}
	if _, err := spec.build(rowsModel{FlightID: types.StringValue(testUUID), Limit: types.Int64Value(4294967295), Offset: types.Int64Value(1)}); err == nil {
		t.Fatal("build accepts a window above 4294967295")
	}
}

// Live, MD_LIST_FLIGHTS("LIMIT" := 0) returns every Flight,
// MD_LIST_FLIGHT_RUNS("LIMIT" := 0) fails with an internal error, and
// MD_LIST_DIVE_VERSIONS("limit" := 0) fails with "Could not find Dive Or Not
// Authorized". A zero limit must return no rows and never reach the server.
func TestLimitZeroReturnsNoRows(t *testing.T) {
	id := types.StringValue(testUUID)
	for name, model := range map[string]rowsModel{
		"dives":           {Limit: types.Int64Value(0), Offset: types.Int64Value(3)},
		"dive_versions":   {DiveID: id, Limit: types.Int64Value(0), Offset: types.Int64Value(3)},
		"flights":         {Limit: types.Int64Value(0)},
		"flight_versions": {FlightID: id, Limit: types.Int64Value(0)},
		"flight_runs":     {FlightID: id, Limit: types.Int64Value(0), Offset: types.Int64Value(3)},
		"guides":          {Limit: types.Int64Value(0)},
		"guide_versions":  {GuideID: id, Limit: types.Int64Value(0)},
		"flight_logs":     {FlightID: id, RunNumber: types.Int64Value(1), Limit: types.Int64Value(0)},
	} {
		query := buildSpec(t, name, model)
		if !strings.HasSuffix(query, " LIMIT 0") || strings.Contains(strings.ToLower(query), `"limit" := 0`) {
			t.Errorf("%s limit = 0 builds %q, want a SQL LIMIT 0 and no native zero limit", name, query)
		}
	}
}

// MotherDuck fails a Dive version page that holds no versions (mono
// entity_service listDashboardVersions throws NOT_FOUND), so an offset past
// the last version must page in SQL and return an empty list.
func TestDiveVersionsOffsetPastEndIsNotAnError(t *testing.T) {
	query := buildSpec(t, "dive_versions", rowsModel{DiveID: types.StringValue(testUUID), Limit: types.Int64Value(10), Offset: types.Int64Value(2)})
	if strings.Contains(query, `"offset"`) || !strings.HasSuffix(query, " ORDER BY version DESC LIMIT 10 OFFSET 2") {
		t.Fatalf("dive_versions builds %q, want the offset applied in SQL", query)
	}
	// A window above the INTEGER maximum reads every version instead.
	query = buildSpec(t, "dive_versions", rowsModel{DiveID: types.StringValue(testUUID), Limit: types.Int64Value(2147483647), Offset: types.Int64Value(1)})
	if strings.Contains(query, `"limit"`) || !strings.HasSuffix(query, " ORDER BY version DESC LIMIT 2147483647 OFFSET 1") {
		t.Fatalf("dive_versions builds %q, want no native limit above 2147483647", query)
	}
}

// Live, every paged MotherDuck function fails a value above UINTEGER, and the
// Dive functions read limit and offset as INTEGER.
func TestPagingIsCappedToServerTypes(t *testing.T) {
	for name, maximum := range map[string]int64{
		"dives": maxInteger, "dive_versions": maxInteger,
		"flights": maxUInteger, "flight_versions": maxUInteger, "flight_runs": maxUInteger,
		"guides": maxUInteger, "guide_versions": maxUInteger, "flight_logs": maxUInteger,
	} {
		for _, attr := range []string{"limit", "offset"} {
			if int64AttrRejects(t, name, attr, maximum) || int64AttrRejects(t, name, attr, 0) {
				t.Errorf("%s.%s rejects a value in 0..%d", name, attr, maximum)
			}
			if !int64AttrRejects(t, name, attr, maximum+1) {
				t.Errorf("%s.%s accepts %d, which MotherDuck rejects", name, attr, maximum+1)
			}
		}
	}
	// Live, MD_GET_GUIDE(version := 2147483648) fails with an internal error.
	if !int64AttrRejects(t, "guide", "version", 2147483648) || int64AttrRejects(t, "guide", "version", 2147483647) || !int64AttrRejects(t, "guide", "version", 0) {
		t.Error("guide.version must be between 1 and 2147483647")
	}
}

// Live column sets from `DESCRIBE SELECT * FROM ...` on
// v1.5.6-2026-10-484. Every typed row attribute must read a returned column.
func TestTypedRowsMatchLiveColumns(t *testing.T) {
	live := map[string]string{
		"databases":           "name,uuid,created_ts,transient,historical_snapshot_retention,type",
		"attached_databases":  "alias,is_share,is_readonly,is_error_catalog,error_message,is_ducklake",
		"database_snapshots":  "database_name,database_id,snapshot_id,snapshot_name,created_ts,active_bytes,bytes_written,bytes_deleted,user_name,user_id",
		"owned_shares":        "name,url,source_db_name,source_db_uuid,access,visibility,update,created_ts,grants,include_pattern",
		"shared_with_me":      "name,url,owner,visibility,created_ts,updated_ts,update_mode,access",
		"current_user_tokens": "token_name,token_type,created_ts,expire_at",
		"storage_info":        "database_name,database_id,created_ts,deleted_ts,user_name,active_bytes,historical_bytes,retained_for_clone_bytes,failsafe_bytes,transient,historical_snapshot_retention,computed_ts",
		"dives":               "id,title,description,owner_id,current_version,created_at,updated_at,owner_name,status,status_changed_at,status_set_by,status_applies_to_version,required_resources",
		"dive":                "id,title,description,owner_id,current_version,created_at,updated_at,owner_name,status,status_changed_at,status_set_by,status_applies_to_version,version_id,version_storage_url,version_description,version_created_at,version_api_version,version_required_resources,content",
		"dive_versions":       "id,version,storage_url,description,created_at,api_version,required_resources",
		"flights":             "flight_id,flight_name,schedule_cron,schedule_status,status,current_version,created_at,updated_at,owner_name",
		"flight":              "flight_id,flight_name,schedule_cron,schedule_status,status,current_version,created_at,updated_at,owner_name",
		"flight_versions":     "version_id,flight_id,flight_version,created_at,access_token_name,flight_secret_names,config,max_runtime_sec,instance_type",
		"flight_runs":         "run_id,flight_id,flight_name,flight_version,config,run_number,is_scheduled,status,created_at,started_at,ended_at,scheduled_at,cancelled_at,exit_code",
		"flight_logs":         "line_number,reported_at,line",
		"guides":              "id,topic,title,description,owner_id,owner_name,access,current_version,created_at,updated_at",
		"guide":               "id,topic,title,description,owner_id,owner_name,access,current_version,created_at,updated_at,version,version_change_comment,version_external_id,version_created_at,references,content",
		"guide_versions":      "version,change_comment,external_id,created_at",
		"share_grants":        "share_owner,grantee_name,grantee_type,privilege,granted_at",
		"files":               "name,type,size,last_modified",
		"buckets_for_secret":  "name,creation_date,region,arn",
	}
	for name, columns := range live {
		have := map[string]bool{}
		for _, column := range strings.Split(columns, ",") {
			have[column] = true
		}
		for _, attr := range findSpec(t, name).typedRows {
			source := attr.source
			if source == "" {
				source = attr.name
			}
			if !have[source] && attr.deprecation == "" {
				t.Errorf("%s.rows[*].%s reads column %q, which MotherDuck does not return, so it is always null", name, attr.name, source)
			}
		}
	}
	for _, attr := range findSpec(t, "dive").typedRows {
		if attr.name == "required_resources" && !attr.sensitive {
			t.Error("dive.rows[*].required_resources must stay sensitive")
		}
	}
}

// A Guide reference filter can carry a share URL, so it is sensitive and
// diagnostics never echo it.
func TestGuidesReferenceURLStaysOutOfOutput(t *testing.T) {
	var resp datasource.SchemaResponse
	spec := findSpec(t, "guides")
	(&rowsDataSource{spec: spec}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	if attr := resp.Schema.Attributes["reference_url"].(schema.StringAttribute); !attr.Sensitive || !attr.Optional {
		t.Error("motherduck_guides.reference_url must be optional and sensitive")
	}
	shareID := "0a9a026ec5a55946a9de39851087ed81"
	query := buildSpec(t, "guides", rowsModel{ReferenceType: types.StringValue("catalog"), ReferenceURL: types.StringValue("md:_share/tenant/" + shareID)})
	for name, err := range map[string]error{
		"unsupported": errors.New("Not implemented Error: reference filters"),
		"generic":     errors.New("Catalog Error: Share 'md:_share/tenant/" + shareID + "' not found"),
	} {
		var diags diag.Diagnostics
		(&rowsDataSource{spec: spec}).queryRows(context.Background(), fakeRowsClient{err: err}, query, &diags)
		if !diags.HasError() {
			t.Fatalf("%s: want an error", name)
		}
		if detail := diags[0].Detail(); strings.Contains(detail, shareID) || !strings.Contains(detail, redactedShareURL) {
			t.Errorf("%s: diagnostic echoes the share URL: %s", name, detail)
		}
	}
}

// The generic unsupported-command text must not hide what MotherDuck said.
func TestUnsupportedCommandKeepsServerMessage(t *testing.T) {
	var diags diag.Diagnostics
	err := errors.New("Binder Error: Unsupported named parameter include_org_shares for md_list_dives")
	(&rowsDataSource{spec: findSpec(t, "dives")}).queryRows(context.Background(), fakeRowsClient{err: err}, "SELECT * FROM MD_LIST_DIVES(include_org_shares := true)", &diags)
	if !diags.HasError() || diags[0].Summary() != "MotherDuck SQL command unavailable" {
		t.Fatalf("diagnostics = %v", diags)
	}
	if !strings.Contains(diags[0].Detail(), "Unsupported named parameter include_org_shares") {
		t.Fatalf("detail = %q, want the MotherDuck message", diags[0].Detail())
	}
}

// MotherDuck reports a canonical spelling of a share name. A configured name
// keeps its configured value, the same way a configured URL does.
func TestShareDataSourceKeepsConfiguredName(t *testing.T) {
	share := shareDescription{}
	share.Name.String, share.Name.Valid = "tenant", true
	share.URL.String, share.URL.Valid = "md:_share/tenant/abc", true
	model := shareModel{Name: types.StringValue("Tenant")}
	setShareModel(&model, share)
	if !model.Name.Equal(types.StringValue("Tenant")) || !model.URL.Equal(types.StringValue("md:_share/tenant/abc")) {
		t.Fatalf("name = %v, url = %v, want the configured name and the reported URL", model.Name, model.URL)
	}
	model = shareModel{URL: types.StringValue("MD:_SHARE/tenant/abc")}
	setShareModel(&model, share)
	if !model.URL.Equal(types.StringValue("MD:_SHARE/tenant/abc")) || !model.Name.Equal(types.StringValue("tenant")) {
		t.Fatalf("name = %v, url = %v, want the reported name and the configured URL", model.Name, model.URL)
	}
}

// A share name can contain a quote, so redaction covers the whole URL token
// or quoted literal instead of stopping at the quote.
func TestRedactShareURLWithQuoteInName(t *testing.T) {
	shareID := "0a9a026ec5a55946a9de39851087ed81"
	for name, tc := range map[string]struct{ message, want string }{
		"quoted literal":    {"Catalog Error: Share: 'md:_share/o'brien/" + shareID + "' not found.", "Catalog Error: Share: '<redacted share URL>' not found."},
		"doubled quote":     {"SELECT * FROM MD_LIST_GUIDES(reference := {'url': 'md:_share/o''brien/" + shareID + "'})", "SELECT * FROM MD_LIST_GUIDES(reference := {'url': '<redacted share URL>'})"},
		"literal at end":    {"could not resolve 'md:_share/o'brien/" + shareID + "'", "could not resolve '<redacted share URL>'"},
		"unquoted token":    {"could not resolve md:_share/o'brien/" + shareID + ".", "could not resolve <redacted share URL>."},
		"two literals":      {"'md:_share/a/" + shareID + "' and 'md:_share/b'c/" + shareID + "'", "'<redacted share URL>' and '<redacted share URL>'"},
		"double quoted":     {`share "md:_share/o'brien/` + shareID + `" missing`, `share "<redacted share URL>" missing`},
		"unterminated":      {"share 'md:_share/o'brien/" + shareID, "share '<redacted share URL>"},
		"no share url":      {"Share 'o'brien' not found", "Share 'o'brien' not found"},
		"encoded with tick": {"GET md%3A_share%2Fo'brien%2F" + shareID + " failed", "GET <redacted share URL> failed"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := redactShareURL(tc.message, types.StringNull()); got != tc.want {
				t.Fatalf("redactShareURL(%q) = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}
