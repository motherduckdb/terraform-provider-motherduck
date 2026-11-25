// Package tokencontext validates the key/value context that a MotherDuck
// short-lived token carries. Secure view policies read it with
// session_context(key). The rules mirror the MotherDuck API, so a bad context
// fails at plan time instead of at apply time.
package tokencontext

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	// MaxKeys is the largest number of keys MotherDuck accepts.
	MaxKeys = 64
	// MaxBytes caps the UTF-8 bytes of the lowercased keys and the values.
	MaxBytes = 4096
	// SessionNameKey is the key whose value names the token's session and
	// routes its requests.
	SessionNameKey = "name"
)

var (
	keyPattern         = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
	sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Description documents the rules for schema descriptions.
const Description = "Keys may contain letters, digits, `_` and `.`. MotherDuck lowercases them, so two keys that differ only in letter case are rejected. At most 64 keys and 4096 bytes of keys and values in total. Values must not contain a NUL character."

// Validate reports the first rule the context breaks, checking keys in sorted
// order so the error does not depend on map iteration.
func Validate(values map[string]string) error {
	return validate(values, nil)
}

// validate checks values. Keys in unknown have a value that is not known yet,
// so only the key rules apply to them and the size limit is not checked.
func validate(values map[string]string, unknown map[string]bool) error {
	if len(values) > MaxKeys {
		return fmt.Errorf("context may have at most %d keys, got %d", MaxKeys, len(values))
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]string, len(keys))
	total := 0
	for _, key := range keys {
		if !keyPattern.MatchString(key) {
			return fmt.Errorf("context key %q may only contain letters, digits, '_' and '.'", key)
		}
		lower := strings.ToLower(key)
		if lower == "__proto__" {
			return errors.New("the context key __proto__ cannot be set")
		}
		if other, ok := seen[lower]; ok {
			return fmt.Errorf("context keys %q and %q differ only in letter case. MotherDuck lowercases keys, so set only one of them", other, key)
		}
		seen[lower] = key
		if unknown[key] {
			continue
		}
		value := values[key]
		if !utf8.ValidString(value) {
			return fmt.Errorf("the value of context key %q is not valid UTF-8", key)
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("the value of context key %q contains a NUL character", key)
		}
		if lower == SessionNameKey && !sessionNamePattern.MatchString(value) {
			return fmt.Errorf("the value of context key %q must be 1 to 64 characters of A-Z, a-z, 0-9, '.', '_' and '-', starting with a letter or digit", key)
		}
		total += len(lower) + len(value)
	}
	if len(unknown) == 0 && total > MaxBytes {
		return fmt.Errorf("context keys and values may total at most %d bytes, got %d", MaxBytes, total)
	}
	return nil
}

// HasSessionName reports whether a context map sets the session name. Keys
// are case-insensitive, and a map that is not known yet sets none.
func HasSessionName(value types.Map) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	for key := range value.Elements() {
		if strings.EqualFold(key, SessionNameKey) {
			return true
		}
	}
	return false
}

// FromMap returns the elements of a known map of strings after checking them
// against the MotherDuck rules. It returns nil for a null map.
func FromMap(value types.Map) (map[string]string, error) {
	if value.IsNull() {
		return nil, nil
	}
	if value.IsUnknown() {
		return nil, errors.New("context is not known yet")
	}
	result := make(map[string]string, len(value.Elements()))
	for key, element := range value.Elements() {
		str, ok := element.(types.String)
		if !ok {
			return nil, fmt.Errorf("context key %q does not hold a string", key)
		}
		if str.IsUnknown() {
			return nil, fmt.Errorf("the value of context key %q is not known yet", key)
		}
		if str.IsNull() {
			return nil, fmt.Errorf("the value of context key %q must not be null", key)
		}
		result[key] = str.ValueString()
	}
	if err := Validate(result); err != nil {
		return nil, err
	}
	return result, nil
}

// Validators returns the plan-time validators for a context map attribute.
func Validators() []validator.Map {
	return []validator.Map{mapValidator{}}
}

type mapValidator struct{}

func (mapValidator) Description(context.Context) string {
	return fmt.Sprintf("keys of letters, digits, '_' and '.', at most %d keys and %d bytes", MaxKeys, MaxBytes)
}

func (v mapValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (mapValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	values := map[string]string{}
	unknown := map[string]bool{}
	for key, element := range req.ConfigValue.Elements() {
		str, ok := element.(types.String)
		if !ok {
			return
		}
		if str.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck token context", fmt.Sprintf("The value of context key %q must not be null.", key))
			return
		}
		if str.IsUnknown() {
			unknown[key] = true
			values[key] = ""
			continue
		}
		values[key] = str.ValueString()
	}
	if err := validate(values, unknown); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck token context", err.Error()+".")
	}
}
