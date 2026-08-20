package tfvalidators

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func Int64Min(name string, min int64) validator.Int64 {
	return int64MinValidator{name: name, min: min}
}

func Int64Range(name string, min, max int64) validator.Int64 {
	return int64RangeValidator{name: name, min: min, max: max}
}

type int64MinValidator struct {
	name string
	min  int64
}

type int64RangeValidator struct {
	name string
	min  int64
	max  int64
}

func (v int64RangeValidator) Description(context.Context) string {
	return fmt.Sprintf("must be between %d and %d", v.min, v.max)
}

func (v int64RangeValidator) MarkdownDescription(context.Context) string {
	return fmt.Sprintf("must be between `%d` and `%d`", v.min, v.max)
}

func (v int64RangeValidator) ValidateInt64(ctx context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueInt64()
	if value < v.min || value > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("Value must be between %d and %d.", v.min, v.max))
	}
}

func (v int64MinValidator) Description(context.Context) string {
	return fmt.Sprintf("must be at least %d", v.min)
}

func (v int64MinValidator) MarkdownDescription(context.Context) string {
	return fmt.Sprintf("must be at least `%d`", v.min)
}

func (v int64MinValidator) ValidateInt64(ctx context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueInt64() < v.min {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("Value must be at least %d.", v.min))
	}
}
