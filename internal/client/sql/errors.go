package sql

import (
	"errors"
	"strings"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

var ErrMissingToken = errors.New("MotherDuck SQL operations require token or MOTHERDUCK_TOKEN")

// IsUnsupportedCommand reports whether err indicates the current MotherDuck
// SQL session does not understand or implement the attempted statement, for
// example a SHOW command that the connected server does not support.
func IsUnsupportedCommand(err error) bool {
	if err == nil {
		return false
	}
	var duckErr *duckdb.Error
	if errors.As(err, &duckErr) &&
		(duckErr.Type == duckdb.ErrorTypeParser || duckErr.Type == duckdb.ErrorTypeNotImplemented) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "syntax error") ||
		strings.Contains(msg, "not implemented") ||
		strings.Contains(msg, "unrecognized") ||
		strings.Contains(msg, "unsupported")
}
