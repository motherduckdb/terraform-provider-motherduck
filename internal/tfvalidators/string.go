package tfvalidators

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func StringLength(name string, min int, max int) validator.String {
	return stringLengthValidator{name: name, min: min, max: max}
}

type stringLengthValidator struct {
	name string
	min  int
	max  int
}

func (v stringLengthValidator) Description(context.Context) string {
	if v.max > 0 {
		return fmt.Sprintf("must be between %d and %d characters", v.min, v.max)
	}
	return fmt.Sprintf("must be at least %d character", v.min)
}

func (v stringLengthValidator) MarkdownDescription(context.Context) string {
	if v.max > 0 {
		return fmt.Sprintf("must be between `%d` and `%d` characters", v.min, v.max)
	}
	return fmt.Sprintf("must be at least `%d` character", v.min)
}

func (v stringLengthValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	length := len([]rune(value))
	if strings.TrimSpace(value) != "" && length >= v.min && (v.max == 0 || length <= v.max) {
		return
	}
	if v.max > 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("Value must be non-blank and between %d and %d characters.", v.min, v.max))
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("Value must be non-blank and at least %d character.", v.min))
}

// StringMaxUTF16Units limits a string to max UTF-16 code units, the length a
// JavaScript service measures.
func StringMaxUTF16Units(name string, max int) validator.String {
	return stringUTF16Validator{name: name, max: max}
}

type stringUTF16Validator struct {
	name string
	max  int
}

func (v stringUTF16Validator) Description(context.Context) string {
	return fmt.Sprintf("must be at most %d UTF-16 code units", v.max)
}

func (v stringUTF16Validator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v stringUTF16Validator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if units := len(utf16.Encode([]rune(req.ConfigValue.ValueString()))); units > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("%s must be at most %d UTF-16 code units, and characters such as emoji count as two. This value has %d.", v.name, v.max, units))
	}
}
