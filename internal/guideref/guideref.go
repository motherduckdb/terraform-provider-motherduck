// Package guideref validates MotherDuck Guide references and renders them as
// the SQL struct literal the Guide functions accept. The Guide resource and
// the Guides data source share it so references are checked and encoded the
// same way for writes and for list filters.
package guideref

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// Reference is one catalog, Dive, Flight, or Guide reference.
type Reference struct {
	Type   types.String
	URL    types.String
	Schema types.String
	Table  types.String
	Column types.String
	View   types.String
	Macro  types.String
	UUID   types.String
}

// Problem identifies why a reference is invalid. Callers own the wording
// because the resource and the data source name the fields differently.
type Problem int

const (
	Valid Problem = iota
	MissingURL
	MultipleNarrowings
	ColumnWithoutTable
	MissingSchema
	MissingUUID
	UnsupportedType
)

// Validate checks the field combinations MotherDuck accepts for a reference.
// Empty strings count as unset for the optional narrowing fields.
func Validate(ref Reference) Problem {
	switch ref.Type.ValueString() {
	case "catalog":
		if ref.URL.IsNull() || !strings.HasPrefix(ref.URL.ValueString(), "md:") {
			return MissingURL
		}
		narrowings := 0
		for _, value := range []types.String{ref.Table, ref.View, ref.Macro} {
			if set(value) {
				narrowings++
			}
		}
		if narrowings > 1 {
			return MultipleNarrowings
		}
		if set(ref.Column) && !set(ref.Table) {
			return ColumnWithoutTable
		}
		if narrowings > 0 && !set(ref.Schema) {
			return MissingSchema
		}
		return Valid
	case "dive", "flight", "guide":
		if ref.UUID.IsNull() {
			return MissingUUID
		}
		return Valid
	default:
		return UnsupportedType
	}
}

// StructLiteral renders the reference as a Guide reference struct literal.
func StructLiteral(ref Reference, description types.String) string {
	fields := []string{
		"'type': " + NullableLiteral(ref.Type, "VARCHAR"),
		"'url': " + NullableLiteral(ref.URL, "VARCHAR"),
		"'schema': " + NullableLiteral(ref.Schema, "VARCHAR"),
		"'table': " + NullableLiteral(ref.Table, "VARCHAR"),
		"'column': " + NullableLiteral(ref.Column, "VARCHAR"),
		"'view': " + NullableLiteral(ref.View, "VARCHAR"),
		"'macro': " + NullableLiteral(ref.Macro, "VARCHAR"),
		"'uuid': " + NullableLiteral(ref.UUID, "UUID"),
		"'description': " + NullableLiteral(description, "VARCHAR"),
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// NullableLiteral renders a string as a SQL literal of sqlType, or a typed
// NULL when the value is null or unknown.
func NullableLiteral(value types.String, sqlType string) string {
	if value.IsNull() || value.IsUnknown() {
		return "NULL::" + sqlType
	}
	literal := sqlbuild.StringLiteral(value.ValueString())
	if sqlType == "UUID" {
		return literal + "::UUID"
	}
	return literal
}

func set(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}
