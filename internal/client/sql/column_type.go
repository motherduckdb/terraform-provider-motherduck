package sql

import "strings"

// columnType is a parsed DuckDB type name such as UUID[],
// STRUCT("a" UUID, "b" DECIMAL(10,2)), or MAP(VARCHAR, UUID). It carries
// only the structure needed to normalize scanned values.
type columnType struct {
	name   string
	elem   *columnType
	fields map[string]*columnType
	key    *columnType
	value  *columnType
}

func (t *columnType) is(name string) bool {
	return t != nil && t.name == name
}

func (t *columnType) element() *columnType {
	if t == nil {
		return nil
	}
	return t.elem
}

func (t *columnType) field(name string) *columnType {
	if t == nil || t.fields == nil {
		return nil
	}
	if field, ok := t.fields[name]; ok {
		return field
	}
	// DuckDB field names are case insensitive.
	for fieldName, field := range t.fields {
		if strings.EqualFold(fieldName, name) {
			return field
		}
	}
	return nil
}

func (t *columnType) mapKey() *columnType {
	if t == nil {
		return nil
	}
	return t.key
}

func (t *columnType) mapValue() *columnType {
	if t == nil {
		return nil
	}
	return t.value
}

// parseColumnType parses a DuckDB DatabaseTypeName. Unrecognized shapes parse
// to a plain named type, which normalizes like the previous untyped behavior.
func parseColumnType(name string) *columnType {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.HasSuffix(name, "]") {
		if open := lastTopLevelIndex(name, '['); open > 0 {
			return &columnType{name: "LIST", elem: parseColumnType(name[:open])}
		}
	}
	open := strings.IndexByte(name, '(')
	if open < 0 || !strings.HasSuffix(name, ")") {
		return &columnType{name: strings.ToUpper(name)}
	}
	t := &columnType{name: strings.ToUpper(strings.TrimSpace(name[:open]))}
	inner := name[open+1 : len(name)-1]
	switch t.name {
	case "STRUCT", "UNION":
		t.fields = make(map[string]*columnType)
		for _, part := range splitTopLevel(inner) {
			fieldName, fieldType := splitFieldName(part)
			if fieldName != "" {
				t.fields[fieldName] = parseColumnType(fieldType)
			}
		}
	case "MAP":
		if parts := splitTopLevel(inner); len(parts) == 2 {
			t.key = parseColumnType(parts[0])
			t.value = parseColumnType(parts[1])
		}
	}
	return t
}

// lastTopLevelIndex returns the index of the last target byte outside
// parentheses, brackets, and double-quoted identifiers, or -1.
func lastTopLevelIndex(s string, target byte) int {
	depth := 0
	quoted := false
	last := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted:
			if c == '"' {
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == target && depth == 0:
			last = i
			depth++
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		}
	}
	return last
}

// splitTopLevel splits s on commas outside parentheses, brackets, and
// double-quoted identifiers.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	quoted := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted:
			if c == '"' {
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// splitFieldName splits a STRUCT or UNION member such as "a b" UUID or
// name VARCHAR into its unquoted name and type text.
func splitFieldName(part string) (string, string) {
	part = strings.TrimSpace(part)
	if strings.HasPrefix(part, `"`) {
		var name strings.Builder
		for i := 1; i < len(part); i++ {
			if part[i] != '"' {
				name.WriteByte(part[i])
				continue
			}
			if i+1 < len(part) && part[i+1] == '"' {
				name.WriteByte('"')
				i++
				continue
			}
			return name.String(), strings.TrimSpace(part[i+1:])
		}
		return "", ""
	}
	name, rest, ok := strings.Cut(part, " ")
	if !ok {
		return "", ""
	}
	return name, strings.TrimSpace(rest)
}
