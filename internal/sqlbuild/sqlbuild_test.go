package sqlbuild

import "testing"

func TestQuoteIdentifier(t *testing.T) {
	got := QuoteIdentifier(` first.last"reader `)
	want := `" first.last""reader "`
	if got != want {
		t.Fatalf("QuoteIdentifier() = %q, want %q", got, want)
	}
}

func TestQuoteQualifiedIdentifier(t *testing.T) {
	got := QuoteQualifiedIdentifier("db", `weird"schema`, "table")
	want := `"db"."weird""schema"."table"`
	if got != want {
		t.Fatalf("QuoteQualifiedIdentifier() = %q, want %q", got, want)
	}
}

func TestStringLiteral(t *testing.T) {
	got := StringLiteral("duck's db")
	want := "'duck''s db'"
	if got != want {
		t.Fatalf("StringLiteral() = %q, want %q", got, want)
	}
}

func TestMapLiteralSortsKeys(t *testing.T) {
	got := MapLiteral(map[string]string{"b": "2", "a": "1"})
	want := "MAP {'a': '1', 'b': '2'}"
	if got != want {
		t.Fatalf("MapLiteral() = %q, want %q", got, want)
	}
}

func TestMapLiteralTypesEmptyMap(t *testing.T) {
	got := MapLiteral(map[string]string{})
	want := "MAP {}::MAP(VARCHAR, VARCHAR)"
	if got != want {
		t.Fatalf("MapLiteral() = %q, want %q", got, want)
	}
}

func TestListLiteralEscapesValues(t *testing.T) {
	got := ListLiteral([]string{"alpha", "beta's"})
	want := "['alpha', 'beta''s']"
	if got != want {
		t.Fatalf("ListLiteral() = %q, want %q", got, want)
	}
}

func TestListLiteralTypesEmptyList(t *testing.T) {
	got := ListLiteral([]string{})
	want := "[]::VARCHAR[]"
	if got != want {
		t.Fatalf("ListLiteral() = %q, want %q", got, want)
	}
}

func TestNamedArgsSortsKeys(t *testing.T) {
	got := NamedArgs(map[string]string{"z": "3", "a": "1"})
	want := "(a := 1, z := 3)"
	if got != want {
		t.Fatalf("NamedArgs() = %q, want %q", got, want)
	}
}

func TestOptionsSkipsBlankParts(t *testing.T) {
	got := Options("", "TRANSIENT", "  ", "ENCRYPTED true")
	want := " (TRANSIENT, ENCRYPTED true)"
	if got != want {
		t.Fatalf("Options() = %q, want %q", got, want)
	}
}

func TestBoolLiteral(t *testing.T) {
	if got := BoolLiteral(true); got != "true" {
		t.Fatalf("BoolLiteral(true) = %q", got)
	}
	if got := BoolLiteral(false); got != "false" {
		t.Fatalf("BoolLiteral(false) = %q", got)
	}
}
