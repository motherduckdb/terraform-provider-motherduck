package resources

import (
	"context"
	"errors"
	"reflect"
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
	err := errors.New("failed: url := 'md:_share/it''s/abc' rejected. Also raw md:_share/it's/abc")
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
	if guideSensitiveValues(ctx, types.ListUnknown(objType)) != nil {
		t.Fatal("unknown list should yield no values")
	}
}

func TestDiveSensitiveValuesCharacterizesTerraformValues(t *testing.T) {
	ctx := context.Background()
	objType := diveRequiredResourceObjectType()
	object := func(alias, url attr.Value) attr.Value {
		t.Helper()
		return types.ObjectValueMust(objType.AttrTypes, map[string]attr.Value{"alias": alias, "url": url})
	}
	list := func(elements ...attr.Value) types.List {
		t.Helper()
		return types.ListValueMust(objType, elements)
	}

	tests := []struct {
		name string
		list types.List
		want []string
		nil  bool
	}{
		{name: "known empty", list: list(), want: []string{}},
		{
			name: "known URLs preserve order and duplicates",
			list: list(
				object(types.StringValue("first"), types.StringValue("")),
				object(types.StringValue("second"), types.StringValue("  ")),
				object(types.StringValue("third"), types.StringValue("https://host/resource")),
				object(types.StringValue("fourth"), types.StringValue("https://host/resource")),
			),
			want: []string{"", "  ", "https://host/resource", "https://host/resource"},
		},
		{
			name: "URL null and unknown are skipped",
			list: list(
				object(types.StringValue("null"), types.StringNull()),
				object(types.StringValue("unknown"), types.StringUnknown()),
				object(types.StringValue("known"), types.StringValue("md:_share/known")),
			),
			want: []string{"md:_share/known"},
		},
		{
			name: "non URL null and unknown are ignored",
			list: list(
				object(types.StringNull(), types.StringValue("md:_share/null-alias")),
				object(types.StringUnknown(), types.StringValue("md:_share/unknown-alias")),
			),
			want: []string{"md:_share/null-alias", "md:_share/unknown-alias"},
		},
		{name: "null object", list: list(object(types.StringValue("known"), types.StringValue("md:_share/known")), types.ObjectNull(objType.AttrTypes)), nil: true},
		{name: "unknown object", list: list(object(types.StringValue("known"), types.StringValue("md:_share/known")), types.ObjectUnknown(objType.AttrTypes)), nil: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := diveSensitiveValues(ctx, test.list)
			if test.nil {
				if got != nil {
					t.Fatalf("diveSensitiveValues = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("diveSensitiveValues = %#v, want %#v", got, test.want)
			}
		})
	}

	incompatibleType := types.ObjectType{AttrTypes: map[string]attr.Type{"alias": types.Int64Type, "url": types.StringType}}
	incompatible := types.ListValueMust(incompatibleType, []attr.Value{
		types.ObjectValueMust(incompatibleType.AttrTypes, map[string]attr.Value{
			"alias": types.Int64Value(42),
			"url":   types.StringValue("md:_share/known"),
		}),
	})
	if got := diveSensitiveValues(ctx, incompatible); got != nil {
		t.Fatalf("diveSensitiveValues incompatible object = %#v, want nil", got)
	}
}

func TestGuideSensitiveValuesCharacterizesTerraformValues(t *testing.T) {
	ctx := context.Background()
	attrTypes := guideReferenceAttrTypes()
	objType := types.ObjectType{AttrTypes: attrTypes}
	object := func(url, field attr.Value) attr.Value {
		t.Helper()
		values := make(map[string]attr.Value, len(attrTypes))
		for name := range attrTypes {
			values[name] = types.StringNull()
		}
		values["url"] = url
		values["type"] = field
		return types.ObjectValueMust(attrTypes, values)
	}
	list := func(elements ...attr.Value) types.List {
		t.Helper()
		return types.ListValueMust(objType, elements)
	}

	tests := []struct {
		name string
		list types.List
		want []string
		nil  bool
	}{
		{name: "known empty", list: list(), want: []string{}},
		{
			name: "known URLs preserve order and duplicates",
			list: list(
				object(types.StringValue(""), types.StringNull()),
				object(types.StringValue("  "), types.StringUnknown()),
				object(types.StringValue("https://host/resource"), types.StringValue("catalog")),
				object(types.StringValue("https://host/resource"), types.StringValue("catalog")),
			),
			want: []string{"", "  ", "https://host/resource", "https://host/resource"},
		},
		{
			name: "URL null and unknown are skipped",
			list: list(
				object(types.StringNull(), types.StringValue("catalog")),
				object(types.StringUnknown(), types.StringValue("catalog")),
				object(types.StringValue("md:_share/known"), types.StringValue("catalog")),
			),
			want: []string{"md:_share/known"},
		},
		{
			name: "non URL null and unknown are ignored",
			list: list(
				object(types.StringValue("md:_share/null-fields"), types.StringNull()),
				object(types.StringValue("md:_share/unknown-fields"), types.StringUnknown()),
			),
			want: []string{"md:_share/null-fields", "md:_share/unknown-fields"},
		},
		{name: "null object", list: list(object(types.StringValue("md:_share/known"), types.StringValue("catalog")), types.ObjectNull(objType.AttrTypes)), nil: true},
		{name: "unknown object", list: list(object(types.StringValue("md:_share/known"), types.StringValue("catalog")), types.ObjectUnknown(objType.AttrTypes)), nil: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := guideSensitiveValues(ctx, test.list)
			if test.nil {
				if got != nil {
					t.Fatalf("guideSensitiveValues = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("guideSensitiveValues = %#v, want %#v", got, test.want)
			}
		})
	}

	incompatibleTypes := guideReferenceAttrTypes()
	incompatibleTypes["type"] = types.Int64Type
	incompatible := types.ListValueMust(types.ObjectType{AttrTypes: incompatibleTypes}, []attr.Value{
		types.ObjectValueMust(incompatibleTypes, func() map[string]attr.Value {
			values := make(map[string]attr.Value, len(incompatibleTypes))
			for name := range incompatibleTypes {
				values[name] = types.StringNull()
			}
			values["type"] = types.Int64Value(42)
			values["url"] = types.StringValue("md:_share/known")
			return values
		}()),
	})
	if got := guideSensitiveValues(ctx, incompatible); got != nil {
		t.Fatalf("guideSensitiveValues incompatible object = %#v, want nil", got)
	}
}

func TestSensitiveWriteDiagnosticRedactsExtractedURLs(t *testing.T) {
	short := "https://host/resource"
	long := short + "?token=secret"
	apostrophe := "md:_share/it's/abc"
	message := "driver failure: " + long + " and " + short + " and " + apostrophe + " and md:_share/it''s/abc"

	t.Run("dive", func(t *testing.T) {
		objType := diveRequiredResourceObjectType()
		list := types.ListValueMust(objType, []attr.Value{
			types.ObjectValueMust(objType.AttrTypes, map[string]attr.Value{"alias": types.StringValue("a"), "url": types.StringValue(short)}),
			types.ObjectValueMust(objType.AttrTypes, map[string]attr.Value{"alias": types.StringValue("b"), "url": types.StringValue(long)}),
			types.ObjectValueMust(objType.AttrTypes, map[string]attr.Value{"alias": types.StringValue("c"), "url": types.StringValue(apostrophe)}),
		})
		assertRedactedExtractedURLs(t, message, diveSensitiveValues(context.Background(), list))
	})

	t.Run("guide", func(t *testing.T) {
		attrTypes := guideReferenceAttrTypes()
		objType := types.ObjectType{AttrTypes: attrTypes}
		object := func(url string) attr.Value {
			values := make(map[string]attr.Value, len(attrTypes))
			for name := range attrTypes {
				values[name] = types.StringNull()
			}
			values["url"] = types.StringValue(url)
			return types.ObjectValueMust(attrTypes, values)
		}
		list := types.ListValueMust(objType, []attr.Value{object(short), object(long), object(apostrophe)})
		assertRedactedExtractedURLs(t, message, guideSensitiveValues(context.Background(), list))
	})
}

func assertRedactedExtractedURLs(t *testing.T, message string, sensitive []string) {
	t.Helper()
	got := sensitiveWriteDiagnostic("Guide", errors.New(message), sensitive)
	want := "driver failure: [redacted] and [redacted] and [redacted] and [redacted]"
	if got != want {
		t.Fatalf("diagnostic = %q, want %q", got, want)
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
