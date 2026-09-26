package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

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

func TestValidateViewQueryRejectsSemicolons(t *testing.T) {
	var diags diag.Diagnostics
	validateViewQuery("SELECT 1", &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics for single statement: %v", diags)
	}
	validateViewQuery("SELECT 1; DROP DATABASE prod", &diags)
	if !diags.HasError() {
		t.Fatal("expected semicolon to be rejected")
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

	// Definition strings as DuckDB 1.5.5 reports them in information_schema.views.
	for definition, want := range map[string]string{
		`CREATE VIEW s."my as view" AS SELECT 1 AS "a AS b", ' AS ' AS c;`: `SELECT 1 AS "a AS b", ' AS ' AS c`,
		`CREATE VIEW "q""x AS " AS SELECT 2;`:                              `SELECT 2`,
		`CREATE VIEW v2 (x) AS SELECT 1;`:                                  `SELECT 1`,
		"CREATE VIEW v\nAS\nSELECT 3;":                                     `SELECT 3`,
	} {
		if got := viewQueryFromDefinition(definition); got != want {
			t.Errorf("viewQueryFromDefinition(%q) = %q, want %q", definition, got, want)
		}
	}
}

func TestViewCreateDoesNotReplaceExistingView(t *testing.T) {
	for _, tc := range []struct {
		replace bool
		want    string
	}{
		{replace: false, want: `CREATE VIEW "analytics"."main"."v" AS SELECT 1`},
		{replace: true, want: `CREATE OR REPLACE VIEW "analytics"."main"."v" AS SELECT 1`},
	} {
		client := &recordingSQLClient{}
		r := &viewResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
		plan := viewModel{
			Database: types.StringValue("analytics"),
			Schema:   types.StringValue("main"),
			Name:     types.StringValue("v"),
			Query:    types.StringValue("SELECT 1"),
		}
		var diags diag.Diagnostics
		r.writeView(context.Background(), modelGetter{model: plan}, discardSetter{}, &fakePrivateState{}, tc.replace, &diags)
		if len(client.execs) == 0 || client.execs[0] != tc.want {
			t.Fatalf("replace=%v execs = %q, want first %q", tc.replace, client.execs, tc.want)
		}
	}
}

func TestViewApplyRejectsMultiStatementQuery(t *testing.T) {
	client := &recordingSQLClient{}
	r := &viewResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	plan := viewModel{
		Database: types.StringValue("analytics"),
		Schema:   types.StringValue("main"),
		Name:     types.StringValue("v"),
		// Unknown at plan time, resolved at apply time to a second statement.
		Query: types.StringValue("SELECT 1; DROP DATABASE prod"),
	}
	var diags diag.Diagnostics
	r.writeView(context.Background(), modelGetter{model: plan}, discardSetter{}, &fakePrivateState{}, false, &diags)
	if !diags.HasError() {
		t.Fatal("expected apply-time validation error")
	}
	if len(client.execs) != 0 {
		t.Fatalf("expected no SQL to run, got %q", client.execs)
	}
}
