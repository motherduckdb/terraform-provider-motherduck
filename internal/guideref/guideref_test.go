package guideref

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func catalogRef() Reference {
	return Reference{
		Type:   types.StringValue("catalog"),
		URL:    types.StringValue("md:analytics"),
		Schema: types.StringNull(),
		Table:  types.StringNull(),
		Column: types.StringNull(),
		View:   types.StringNull(),
		Macro:  types.StringNull(),
		UUID:   types.StringNull(),
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Reference)
		want   Problem
	}{
		"catalog":     {func(*Reference) {}, Valid},
		"missing url": {func(r *Reference) { r.URL = types.StringNull() }, MissingURL},
		"non md url":  {func(r *Reference) { r.URL = types.StringValue("s3://bucket") }, MissingURL},
		"two narrowings": {func(r *Reference) {
			r.Schema, r.Table, r.View = types.StringValue("main"), types.StringValue("t"), types.StringValue("v")
		}, MultipleNarrowings},
		"column without":     {func(r *Reference) { r.Column = types.StringValue("c") }, ColumnWithoutTable},
		"empty column unset": {func(r *Reference) { r.Column = types.StringValue("") }, Valid},
		"table without":      {func(r *Reference) { r.Table = types.StringValue("t") }, MissingSchema},
		"empty schema":       {func(r *Reference) { r.Table, r.Schema = types.StringValue("t"), types.StringValue("") }, MissingSchema},
		"table with schema":  {func(r *Reference) { r.Table, r.Schema = types.StringValue("t"), types.StringValue("main") }, Valid},
		"dive without uuid":  {func(r *Reference) { r.Type = types.StringValue("dive") }, MissingUUID},
		"guide with uuid":    {func(r *Reference) { r.Type, r.UUID = types.StringValue("guide"), types.StringValue("x") }, Valid},
		"unsupported":        {func(r *Reference) { r.Type = types.StringValue("table") }, UnsupportedType},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ref := catalogRef()
			tc.mutate(&ref)
			if got := Validate(ref); got != tc.want {
				t.Fatalf("Validate() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStructLiteral(t *testing.T) {
	ref := catalogRef()
	ref.Schema = types.StringValue("main")
	ref.Table = types.StringValue("o'rders")
	got := StructLiteral(ref, types.StringValue("why"))
	want := "{'type': 'catalog', 'url': 'md:analytics', 'schema': 'main', 'table': 'o''rders', 'column': NULL::VARCHAR, 'view': NULL::VARCHAR, 'macro': NULL::VARCHAR, 'uuid': NULL::UUID, 'description': 'why'}"
	if got != want {
		t.Fatalf("StructLiteral() = %s, want %s", got, want)
	}
	if got := NullableLiteral(types.StringValue("123e4567-e89b-42d3-a456-426614174000"), "UUID"); got != "'123e4567-e89b-42d3-a456-426614174000'::UUID" {
		t.Fatalf("UUID literal = %s", got)
	}
}
