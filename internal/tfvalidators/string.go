package tfvalidators

import (
	"context"
	"fmt"
	"strings"

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
