package resources

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

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

func TestIsNotFoundForRequiresNamedObject(t *testing.T) {
	dbMissing := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Database with name "analytics" does not exist!`}
	tableMissing := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table with name facts does not exist!"}
	unrelated := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table with name information_schema.views does not exist!"}
	rest := &mdrest.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "entity not found"}
	tests := map[string]struct {
		err   error
		names []string
		want  bool
	}{
		"names the database":              {err: dbMissing, names: []string{"analytics", "main", "facts"}, want: true},
		"names the object":                {err: tableMissing, names: []string{"analytics", "main", "facts"}, want: true},
		"case insensitive":                {err: tableMissing, names: []string{"FACTS"}, want: true},
		"unrelated catalog object":        {err: unrelated, names: []string{"analytics", "main", "facts"}, want: false},
		"non catalog error":               {err: errors.New("facts not found"), names: []string{"facts"}, want: false},
		"nil error":                       {err: nil, names: []string{"facts"}, want: false},
		"no names falls back to broad":    {err: unrelated, names: nil, want: true},
		"empty names fall back to broad":  {err: unrelated, names: []string{"", " "}, want: true},
		"rest entity not found unchanged": {err: rest, names: []string{"anything"}, want: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isNotFoundFor(tc.err, tc.names...); got != tc.want {
				t.Fatalf("isNotFoundFor() = %t, want %t", got, tc.want)
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

// recordingSQLClient is an always-available SQL client that records executed
// statements so apply-path tests can prove that nothing reached the server.
type recordingSQLClient struct {
	execs []string
}

func (c *recordingSQLClient) Available() bool { return true }

func (c *recordingSQLClient) AttachDatabase(context.Context, string) error { return nil }

func (c *recordingSQLClient) Close() error { return nil }

func (c *recordingSQLClient) Exec(_ context.Context, query string, _ ...any) error {
	c.execs = append(c.execs, query)
	return nil
}

func (c *recordingSQLClient) Exists(context.Context, string, ...any) (bool, error) {
	return false, nil
}

func (c *recordingSQLClient) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return errRowScanner{err: sql.ErrNoRows}
}

func (c *recordingSQLClient) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return "[]", nil
}

func (c *recordingSQLClient) ScalarString(context.Context, string, ...any) (string, error) {
	return "", nil
}

func (c *recordingSQLClient) WithDatabaseUse(ctx context.Context, _ string, fn func(func(string, ...any) error) error) error {
	return fn(func(query string, args ...any) error { return c.Exec(ctx, query, args...) })
}

type errRowScanner struct{ err error }

func (r errRowScanner) Scan(...any) error { return r.err }

// modelGetter satisfies the plan/state getter interface used by the shared
// create helpers, returning a fixed model.
type modelGetter struct{ model any }

func (g modelGetter) Get(_ context.Context, target any) diag.Diagnostics {
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(g.model))
	return nil
}

type discardSetter struct{}

func (discardSetter) Set(context.Context, any) diag.Diagnostics { return nil }

func TestContainsIdentifierWord(t *testing.T) {
	cases := map[string]struct {
		msg, name string
		want      bool
	}{
		"short name inside another word": {"catalog error: table with name b does not exist", "a", false},
		"short name as whole token":      {"catalog error: table with name a does not exist", "a", true},
		"quoted name":                    {`catalog error: table "tf_x" does not exist`, "tf_x", true},
		"name is prefix of another":      {"table tf_x_backup does not exist", "tf_x", false},
		"name at end":                    {"does not exist: tf_x", "tf_x", true},
	}
	for label, tc := range cases {
		if got := containsIdentifierWord(tc.msg, tc.name); got != tc.want {
			t.Fatalf("%s: containsIdentifierWord(%q, %q) = %v, want %v", label, tc.msg, tc.name, got, tc.want)
		}
	}
}
