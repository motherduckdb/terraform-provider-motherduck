package tfvalidators

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func UUID() validator.String {
	return uuidValidator{}
}

func ValidateUUIDValue(value, subject string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if value != trimmed {
		return subject + " must not include leading or trailing whitespace.", false
	}
	if !uuidPattern.MatchString(value) {
		return subject + " must be a UUID.", false
	}
	return "", true
}

type uuidValidator struct{}

func (uuidValidator) Description(context.Context) string {
	return "must be a UUID"
}

func (uuidValidator) MarkdownDescription(context.Context) string {
	return "must be a UUID"
}

func (uuidValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if detail, ok := ValidateUUIDValue(req.ConfigValue.ValueString(), "Value"); !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck UUID", detail)
	}
}
