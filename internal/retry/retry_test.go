package retry

import (
	"context"
	stdsql "database/sql"
	"errors"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func TestSQLRetriesTransientErrors(t *testing.T) {
	transientErr := errors.New("UNAVAILABLE: failed to connect to all addresses")
	attempts := 0

	err := sql(context.Background(), 0, func() error {
		attempts++
		if attempts < 3 {
			return transientErr
		}
		return nil
	})

	if err != nil {
		t.Fatalf("sql() error = %v, want nil", err)
	}
	if attempts != 3 {
		t.Fatalf("sql() attempts = %d, want 3", attempts)
	}
}

func TestSQLStopsAfterFourAttempts(t *testing.T) {
	wantErr := errors.New("request timed out")
	attempts := 0

	err := sql(context.Background(), 0, func() error {
		attempts++
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("sql() error = %v, want %v", err, wantErr)
	}
	if attempts != sqlMaxAttempts {
		t.Fatalf("sql() attempts = %d, want %d", attempts, sqlMaxAttempts)
	}
}

func TestSQLDoesNotRetryTerminalErrors(t *testing.T) {
	tests := map[string]error{
		"no rows": stdsql.ErrNoRows,
		"parser":  errors.New("Parser Error: syntax error"),
		"missing catalog": &duckdb.Error{
			Type: duckdb.ErrorTypeCatalog,
			Msg:  "Catalog Error: table does not exist because the service is unavailable",
		},
	}

	for name, wantErr := range tests {
		t.Run(name, func(t *testing.T) {
			attempts := 0
			err := sql(context.Background(), 0, func() error {
				attempts++
				return wantErr
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("sql() error = %v, want %v", err, wantErr)
			}
			if attempts != 1 {
				t.Fatalf("sql() attempts = %d, want 1", attempts)
			}
		})
	}
}

func TestSQLStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0

	err := sql(ctx, time.Hour, func() error {
		attempts++
		return errors.New("network is unreachable")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sql() error = %v, want context canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("sql() attempts = %d, want 1", attempts)
	}
}

func TestIsTransientMotherDuckError(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"deadline":    {err: errors.New("DEADLINE_EXCEEDED, RPC 'SETUP_PLAN_FRAGMENTS'"), want: true},
		"unavailable": {err: errors.New("UNAVAILABLE: failed to connect to all addresses"), want: true},
		"other":       {err: errors.New("Parser Error: syntax error"), want: false},
		"nil":         {err: nil, want: false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := isTransientMotherDuckError(test.err); got != test.want {
				t.Fatalf("isTransientMotherDuckError() = %t, want %t", got, test.want)
			}
		})
	}
}
