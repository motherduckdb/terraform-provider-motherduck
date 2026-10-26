package datasources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTypedRowsCharacterization(t *testing.T) {
	spec := rowSpec{typedRows: []typedRowAttribute{{name: "name", source: "original"}, {name: "missing"}, {name: "value", sensitive: true}}}
	for _, input := range []string{"[]", "null"} {
		got, diags := spec.typedRowsValue(input)
		if diags.HasError() || got.IsNull() || len(got.Elements()) != 0 {
			t.Fatalf("%q: %v, %v", input, got, diags)
		}
	}
	got, diags := spec.typedRowsValue(`[{"original":"first","value":12},{"original":"second","value":null}]`)
	if diags.HasError() || len(got.Elements()) != 2 {
		t.Fatalf("rows: %v, %v", got, diags)
	}
	for i, want := range []string{"first", "second"} {
		attrs := got.Elements()[i].(types.Object).Attributes()
		if !attrs["name"].Equal(types.StringValue(want)) || !attrs["missing"].IsNull() {
			t.Fatalf("row %d: %v", i, attrs)
		}
		if i == 0 && !attrs["value"].Equal(types.StringValue("12")) {
			t.Fatalf("row %d: %v", i, attrs)
		}
		if i == 1 && !attrs["value"].IsNull() {
			t.Fatalf("row %d: %v", i, attrs)
		}
	}
	invalid, invalidDiags := spec.typedRowsValue("[")
	if !invalid.IsNull() || len(invalidDiags) != 1 || invalidDiags[0].Summary() != "Unable to decode typed MotherDuck rows" || invalidDiags[0].Detail() != "unexpected end of JSON input" {
		t.Fatalf("invalid: %v, %v", invalid, invalidDiags)
	}
	if !spec.typedRowsAttribute().NestedObject.Attributes["value"].IsSensitive() {
		t.Fatal("value must remain sensitive")
	}
}
