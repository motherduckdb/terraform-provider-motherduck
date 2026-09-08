package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const testSensitiveShareURL = "md:_share/tenant_a/0f9c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"

func TestSensitiveWriteDiagnosticNeverEchoesSensitiveLiterals(t *testing.T) {
	statement := "SELECT id FROM MD_CREATE_DIVE(required_resources := [{alias: 'a', url: '" + testSensitiveShareURL + "'}])"
	cases := map[string]error{
		"parser":  &duckdb.Error{Type: duckdb.ErrorTypeParser, Msg: "Parser Error: syntax error at or near \"]\"\nLINE 1: " + statement},
		"binder":  &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: "Binder Error: invalid argument\nLINE 1: " + statement},
		"catalog": &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: function MD_CREATE_DIVE does not exist\n" + statement},
		"generic": errors.New("driver failure while running: " + statement),
		"wrapped": errors.Join(errors.New("retry exhausted"), errors.New("statement failed: "+statement)),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			got := sensitiveWriteDiagnostic("Dive", err, []string{testSensitiveShareURL})
			if got == "" {
				t.Fatal("expected a diagnostic detail")
			}
			if strings.Contains(got, testSensitiveShareURL) {
				t.Fatalf("diagnostic leaked sensitive URL: %q", got)
			}
			if strings.Contains(got, "tenant_a") {
				t.Fatalf("diagnostic leaked sensitive URL fragment: %q", got)
			}
		})
	}
}

func TestSensitiveWriteDiagnosticDropsRawDuckDBMessage(t *testing.T) {
	err := &duckdb.Error{Type: duckdb.ErrorTypeParser, Msg: "Parser Error: top secret statement text"}
	got := sensitiveWriteDiagnostic("Guide", err, nil)
	if strings.Contains(got, "top secret") {
		t.Fatalf("duckdb message must not be copied: %q", got)
	}
	if !strings.Contains(got, "invalid SQL syntax") {
		t.Fatalf("parser errors should be classified as syntax problems: %q", got)
	}
	if !strings.Contains(got, "sensitive values") {
		t.Fatalf("diagnostic should explain why raw details are omitted: %q", got)
	}
}

func TestSensitiveWriteDiagnosticRedactsEscapedLiterals(t *testing.T) {
	value := "md:_share/it's/abc"
	err := errors.New("failed: url := 'md:_share/it''s/abc' rejected; also raw md:_share/it's/abc")
	got := sensitiveWriteDiagnostic("Dive", err, []string{value, ""})
	if strings.Contains(got, "it's") || strings.Contains(got, "it''s") {
		t.Fatalf("escaped literal leaked: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected redaction marker: %q", got)
	}
}

func TestSensitiveWriteDiagnosticContextErrors(t *testing.T) {
	got := sensitiveWriteDiagnostic("Dive", context.DeadlineExceeded, nil)
	if !strings.Contains(got, "canceled or timed out") {
		t.Fatalf("unexpected context diagnostic: %q", got)
	}
	if sensitiveWriteDiagnostic("Dive", nil, nil) != "" {
		t.Fatal("nil error should produce no detail")
	}
}

func TestDiveSensitiveValuesCollectsURLs(t *testing.T) {
	ctx := context.Background()
	objType := types.ObjectType{AttrTypes: map[string]attr.Type{"alias": types.StringType, "url": types.StringType}}
	first, diags := types.ObjectValue(objType.AttrTypes, map[string]attr.Value{"alias": types.StringValue("a"), "url": types.StringValue(testSensitiveShareURL)})
	if diags.HasError() {
		t.Fatalf("object value: %v", diags)
	}
	list, diags := types.ListValue(objType, []attr.Value{first})
	if diags.HasError() {
		t.Fatalf("list value: %v", diags)
	}
	got := diveSensitiveValues(ctx, list)
	if len(got) != 1 || got[0] != testSensitiveShareURL {
		t.Fatalf("diveSensitiveValues = %#v, want the configured URL", got)
	}
	if diveSensitiveValues(ctx, types.ListNull(objType)) != nil {
		t.Fatal("null list should yield no values")
	}
	if diveSensitiveValues(ctx, types.ListUnknown(objType)) != nil {
		t.Fatal("unknown list should yield no values")
	}
}

func TestGuideSensitiveValuesCollectsURLs(t *testing.T) {
	ctx := context.Background()
	attrTypes := guideReferenceAttrTypes()
	objType := types.ObjectType{AttrTypes: attrTypes}
	values := map[string]attr.Value{}
	for name := range attrTypes {
		values[name] = types.StringNull()
	}
	values["type"] = types.StringValue("catalog")
	values["url"] = types.StringValue(testSensitiveShareURL)
	reference, diags := types.ObjectValue(attrTypes, values)
	if diags.HasError() {
		t.Fatalf("object value: %v", diags)
	}
	list, diags := types.ListValue(objType, []attr.Value{reference})
	if diags.HasError() {
		t.Fatalf("list value: %v", diags)
	}
	got := guideSensitiveValues(ctx, list)
	if len(got) != 1 || got[0] != testSensitiveShareURL {
		t.Fatalf("guideSensitiveValues = %#v, want the configured URL", got)
	}
	if guideSensitiveValues(ctx, types.ListNull(objType)) != nil {
		t.Fatal("null list should yield no values")
	}
}

func TestRedactSensitiveValuesOverlappingPrefixes(t *testing.T) {
	short := "https://host/resource"
	long := short + "?token=secret"
	for name, order := range map[string][]string{"short first": {short, long}, "long first": {long, short}} {
		got := redactSensitiveValues("failed: "+long+" and "+short, order)
		if strings.Contains(got, "secret") || strings.Contains(got, "host/resource") {
			t.Fatalf("%s: sensitive text survived redaction: %q", name, got)
		}
	}
}
