package sql

import (
	"slices"
	"testing"
)

func TestParseColumnTypeNestedShapes(t *testing.T) {
	parsed := parseColumnType(`STRUCT("comma,name" UUID, "bracket]name" MAP(VARCHAR, STRUCT("paren(name)" UUID, "double""quote" UUID[])), "union" UNION("tag,name" UUID, "nested" STRUCT("close)" UUID)))[2][][]`)

	outer := parsed
	if !outer.is("LIST") {
		t.Fatalf("outer type = %+v, want LIST", outer)
	}
	inner := outer.element()
	if !inner.is("LIST") {
		t.Fatalf("inner type = %+v, want LIST", inner)
	}
	fixedArray := inner.element()
	if !fixedArray.is("LIST") {
		t.Fatalf("fixed array type = %+v, want LIST", fixedArray)
	}
	structType := fixedArray.element()
	if !structType.is("STRUCT") {
		t.Fatalf("struct type = %+v, want STRUCT", structType)
	}
	if !structType.field("comma,name").is("UUID") {
		t.Fatalf("comma field = %+v, want UUID", structType.field("comma,name"))
	}

	mapType := structType.field("bracket]name")
	if !mapType.is("MAP") || !mapType.mapKey().is("VARCHAR") {
		t.Fatalf("map type = %+v, want MAP(VARCHAR, ...)", mapType)
	}
	nestedStruct := mapType.mapValue()
	if !nestedStruct.is("STRUCT") {
		t.Fatalf("nested map value = %+v, want STRUCT", nestedStruct)
	}
	if !nestedStruct.field("paren(name)").is("UUID") {
		t.Fatalf("parenthesized field = %+v, want UUID", nestedStruct.field("paren(name)"))
	}
	if !nestedStruct.field(`double"quote`).element().is("UUID") {
		t.Fatalf("doubled quote field = %+v, want UUID[]", nestedStruct.field(`double"quote`))
	}

	unionType := structType.field("union")
	if !unionType.is("UNION") {
		t.Fatalf("union type = %+v, want UNION", unionType)
	}
	if !unionType.field("tag,name").is("UUID") {
		t.Fatalf("union tag field = %+v, want UUID", unionType.field("tag,name"))
	}
	if !unionType.field("nested").field("close)").is("UUID") {
		t.Fatalf("nested union field = %+v, want UUID", unionType.field("nested"))
	}
}

func TestParseColumnTypeMalformedFallsBackToNamedTypes(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "UUID", want: "UUID"},
		{name: "UUID[", want: "UUID["},
		{name: "STRUCT(a UUID", want: "STRUCT(A UUID"},
		{name: "MAP(VARCHAR, UUID", want: "MAP(VARCHAR, UUID"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseColumnType(tc.name)
			if parsed == nil || parsed.name != tc.want {
				t.Fatalf("parseColumnType(%q) = %+v, want named type %q", tc.name, parsed, tc.want)
			}
		})
	}

	unterminatedField := parseColumnType(`STRUCT("broken UUID)`)
	if !unterminatedField.is("STRUCT") || len(unterminatedField.fields) != 0 {
		t.Fatalf("unterminated quoted field = %+v, want empty STRUCT fields", unterminatedField)
	}
	if got := parseColumnType(""); got != nil {
		t.Fatalf("parseColumnType(\"\") = %+v, want nil", got)
	}
}

func TestLastTopLevelIndexCharacterizesNestingAndQuotes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		target byte
		want   int
	}{
		{name: "nested delimiters", input: `STRUCT(a MAP(VARCHAR, UUID[]), b UUID)[]`, target: '[', want: 38},
		{name: "quoted delimiter", input: `STRUCT("[" UUID)[]`, target: '[', want: 16},
		{name: "unmatched closing delimiter", input: `] [`, target: '[', want: -1},
		{name: "arbitrary target", input: `a:b:c`, target: ':', want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastTopLevelIndex(tc.input, tc.target); got != tc.want {
				t.Fatalf("lastTopLevelIndex(%q, %q) = %d, want %d", tc.input, tc.target, got, tc.want)
			}
		})
	}
}

func TestSplitTopLevelCharacterizesNestingQuotesAndMalformedDepth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "nested delimiters",
			input: `a MAP(VARCHAR, UUID), b STRUCT("x,y" UUID), c UUID`,
			want:  []string{`a MAP(VARCHAR, UUID)`, `b STRUCT("x,y" UUID)`, `c UUID`},
		},
		{
			name:  "quoted comma and doubled quote",
			input: `"a,b" UUID, "quote""name" UUID, c UUID`,
			want:  []string{`"a,b" UUID`, `"quote""name" UUID`, `c UUID`},
		},
		{
			name:  "unmatched closing delimiter",
			input: `a UUID], b UUID, c UUID`,
			want:  []string{`a UUID], b UUID, c UUID`},
		},
		{
			name:  "empty and whitespace segments",
			input: `  , a UUID,  ,  `,
			want:  []string{"", "a UUID", "", ""},
		},
		{
			name:  "unterminated quote",
			input: `"a,b UUID, c UUID`,
			want:  []string{`"a,b UUID, c UUID`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitTopLevel(tc.input); !slices.Equal(got, tc.want) {
				t.Fatalf("splitTopLevel(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestQueryRowsJSONNormalizesUUIDsInQuotedNestedFields(t *testing.T) {
	client := newEmbeddedClient(t)
	got, err := client.QueryRowsJSON(t.Context(), `SELECT
		CASE WHEN i = 0 THEN 'fdd482f5-740b-4e96-b258-2702d4a69945'::UUID ELSE NULL END AS id,
		CASE WHEN i = 0 THEN ['fdd482f5-740b-4e96-b258-2702d4a69945'::UUID] ELSE NULL END AS ids,
		CASE WHEN i = 0 THEN {
			'comma,field': 'fdd482f5-740b-4e96-b258-2702d4a69945'::UUID,
			'bracket]field': ['fdd482f5-740b-4e96-b258-2702d4a69945'::UUID]
		} ELSE NULL END AS details
		FROM range(2) t(i)
		ORDER BY i`)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"details":{"bracket]field":["fdd482f5-740b-4e96-b258-2702d4a69945"],"comma,field":"fdd482f5-740b-4e96-b258-2702d4a69945"},"id":"fdd482f5-740b-4e96-b258-2702d4a69945","ids":["fdd482f5-740b-4e96-b258-2702d4a69945"]},{"details":null,"id":null,"ids":null}]`
	if got != want {
		t.Fatalf("rows =\n%s\nwant\n%s", got, want)
	}
}
