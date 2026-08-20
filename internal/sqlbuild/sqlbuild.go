package sqlbuild

import (
	"fmt"
	"sort"
	"strings"
)

func QuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func QuoteQualifiedIdentifier(parts ...string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, QuoteIdentifier(part))
	}
	return strings.Join(quoted, ".")
}

func StringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func BoolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func ListLiteral(values []string) string {
	if len(values) == 0 {
		return "[]::VARCHAR[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, StringLiteral(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func MapLiteral(values map[string]string) string {
	if len(values) == 0 {
		return "MAP {}::MAP(VARCHAR, VARCHAR)"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s: %s", StringLiteral(key), StringLiteral(values[key])))
	}
	return "MAP {" + strings.Join(pairs, ", ") + "}"
}

func Options(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return " (" + strings.Join(clean, ", ") + ")"
}

func NamedArgs(args map[string]string) string {
	if len(args) == 0 {
		return "()"
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s := %s", key, args[key]))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
