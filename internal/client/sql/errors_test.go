package sql

import (
	"errors"
	"fmt"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func TestIsUnsupportedCommand(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"nil":         {err: nil, want: false},
		"parser type": {err: &duckdb.Error{Type: duckdb.ErrorTypeParser, Msg: "Parser Error: unexpected token"}, want: true},
		"not implemented type": {
			err:  &duckdb.Error{Type: duckdb.ErrorTypeNotImplemented, Msg: "Not implemented Error: SHOW ALL ROLES"},
			want: true,
		},
		"wrapped parser type": {
			err:  fmt.Errorf("read roles: %w", &duckdb.Error{Type: duckdb.ErrorTypeParser, Msg: "Parser Error: unexpected token"}),
			want: true,
		},
		"catalog type": {
			err:  &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: role does not exist"},
			want: false,
		},
		"syntax error message":    {err: errors.New("syntax error at or near \"SHOW\""), want: true},
		"not implemented message": {err: errors.New("this command is not implemented"), want: true},
		"unrecognized message":    {err: errors.New("unrecognized statement type"), want: true},
		"unsupported message":     {err: errors.New("unsupported command in this session"), want: true},
		"other error":             {err: errors.New("connection reset"), want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := IsUnsupportedCommand(tc.err); got != tc.want {
				t.Fatalf("IsUnsupportedCommand(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
