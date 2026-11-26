package tfvalidators

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStringMaxUTF16Units(t *testing.T) {
	v := StringMaxUTF16Units("MotherDuck access token description", 4)
	for name, tc := range map[string]struct {
		value   types.String
		wantErr bool
	}{
		"ascii at limit":         {value: types.StringValue("abcd")},
		"ascii over limit":       {value: types.StringValue("abcde"), wantErr: true},
		"accented letters":       {value: types.StringValue("éèêë")},
		"emoji count as two":     {value: types.StringValue("ab🦆"), wantErr: false},
		"emoji push over limit":  {value: types.StringValue("abc🦆"), wantErr: true},
		"only emoji over limit":  {value: types.StringValue(strings.Repeat("🦆", 3)), wantErr: true},
		"unknown is not checked": {value: types.StringUnknown()},
		"null is not checked":    {value: types.StringNull()},
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("description"), ConfigValue: tc.value}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestStringLengthValidatorSingularPlural(t *testing.T) {
	ctx := context.Background()
	singular := StringLength("role", 1, 0)
	if got, want := singular.Description(ctx), "must be at least 1 character"; got != want {
		t.Fatalf("singular Description() = %q, want %q", got, want)
	}
	if got, want := singular.MarkdownDescription(ctx), "must be at least `1` character"; got != want {
		t.Fatalf("singular MarkdownDescription() = %q, want %q", got, want)
	}

	plural := StringLength("role", 2, 0)
	if got, want := plural.Description(ctx), "must be at least 2 characters"; got != want {
		t.Fatalf("plural Description() = %q, want %q", got, want)
	}
	if got, want := plural.MarkdownDescription(ctx), "must be at least `2` characters"; got != want {
		t.Fatalf("plural MarkdownDescription() = %q, want %q", got, want)
	}
	var resp validator.StringResponse
	plural.ValidateString(ctx, validator.StringRequest{Path: path.Root("name"), ConfigValue: types.StringValue("a")}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "at least 2 characters.") {
		t.Fatalf("plural diagnostics = %v, want 'at least 2 characters.'", resp.Diagnostics)
	}
}
