package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// kelvinSign is U+212A, which Unicode case folding treats as the letter k.
const kelvinSign = "K"

// DuckDB folds only ASCII letters when it compares schema, table, and view
// names, while MotherDuck resolves database names with a Unicode
// case-insensitive collation. The plan modifiers must replace an object
// exactly when the new name resolves to a different object.
func TestIdentifierPlanModifiersMatchCatalogResolution(t *testing.T) {
	ctx := context.Background()
	type renameCase struct {
		prior, planned string
		wantReplace    bool
	}
	asciiCases := []renameCase{
		{"Sales", "sales", false},
		{"sales", "SALES", false},
		{"Äpfel", "äpfel", true},
		{"Kelvin", kelvinSign + "elvin", true},
		{"KELVIN", kelvinSign + "ELVIN", true},
		{"ſales", "sales", true},
		{"sales", "orders", true},
	}
	unicodeCases := []renameCase{
		{"Analytics", "analytics", false},
		{"Äpfel", "äpfel", false},
		{"Kelvin", kelvinSign + "elvin", false},
		{"analytics", "warehouse", true},
	}
	for _, tc := range []struct {
		resource  resource.Resource
		attribute string
		unicode   bool
	}{
		{NewSchemaResource(), "name", false},
		{NewSchemaResource(), "database", true},
		{NewTableResource(), "name", false},
		{NewTableResource(), "schema", false},
		{NewTableResource(), "database", true},
		{NewViewResource(), "name", false},
		{NewViewResource(), "schema", false},
		{NewViewResource(), "database", true},
	} {
		var schemaResp resource.SchemaResponse
		tc.resource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
		var metadata resource.MetadataResponse
		tc.resource.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "motherduck"}, &metadata)
		modifiers := schemaResp.Schema.Attributes[tc.attribute].(schema.StringAttribute).PlanModifiers
		cases := asciiCases
		if tc.unicode {
			cases = unicodeCases
		}
		for _, c := range cases {
			t.Run(metadata.TypeName+"."+tc.attribute+"/"+c.prior+"->"+c.planned, func(t *testing.T) {
				state, plan := identifierPlanPair(t, schemaResp.Schema, tc.attribute, c.prior, c.planned)
				req := planmodifier.StringRequest{
					Path:        path.Root(tc.attribute),
					State:       state,
					Plan:        plan,
					StateValue:  types.StringValue(c.prior),
					PlanValue:   types.StringValue(c.planned),
					ConfigValue: types.StringValue(c.planned),
				}
				resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
				for _, modifier := range modifiers {
					modifier.PlanModifyString(ctx, req, resp)
				}
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				if resp.RequiresReplace != c.wantReplace {
					t.Fatalf("RequiresReplace = %t, want %t", resp.RequiresReplace, c.wantReplace)
				}
			})
		}
	}
}

func identifierPlanPair(t *testing.T, s schema.Schema, attribute, prior, planned string) (tfsdk.State, tfsdk.Plan) {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.SetAttribute(ctx, path.Root(attribute), prior); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.SetAttribute(ctx, path.Root(attribute), planned); diags.HasError() {
		t.Fatal(diags)
	}
	return state, plan
}

// duckDBCatalogClient answers catalog queries from a local DuckDB, whose
// catalog folds identifiers the same way MotherDuck's does.
type duckDBCatalogClient struct {
	recordingSQLClient
	db *stdsql.DB
}

func (c *duckDBCatalogClient) QueryRowsJSON(ctx context.Context, query string, args ...any) (string, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return "", err
		}
		row := map[string]any{}
		for i, column := range columns {
			row[column] = values[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(out)
	return string(encoded), err
}

// DuckDB's lower() folds Unicode letters, so a lower() filter also returns
// objects that DuckDB treats as different from the configured name. The
// lookups must report only the object that DDL on the configured name would
// resolve.
func TestCatalogLookupsFoldOnlyASCIILetters(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE SCHEMA "Ärger"`,
		`CREATE SCHEMA "Sales"`,
		`CREATE TABLE "Äpfel" (a INTEGER)`,
		`CREATE TABLE "äpfel" (b VARCHAR)`,
		`CREATE TABLE "` + kelvinSign + `elvin" (k BIGINT)`,
		`CREATE TABLE "Orders" (o INTEGER)`,
		`CREATE VIEW "Öl" AS SELECT 1 AS upper_view`,
		`CREATE VIEW "öl" AS SELECT 2 AS lower_view`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	client := &duckDBCatalogClient{db: db}
	ctx := t.Context()

	for name, want := range map[string]bool{"Ärger": true, "ÄRGER": true, "ärger": false, "äRGER": false, "sales": true, "ſales": false} {
		got, err := schemaExists(ctx, client, "memory", name)
		if err != nil || got != want {
			t.Errorf("schemaExists(%q) = %v, %v, want %v", name, got, err, want)
		}
	}

	table := &tableResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	for name, want := range map[string]bool{"Äpfel": true, "äpfel": true, "ÄPFEL": true, "äPFEL": true, "kelvin": false, kelvinSign + "ELVIN": true, "orders": true, "ſales": false} {
		var diags diag.Diagnostics
		if got := relationExists(ctx, table, "MEMORY", "MAIN", name, "BASE TABLE", &diags); got != want || diags.HasError() {
			t.Errorf("relationExists(%q) = %v, %v, want %v", name, got, diags, want)
		}
	}

	for name, want := range map[string]string{"Äpfel": "a=INTEGER", "äpfel": "b=VARCHAR", "äPFEL": "b=VARCHAR", kelvinSign + "elvin": "k=BIGINT", "kelvin": ""} {
		var diags diag.Diagnostics
		columns := readTableColumnTypes(ctx, client, "memory", "main", name, &diags)
		got := ""
		for column, columnType := range columns {
			got += column + "=" + columnType
		}
		if got != want || diags.HasError() {
			t.Errorf("readTableColumnTypes(%q) = %q, %v, want %q", name, got, diags, want)
		}
	}

	for name, want := range map[string]string{"Öl": "SELECT 1 AS upper_view", "öl": "SELECT 2 AS lower_view", "ÖL": "SELECT 1 AS upper_view"} {
		var diags diag.Diagnostics
		definition, found := readViewServerDefinition(ctx, client, "memory", "main", name, &diags)
		if got := viewQueryFromDefinition(definition); !found || diags.HasError() || got != want {
			t.Errorf("readViewServerDefinition(%q) = %q, %v, %v, want query %q", name, got, found, diags, want)
		}
	}
	var diags diag.Diagnostics
	if _, found := readViewServerDefinition(ctx, client, "memory", "main", "oel", &diags); found || diags.HasError() {
		t.Errorf("readViewServerDefinition(oel) found = %v, %v, want not found", found, diags)
	}
}

func TestMatchCatalogRowPrefersExactMatch(t *testing.T) {
	rows := [][]string{{"analytics", "main", "facts"}, {"Analytics", "main", "facts"}, {"Analytics", "main", "Äpfel"}}
	for _, tc := range []struct {
		want  []string
		index int
	}{
		{[]string{"Analytics", "main", "facts"}, 1},
		{[]string{"analytics", "main", "facts"}, 0},
		{[]string{"ANALYTICS", "MAIN", "FACTS"}, 0},
		{[]string{"analytics", "main", "äpfel"}, -1},
		{[]string{"analytics", "main", "ÄPFEL"}, 2},
	} {
		if got := matchCatalogRow(rows, tc.want...); got != tc.index {
			t.Errorf("matchCatalogRow(%q) = %d, want %d", tc.want, got, tc.index)
		}
	}
}
