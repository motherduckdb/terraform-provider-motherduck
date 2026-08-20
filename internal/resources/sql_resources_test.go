package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestColumnDDLSortsAndQuotes(t *testing.T) {
	got := columnDDL(map[string]string{"b": "VARCHAR", `a.name`: "INTEGER", `a"name`: "BOOLEAN"})
	want := `"a""name" BOOLEAN, "a.name" INTEGER, "b" VARCHAR`
	if got != want {
		t.Fatalf("columnDDL() = %q, want %q", got, want)
	}
}

func TestResourceSchemasHaveVersionsAndAttributeDescriptions(t *testing.T) {
	for _, factory := range All() {
		res := factory()
		var metadata resource.MetadataResponse
		res.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "motherduck"}, &metadata)

		var schemaResp resource.SchemaResponse
		res.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
		if schemaResp.Diagnostics.HasError() {
			t.Fatalf("%s schema diagnostics: %v", metadata.TypeName, schemaResp.Diagnostics)
		}
		if schemaResp.Schema.Version != 1 {
			t.Fatalf("%s schema version = %d, want 1", metadata.TypeName, schemaResp.Schema.Version)
		}
		for name, attr := range schemaResp.Schema.Attributes {
			assertAttributeDescription(t, metadata.TypeName+"."+name, attr)
		}
	}
}

func assertAttributeDescription(t *testing.T, name string, attr resourceschema.Attribute) {
	t.Helper()
	if strings.TrimSpace(attr.GetMarkdownDescription()) == "" && strings.TrimSpace(attr.GetDescription()) == "" {
		t.Errorf("%s has an empty description", name)
	}
	switch nested := attr.(type) {
	case resourceschema.ListNestedAttribute:
		for nestedName, nestedAttr := range nested.NestedObject.Attributes {
			assertAttributeDescription(t, name+"."+nestedName, nestedAttr)
		}
	case resourceschema.SetNestedAttribute:
		for nestedName, nestedAttr := range nested.NestedObject.Attributes {
			assertAttributeDescription(t, name+"."+nestedName, nestedAttr)
		}
	case resourceschema.SingleNestedAttribute:
		for nestedName, nestedAttr := range nested.Attributes {
			assertAttributeDescription(t, name+"."+nestedName, nestedAttr)
		}
	}
}

func TestDatabaseSnapshotRetentionUsesStateForUnknown(t *testing.T) {
	var resp resource.SchemaResponse
	NewDatabaseResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["snapshot_retention_days"].(resourceschema.Int64Attribute)
	if !ok {
		t.Fatalf("snapshot_retention_days attribute = %T, want schema.Int64Attribute", resp.Schema.Attributes["snapshot_retention_days"])
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("snapshot_retention_days should keep prior state for unknown Optional+Computed plans")
	}
}

func TestValidateDatabaseConfigDefersUnknownDatabaseType(t *testing.T) {
	model := databaseModel{
		DatabaseType: types.StringUnknown(),
		DataPath:     types.StringValue("s3://example-bucket/ducklake"),
		Encrypted:    types.BoolValue(true),
		Transient:    types.BoolValue(true),
	}
	var diags diag.Diagnostics
	validateDatabaseConfig(model, &diags)
	if diags.HasError() {
		t.Fatalf("unknown database_type should defer cross-field validation: %v", diags)
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

func TestValidateSecretConfig(t *testing.T) {
	ctx := context.Background()
	tests := map[string]struct {
		params    map[string]string
		secretSQL types.String
		wantErr   bool
	}{
		"valid params": {
			params:    map[string]string{"key_id": "abc", "region": "us-east-1"},
			secretSQL: types.StringValue("URL_STYLE 'path'"),
			wantErr:   false,
		},
		"bad param key": {
			params:    map[string]string{"key-id": "abc"},
			secretSQL: types.StringNull(),
			wantErr:   true,
		},
		"raw semicolon": {
			params:    map[string]string{},
			secretSQL: types.StringValue("URL_STYLE 'path'; DROP SECRET other"),
			wantErr:   true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			params, valueDiags := types.MapValueFrom(ctx, types.StringType, tc.params)
			if valueDiags.HasError() {
				t.Fatalf("building params map: %v", valueDiags)
			}
			model := secretModel{Params: params, SecretSQL: tc.secretSQL}
			var diags diag.Diagnostics
			validateSecretConfig(model, &diags)
			if gotErr := diags.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, diags)
			}
		})
	}
}

func TestIntervalDays(t *testing.T) {
	tests := map[string]types.Int64{
		"7 days":     types.Int64Value(7),
		"00:00:00":   types.Int64Value(0),
		"unparsable": types.Int64Null(),
	}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			got := intervalDays(value)
			if !got.Equal(want) {
				t.Fatalf("intervalDays() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestApplyDatabaseRow(t *testing.T) {
	tests := map[string]struct {
		model         databaseModel
		uuid          sql.NullString
		createdTS     sql.NullString
		dbType        sql.NullString
		transient     sql.NullBool
		retention     sql.NullString
		wantTransient types.Bool
		wantRetention types.Int64
		wantType      types.String
	}{
		"null transient and retention become known nulls": {
			model: databaseModel{
				Name:                  types.StringValue("ducklake_db"),
				Transient:             types.BoolUnknown(),
				SnapshotRetentionDays: types.Int64Unknown(),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DUCKLAKE"),
			transient:     sql.NullBool{},
			retention:     sqlNullStringInvalid(),
			wantTransient: types.BoolNull(),
			wantRetention: types.Int64Null(),
			wantType:      types.StringValue("ducklake"),
		},
		"stale state values are overwritten by null live values": {
			model: databaseModel{
				Name:                  types.StringValue("db"),
				Transient:             types.BoolValue(true),
				SnapshotRetentionDays: types.Int64Value(5),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DEFAULT"),
			transient:     sql.NullBool{},
			retention:     sqlNullStringInvalid(),
			wantTransient: types.BoolNull(),
			wantRetention: types.Int64Null(),
			wantType:      types.StringValue("default"),
		},
		"valid values are mapped": {
			model: databaseModel{
				Name: types.StringValue("db"),
			},
			uuid:          sqlNullString("uuid"),
			createdTS:     sqlNullString("2026-01-01 00:00:00"),
			dbType:        sqlNullString("DEFAULT"),
			transient:     sql.NullBool{Bool: true, Valid: true},
			retention:     sqlNullString("7 days"),
			wantTransient: types.BoolValue(true),
			wantRetention: types.Int64Value(7),
			wantType:      types.StringValue("default"),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			applyDatabaseRow(&tc.model, tc.uuid, tc.createdTS, tc.dbType, tc.transient, tc.retention)
			if got, want := tc.model.ID, types.StringValue(tc.model.Name.ValueString()); !got.Equal(want) {
				t.Fatalf("id = %#v, want %#v", got, want)
			}
			if !tc.model.Transient.Equal(tc.wantTransient) {
				t.Fatalf("transient = %#v, want %#v", tc.model.Transient, tc.wantTransient)
			}
			if !tc.model.SnapshotRetentionDays.Equal(tc.wantRetention) {
				t.Fatalf("snapshot_retention_days = %#v, want %#v", tc.model.SnapshotRetentionDays, tc.wantRetention)
			}
			if !tc.model.DatabaseType.Equal(tc.wantType) {
				t.Fatalf("database_type = %#v, want %#v", tc.model.DatabaseType, tc.wantType)
			}
			if got, want := tc.model.UUID, types.StringValue("uuid"); !got.Equal(want) {
				t.Fatalf("uuid = %#v, want %#v", got, want)
			}
		})
	}
}

func TestLowerNullString(t *testing.T) {
	got := lowerNullString(sqlNullString("AUTOMATIC"))
	want := types.StringValue("automatic")
	if !got.Equal(want) {
		t.Fatalf("lowerNullString() = %#v, want %#v", got, want)
	}
	if got := lowerNullString(sqlNullStringInvalid()); !got.IsNull() {
		t.Fatalf("invalid lowerNullString() = %#v, want null", got)
	}
}

func TestPrepareShareCreateState(t *testing.T) {
	model := &shareModel{
		Name:      types.StringValue("share_name"),
		ID:        types.StringUnknown(),
		URL:       types.StringUnknown(),
		CreatedTS: types.StringUnknown(),
	}

	prepareShareCreateState(model)

	if got, want := model.ID.ValueString(), "share_name"; got != want {
		t.Fatalf("share id = %q, want %q", got, want)
	}
	if !model.URL.IsNull() {
		t.Fatalf("share URL should be known null before catalog read, got %#v", model.URL)
	}
	if !model.CreatedTS.IsNull() {
		t.Fatalf("share created_ts should be known null before catalog read, got %#v", model.CreatedTS)
	}
}

func TestApplyOwnedShare(t *testing.T) {
	ctx := context.Background()
	liveDefaults := sqlcatalog.OwnedShare{
		URL:            sqlNullString("md:_share/share_name/uuid"),
		SourceDatabase: sqlNullString("source_db"),
		Access:         sqlNullString("ORGANIZATION"),
		Visibility:     sqlNullString("DISCOVERABLE"),
		UpdateMode:     sqlNullString("AUTOMATIC"),
		IncludePattern: sqlNullStringInvalid(),
		CreatedTS:      sqlNullString("2026-01-01 00:00:00"),
	}
	tests := map[string]struct {
		model shareModel
		share sqlcatalog.OwnedShare
		want  shareModel
	}{
		"omitted options stay null despite server defaults": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringNull(),
				Visibility: types.StringNull(),
				UpdateMode: types.StringNull(),
			},
			share: liveDefaults,
			want: shareModel{
				Access:     types.StringNull(),
				Visibility: types.StringNull(),
				UpdateMode: types.StringNull(),
			},
		},
		"configured options refresh lowercased from live": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
			share: sqlcatalog.OwnedShare{
				URL:            liveDefaults.URL,
				SourceDatabase: liveDefaults.SourceDatabase,
				Access:         sqlNullString("RESTRICTED"),
				Visibility:     sqlNullString("HIDDEN"),
				UpdateMode:     sqlNullString("MANUAL"),
				CreatedTS:      liveDefaults.CreatedTS,
			},
			want: shareModel{
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
		},
		"configured options surface live drift": {
			model: shareModel{
				Name:       types.StringValue("share_name"),
				Access:     types.StringValue("restricted"),
				Visibility: types.StringValue("hidden"),
				UpdateMode: types.StringValue("manual"),
			},
			share: liveDefaults,
			want: shareModel{
				Access:     types.StringValue("organization"),
				Visibility: types.StringValue("discoverable"),
				UpdateMode: types.StringValue("automatic"),
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			applyOwnedShare(ctx, &tc.model, tc.share, &diags)
			if diags.HasError() {
				t.Fatalf("applyOwnedShare diagnostics: %v", diags)
			}
			if got, want := tc.model.ID, types.StringValue("share_name"); !got.Equal(want) {
				t.Fatalf("id = %#v, want %#v", got, want)
			}
			if !tc.model.Access.Equal(tc.want.Access) {
				t.Fatalf("access = %#v, want %#v", tc.model.Access, tc.want.Access)
			}
			if !tc.model.Visibility.Equal(tc.want.Visibility) {
				t.Fatalf("visibility = %#v, want %#v", tc.model.Visibility, tc.want.Visibility)
			}
			if !tc.model.UpdateMode.Equal(tc.want.UpdateMode) {
				t.Fatalf("update_mode = %#v, want %#v", tc.model.UpdateMode, tc.want.UpdateMode)
			}
		})
	}
}

func TestApplyOwnedShareImportThenConfiguredRoundTrip(t *testing.T) {
	ctx := context.Background()
	live := sqlcatalog.OwnedShare{
		URL:            sqlNullString("md:_share/share_name/uuid"),
		SourceDatabase: sqlNullString("source_db"),
		Access:         sqlNullString("RESTRICTED"),
		Visibility:     sqlNullString("HIDDEN"),
		UpdateMode:     sqlNullString("MANUAL"),
		CreatedTS:      sqlNullString("2026-01-01 00:00:00"),
	}

	// Import: only name is known, so config-owned options stay null.
	imported := shareModel{Name: types.StringValue("share_name")}
	var diags diag.Diagnostics
	applyOwnedShare(ctx, &imported, live, &diags)
	if diags.HasError() {
		t.Fatalf("import applyOwnedShare diagnostics: %v", diags)
	}
	if !imported.Access.IsNull() || !imported.Visibility.IsNull() || !imported.UpdateMode.IsNull() {
		t.Fatalf("imported options should stay null, got %#v %#v %#v", imported.Access, imported.Visibility, imported.UpdateMode)
	}
	if got, want := imported.SourceDatabase, types.StringValue("source_db"); !got.Equal(want) {
		t.Fatalf("source_database = %#v, want %#v", got, want)
	}

	// Follow-up apply with the options configured refreshes them from live.
	configured := imported
	configured.Access = types.StringValue("restricted")
	configured.Visibility = types.StringValue("hidden")
	configured.UpdateMode = types.StringValue("manual")
	applyOwnedShare(ctx, &configured, live, &diags)
	if diags.HasError() {
		t.Fatalf("configured applyOwnedShare diagnostics: %v", diags)
	}
	if got, want := configured.Access, types.StringValue("restricted"); !got.Equal(want) {
		t.Fatalf("access = %#v, want %#v", got, want)
	}
	if got, want := configured.Visibility, types.StringValue("hidden"); !got.Equal(want) {
		t.Fatalf("visibility = %#v, want %#v", got, want)
	}
	if got, want := configured.UpdateMode, types.StringValue("manual"); !got.Equal(want) {
		t.Fatalf("update_mode = %#v, want %#v", got, want)
	}
}

func TestShareURLIsSensitive(t *testing.T) {
	var resp resource.SchemaResponse
	NewShareResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["url"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("url attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["url"])
	}
	if !attr.Sensitive {
		t.Fatal("share url must be sensitive because unrestricted share URLs can grant access")
	}
}

func TestShareIncludePatternSQL(t *testing.T) {
	value, valueDiags := types.ListValueFrom(context.Background(), types.StringType, []string{"main.reporting_*", `main."dim,region"`})
	if valueDiags.HasError() {
		t.Fatalf("list diagnostics: %v", valueDiags)
	}
	var diags diag.Diagnostics
	got, ok := shareIncludePattern(context.Background(), value, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("shareIncludePattern diagnostics: %v", diags)
	}
	if got != `main.reporting_*,main."dim,region"` {
		t.Fatalf("include pattern = %q", got)
	}
}

func TestValidateShareIncludePatternLength(t *testing.T) {
	valid := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("main.*"),
	})
	var validDiags diag.Diagnostics
	validateShareIncludePattern(valid, &validDiags)
	if validDiags.HasError() {
		t.Fatalf("valid include pattern diagnostics: %v", validDiags)
	}

	tooLong := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue(strings.Repeat("x", 16385)),
	})
	var invalidDiags diag.Diagnostics
	validateShareIncludePattern(tooLong, &invalidDiags)
	if !invalidDiags.HasError() {
		t.Fatal("expected oversized include pattern diagnostics")
	}
}

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
		"other errors surface as-is":  {exists: false, err: other, wantRemove: false, wantErr: other},
		"error wins over stale exist": {exists: true, err: other, wantRemove: false, wantErr: other},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			remove, err := shareGrantReadDecision(tc.exists, tc.err)
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

func TestPrepareSnapshotCreateState(t *testing.T) {
	model := &snapshotModel{
		ID:        types.StringUnknown(),
		CreatedTS: types.StringUnknown(),
	}

	prepareSnapshotCreateState(model)

	if !model.ID.IsNull() {
		t.Fatalf("snapshot id should be known null before catalog read, got %#v", model.ID)
	}
	if !model.CreatedTS.IsNull() {
		t.Fatalf("snapshot created_ts should be known null before catalog read, got %#v", model.CreatedTS)
	}
}

type fakePrivateState struct {
	data map[string][]byte
}

func (f *fakePrivateState) GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics) {
	return f.data[key], nil
}

func (f *fakePrivateState) SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics {
	if f.data == nil {
		f.data = map[string][]byte{}
	}
	if len(value) == 0 {
		delete(f.data, key)
		return nil
	}
	f.data[key] = value
	return nil
}

func TestViewServerDefinitionPrivateStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	private := &fakePrivateState{}
	var diags diag.Diagnostics

	storeViewServerDefinition(ctx, private, `CREATE VIEW app.v AS SELECT count(*) FROM app.facts`, &diags)
	if diags.HasError() {
		t.Fatalf("store diagnostics: %v", diags)
	}
	got, ok := loadViewServerDefinition(ctx, private, &diags)
	if diags.HasError() {
		t.Fatalf("load diagnostics: %v", diags)
	}
	if !ok {
		t.Fatal("expected private definition")
	}
	want := `CREATE VIEW app.v AS SELECT count(*) FROM app.facts`
	if got != want {
		t.Fatalf("private definition = %q, want %q", got, want)
	}
}

func TestSchemaDropMode(t *testing.T) {
	if got := schemaDropMode(schemaModel{CascadeOnDelete: types.BoolValue(true)}); got != " CASCADE" {
		t.Fatalf("schemaDropMode(true) = %q, want CASCADE", got)
	}
	if got := schemaDropMode(schemaModel{CascadeOnDelete: types.BoolValue(false)}); got != "" {
		t.Fatalf("schemaDropMode(false) = %q, want empty", got)
	}
	if got := schemaDropMode(schemaModel{CascadeOnDelete: types.BoolNull()}); got != "" {
		t.Fatalf("schemaDropMode(null) = %q, want empty", got)
	}
}

func TestIsNotFoundNil(t *testing.T) {
	if isNotFound(nil) {
		t.Fatal("nil error should not be treated as not found")
	}
}

func TestIsNotFoundUsesTypedErrors(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"rest entity 404": {err: &mdrest.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "entity not found"}, want: true},
		"rest route 404":  {err: &mdrest.APIError{StatusCode: 404, Body: "Not Found"}, want: false},
		"rest 500":        {err: &mdrest.APIError{StatusCode: 500, Code: "INTERNAL", Message: "boom"}, want: false},
		"catalog missing": {err: &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table with name facts does not exist!"}, want: true},
		"binder mention":  {err: &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: "Binder Error: referenced column not found in FROM clause"}, want: false},
		"network mention": {err: errors.New("host not found"), want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isNotFound(tc.err); got != tc.want {
				t.Fatalf("isNotFound() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestSQLIdentifierValidator(t *testing.T) {
	ctx := context.Background()
	v := sqlIdentifierValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"simple":              {value: types.StringValue("tf_database"), wantErr: false},
		"internal space":      {value: types.StringValue("tf database"), wantErr: false},
		"quoted":              {value: types.StringValue(`tf"database`), wantErr: false},
		"blank":               {value: types.StringValue("  "), wantErr: true},
		"leading whitespace":  {value: types.StringValue(" tf_database"), wantErr: true},
		"trailing whitespace": {value: types.StringValue("tf_database "), wantErr: true},
		"dotted":              {value: types.StringValue("db.schema"), wantErr: true},
		"unknown":             {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("name"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
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

func TestSplitImportID(t *testing.T) {
	tests := map[string]struct {
		id        string
		sep       string
		wantParts int
		wantErr   bool
	}{
		"valid dot":      {id: "db.schema.table", sep: ".", wantParts: 3, wantErr: false},
		"valid space":    {id: "tenant db.app schema.table name", sep: ".", wantParts: 3, wantErr: false},
		"too few":        {id: "db.schema", sep: ".", wantParts: 3, wantErr: true},
		"too many":       {id: "db.schema.table.extra", sep: ".", wantParts: 3, wantErr: true},
		"empty middle":   {id: "db..table", sep: ".", wantParts: 3, wantErr: true},
		"blank middle":   {id: "db. .table", sep: ".", wantParts: 3, wantErr: true},
		"empty trailing": {id: "share/", sep: "/", wantParts: 2, wantErr: true},
		"empty leading":  {id: "/user@example.com", sep: "/", wantParts: 2, wantErr: true},
		"valid slash":    {id: "share/user@example.com", sep: "/", wantParts: 2, wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			_, ok := splitImportID(tc.id, tc.sep, tc.wantParts, "`example`", &diags)
			if gotErr := diags.HasError() || !ok; gotErr != tc.wantErr {
				t.Fatalf("splitImportID error = %t, want %t: %v", gotErr, tc.wantErr, diags)
			}
		})
	}
}

func TestSplitSQLImportID(t *testing.T) {
	tests := map[string]struct {
		id     string
		wantOK bool
	}{
		"valid":               {id: "db.schema.table", wantOK: true},
		"spaces inside":       {id: "tenant db.app schema.table name", wantOK: true},
		"leading whitespace":  {id: " db.schema.table", wantOK: false},
		"trailing whitespace": {id: "db.schema.table ", wantOK: false},
		"empty middle":        {id: "db..table", wantOK: false},
		"too many":            {id: "db.schema.table.extra", wantOK: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			_, ok := splitSQLImportID(tc.id, ".", 3, "`<database>.<schema>.<name>`", &diags)
			if ok != tc.wantOK {
				t.Fatalf("splitSQLImportID ok = %t, want %t: %v", ok, tc.wantOK, diags)
			}
			if diags.HasError() == tc.wantOK {
				t.Fatalf("diagnostics error = %t, want %t: %v", diags.HasError(), !tc.wantOK, diags)
			}
		})
	}
}

func TestSingleSQLImportRejectsInvalidNames(t *testing.T) {
	tests := map[string]resource.Resource{
		"database": NewDatabaseResource(),
		"secret":   NewSecretResource(),
		"share":    NewShareResource(),
	}
	for name, res := range tests {
		t.Run(name, func(t *testing.T) {
			for _, id := range []string{"bad.name", " bad_name", "bad_name ", " "} {
				var resp resource.ImportStateResponse
				res.(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatalf("expected import diagnostics for %q", id)
				}
			}
		})
	}
}

func TestImportThreePartIDRejectsEmptySegments(t *testing.T) {
	var resp resource.ImportStateResponse
	importThreePartID(context.Background(), "db..table", &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import diagnostics for empty schema segment")
	}
}

func TestDesiredSecretScopeFromParams(t *testing.T) {
	ctx := context.Background()
	params, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"SCOPE": "s3://bucket/path/with'quote/",
	})
	if valueDiags.HasError() {
		t.Fatalf("building params map: %v", valueDiags)
	}
	got, ok := desiredSecretScopeFromParams(params)
	if !ok {
		t.Fatal("expected scope to be detected")
	}
	want := "['s3://bucket/path/with''quote/']"
	if got != want {
		t.Fatalf("desired scope = %q, want %q", got, want)
	}

	withoutScope, valueDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"region": "us-east-1",
	})
	if valueDiags.HasError() {
		t.Fatalf("building params map: %v", valueDiags)
	}
	if got, ok := desiredSecretScopeFromParams(withoutScope); ok || got != "" {
		t.Fatalf("expected no desired scope, got %q", got)
	}
}

func TestDatabaseTypeValidator(t *testing.T) {
	ctx := context.Background()
	v := databaseTypeValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"default":   {value: types.StringValue("default"), wantErr: false},
		"case":      {value: types.StringValue("DUCKLAKE"), wantErr: true},
		"transient": {value: types.StringValue("transient"), wantErr: true},
		"blank":     {value: types.StringValue("  "), wantErr: true},
		"unknown":   {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("database_type"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestSQLBareWordValidator(t *testing.T) {
	ctx := context.Background()
	v := sqlBareWordValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"s3":           {value: types.StringValue("s3"), wantErr: false},
		"case":         {value: types.StringValue("S3"), wantErr: true},
		"underscore":   {value: types.StringValue("credential_chain"), wantErr: false},
		"starts digit": {value: types.StringValue("3s"), wantErr: true},
		"hyphen":       {value: types.StringValue("key-id"), wantErr: true},
		"semicolon":    {value: types.StringValue("s3; DROP SECRET x"), wantErr: true},
		"unknown":      {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("type"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestStringEnumValidator(t *testing.T) {
	ctx := context.Background()
	v := stringEnumValidator{name: "test", values: []string{"organization", "restricted", "unrestricted"}}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"default": {value: types.StringValue("organization"), wantErr: false},
		"lower":   {value: types.StringValue("restricted"), wantErr: false},
		"case":    {value: types.StringValue("UNRESTRICTED"), wantErr: true},
		"invalid": {value: types.StringValue("public"), wantErr: true},
		"unknown": {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("access"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestViewQueryFromDefinition(t *testing.T) {
	got := viewQueryFromDefinition(`CREATE VIEW app.facts_v AS SELECT id, "label" FROM db.app.facts;`)
	want := `SELECT id, "label" FROM db.app.facts`
	if got != want {
		t.Fatalf("viewQueryFromDefinition() = %q, want %q", got, want)
	}

	if got := viewQueryFromDefinition("SELECT 1;"); got != "SELECT 1" {
		t.Fatalf("plain query = %q, want SELECT 1", got)
	}
}

func TestSQLFunctionAvailableDiagnostics(t *testing.T) {
	resource := &baseResource{}

	var missingDiags diag.Diagnostics
	if resource.sqlFunctionAvailable(context.Background(), fakeSQLFunctionClient{available: false}, &missingDiags, "md_create_dive", "motherduck_dive") {
		t.Fatal("missing SQL function should not be available")
	}
	if !missingDiags.HasError() || !strings.Contains(missingDiags[0].Detail(), "md_create_dive") || !strings.Contains(missingDiags[0].Detail(), "motherduck_dive") {
		t.Fatalf("expected missing function diagnostic, got %v", missingDiags)
	}

	var errDiags diag.Diagnostics
	if resource.sqlFunctionAvailable(context.Background(), fakeSQLFunctionClient{err: errors.New("boom")}, &errDiags, "md_create_dive", "motherduck_dive") {
		t.Fatal("function inspection error should not be available")
	}
	if !errDiags.HasError() || !strings.Contains(errDiags[0].Summary(), "inspect") {
		t.Fatalf("expected inspection diagnostic, got %v", errDiags)
	}
}

func TestDiveMetadataArgs(t *testing.T) {
	tests := map[string]struct {
		plan    *diveModel
		state   *diveModel
		want    map[string]string
		wantRun bool
	}{
		"no changes": {
			plan:    &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("Published")},
			state:   &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("Published")},
			want:    map[string]string{},
			wantRun: false,
		},
		"title and description": {
			plan:    &diveModel{Title: types.StringValue("Revenue v2"), Description: types.StringValue("Updated")},
			state:   &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("Published")},
			want:    map[string]string{"title": "'Revenue v2'", "description": "'Updated'"},
			wantRun: true,
		},
		"empty description": {
			plan:    &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("")},
			state:   &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("Published")},
			want:    map[string]string{"description": "''"},
			wantRun: true,
		},
		"omitted unmanaged description": {
			plan:    &diveModel{Title: types.StringValue("Revenue"), Description: types.StringNull()},
			state:   &diveModel{Title: types.StringValue("Revenue"), Description: types.StringNull()},
			want:    map[string]string{},
			wantRun: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			got, gotRun := diveMetadataArgs(tc.plan, tc.state, &diags)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if gotRun != tc.wantRun {
				t.Fatalf("update = %t, want %t", gotRun, tc.wantRun)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("diveMetadataArgs() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDiveMetadataArgsRejectsDescriptionClear(t *testing.T) {
	plan := &diveModel{Title: types.StringValue("Revenue"), Description: types.StringNull()}
	state := &diveModel{Title: types.StringValue("Revenue"), Description: types.StringValue("Published")}

	var diags diag.Diagnostics
	if _, ok := diveMetadataArgs(plan, state, &diags); ok {
		t.Fatal("expected description removal to fail")
	}
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), `description = ""`) {
		t.Fatalf("expected empty-string clear diagnostic, got %v", diags)
	}
}

func TestDiveContentArgs(t *testing.T) {
	ctx := context.Background()
	unchangedPlan := &diveModel{Content: types.StringValue("export default null"), APIVersion: types.Int64Value(1)}
	unchangedState := &diveModel{Content: types.StringValue("export default null"), APIVersion: types.Int64Value(1)}
	var diags diag.Diagnostics
	if got, ok := diveContentArgs(ctx, unchangedPlan, unchangedState, &diags); ok || got != nil || diags.HasError() {
		t.Fatalf("unchanged content should not update, got %#v", got)
	}

	plan := &diveModel{Content: types.StringValue("export default 1"), APIVersion: types.Int64Value(2)}
	state := &diveModel{Content: types.StringValue("export default null"), APIVersion: types.Int64Value(1)}
	got, ok := diveContentArgs(ctx, plan, state, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("expected content update, diagnostics: %v", diags)
	}
	want := map[string]string{"content": "'export default 1'", "api_version": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diveContentArgs() = %#v, want %#v", got, want)
	}
}

func TestValidateDucklingCooldowns(t *testing.T) {
	valid := &ducklingConfigModel{
		ReadWriteInstanceSize:      types.StringValue("standard"),
		ReadWriteCooldownSeconds:   types.Int64Value(60),
		ReadScalingInstanceSize:    types.StringValue("standard"),
		ReadScalingCooldownSeconds: types.Int64Value(120),
	}
	var validDiags diag.Diagnostics
	if !validateDucklingCooldowns(valid, &validDiags) {
		t.Fatalf("standard instances should allow cooldowns: %v", validDiags)
	}

	invalid := &ducklingConfigModel{
		ReadWriteInstanceSize:      types.StringValue("pulse"),
		ReadWriteCooldownSeconds:   types.Int64Value(60),
		ReadScalingInstanceSize:    types.StringValue("Pulse"),
		ReadScalingCooldownSeconds: types.Int64Value(120),
	}
	var invalidDiags diag.Diagnostics
	if validateDucklingCooldowns(invalid, &invalidDiags) {
		t.Fatal("pulse instances should reject cooldowns")
	}
	if got, want := len(invalidDiags), 2; got != want {
		t.Fatalf("diagnostic count = %d, want %d", got, want)
	}
}

func TestFlightArgs(t *testing.T) {
	ctx := context.Background()
	config, configDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"warehouse": "analytics"})
	if configDiags.HasError() {
		t.Fatalf("config diagnostics: %v", configDiags)
	}
	secrets, secretDiags := types.ListValueFrom(ctx, types.StringType, []string{"aws", "github"})
	if secretDiags.HasError() {
		t.Fatalf("secret diagnostics: %v", secretDiags)
	}
	model := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringValue("0 5 * * *"),
		RequirementsTxt:   types.StringValue("requests==2.32.0"),
		Config:            config,
		AccessTokenName:   types.StringValue("flight-token"),
		FlightSecretNames: secrets,
		MaxRuntimeSec:     types.Int64Value(900),
	}

	var diags diag.Diagnostics
	got, ok := flightCreateArgs(ctx, model, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("flightCreateArgs failed: %v", diags)
	}
	// #nosec G101 -- expected SQL literals are not credentials.
	want := map[string]string{
		"access_token_name":   "'flight-token'",
		"config":              "MAP {'warehouse': 'analytics'}",
		"flight_secret_names": "['aws', 'github']",
		"name":                "'daily'",
		"max_runtime_sec":     "900",
		"requirements_txt":    "'requests==2.32.0'",
		"schedule_cron":       "'0 5 * * *'",
		"source_code":         "'print(''ok'')'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flightCreateArgs() = %#v, want %#v", got, want)
	}
}

func TestFlightUpdateArgsClearsOptionalFields(t *testing.T) {
	ctx := context.Background()
	stateConfig, configDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"warehouse": "analytics"})
	if configDiags.HasError() {
		t.Fatalf("config diagnostics: %v", configDiags)
	}
	stateSecrets, secretDiags := types.ListValueFrom(ctx, types.StringType, []string{"aws", "github"})
	if secretDiags.HasError() {
		t.Fatalf("secret diagnostics: %v", secretDiags)
	}
	plan := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringNull(),
		RequirementsTxt:   types.StringNull(),
		Config:            types.MapNull(types.StringType),
		AccessTokenName:   types.StringNull(),
		FlightSecretNames: types.ListNull(types.StringType),
		MaxRuntimeSec:     types.Int64Value(60),
	}
	state := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringValue("0 5 * * *"),
		RequirementsTxt:   types.StringValue("requests==2.32.0"),
		Config:            stateConfig,
		AccessTokenName:   types.StringNull(),
		FlightSecretNames: stateSecrets,
		MaxRuntimeSec:     types.Int64Value(900),
	}

	var diags diag.Diagnostics
	got, ok := flightUpdateArgs(ctx, plan, state, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("flightUpdateArgs failed: %v", diags)
	}
	want := map[string]string{
		"config":              "NULL",
		"flight_secret_names": "NULL",
		"max_runtime_sec":     "60",
		"requirements_txt":    "NULL",
		"schedule_cron":       "''",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flightUpdateArgs() = %#v, want %#v", got, want)
	}
}

func TestFlightUpdateArgsRejectsAccessTokenClear(t *testing.T) {
	ctx := context.Background()
	plan := &flightModel{
		Name:            types.StringValue("daily"),
		SourceCode:      types.StringValue("print('ok')"),
		AccessTokenName: types.StringNull(),
	}
	state := &flightModel{
		Name:            types.StringValue("daily"),
		SourceCode:      types.StringValue("print('ok')"),
		AccessTokenName: types.StringValue("flight-token"),
	}

	var diags diag.Diagnostics
	if _, ok := flightUpdateArgs(ctx, plan, state, &diags); ok {
		t.Fatal("expected access token clear to fail")
	}
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), "Replace the Flight resource") {
		t.Fatalf("expected replacement diagnostic, got %v", diags)
	}
}

func TestOptionalFlightVersionValuesFromJSON(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	nullList := optionalStringListFromJSON(ctx, types.ListNull(types.StringType), sql.NullString{String: "[]", Valid: true}, "flight_secret_names", &diags)
	if diags.HasError() {
		t.Fatalf("list diagnostics: %v", diags)
	}
	if !nullList.IsNull() {
		t.Fatalf("empty live list should preserve null config state, got %#v", nullList)
	}

	currentList, listDiags := types.ListValueFrom(ctx, types.StringType, []string{"old"})
	if listDiags.HasError() {
		t.Fatalf("current list diagnostics: %v", listDiags)
	}
	emptyList := optionalStringListFromJSON(ctx, currentList, sql.NullString{String: "[]", Valid: true}, "flight_secret_names", &diags)
	if diags.HasError() {
		t.Fatalf("list diagnostics: %v", diags)
	}
	if emptyList.IsNull() {
		t.Fatal("empty live list should be set when prior state was configured")
	}

	liveMap := optionalStringMapFromJSON(ctx, types.MapNull(types.StringType), sql.NullString{String: `{"region":"us"}`, Valid: true}, "config", &diags)
	if diags.HasError() {
		t.Fatalf("map diagnostics: %v", diags)
	}
	if liveMap.IsNull() {
		t.Fatal("non-empty live map should be recorded")
	}

	if got := optionalStringFromLive(types.StringNull(), sql.NullString{String: "", Valid: true}); !got.IsNull() {
		t.Fatalf("empty live optional string should preserve null config state, got %#v", got)
	}
	if got := optionalStringFromLive(types.StringValue("old"), sql.NullString{String: "", Valid: true}); got.IsNull() || got.ValueString() != "" {
		t.Fatalf("empty live optional string should be recorded when prior state was configured, got %#v", got)
	}
}

func sqlNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func sqlNullStringInvalid() sql.NullString {
	return sql.NullString{}
}

type fakeSQLFunctionClient struct {
	available bool
	err       error
}

func (f fakeSQLFunctionClient) Exists(context.Context, string, ...any) (bool, error) {
	return f.available, f.err
}
