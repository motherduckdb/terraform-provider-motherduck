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
	r.createOrReplaceView(context.Background(), modelGetter{model: plan}, discardSetter{}, &fakePrivateState{}, &diags)
	if !diags.HasError() {
		t.Fatal("expected apply-time validation error")
	}
	if len(client.execs) != 0 {
		t.Fatalf("expected no SQL to run, got %q", client.execs)
	}
}
