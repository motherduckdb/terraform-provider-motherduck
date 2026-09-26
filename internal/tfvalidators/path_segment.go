package tfvalidators

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// RESTPathSegment rejects values that URL path normalization would resolve
// away. Escaping leaves "." and ".." unchanged, so a proxy or server that
// normalizes paths could route such a segment to a different endpoint.
func RESTPathSegment(name string) validator.String {
	return restPathSegmentValidator{name: name}
}

// ValidateRESTPathSegmentValue reports whether value is safe to use as one
// REST path segment.
func ValidateRESTPathSegmentValue(value, subject string) (string, bool) {
	if value == "." || value == ".." {
		return subject + ` must not be "." or "..".`, false
	}
	return "", true
}

type restPathSegmentValidator struct{ name string }

func (restPathSegmentValidator) Description(context.Context) string {
	return `must not be "." or ".."`
}

func (restPathSegmentValidator) MarkdownDescription(context.Context) string {
	return "must not be `.` or `..`"
}

func (v restPathSegmentValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if detail, ok := ValidateRESTPathSegmentValue(req.ConfigValue.ValueString(), "Value"); !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, detail)
	}
}
