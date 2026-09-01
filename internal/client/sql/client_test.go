package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func TestNewWithoutTokenIsUnavailable(t *testing.T) {
	client, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if client.Available() {
		t.Fatal("client without token should not be available")
	}
	if err := client.Exec(context.Background(), "SELECT 1"); err != ErrMissingToken {
		t.Fatalf("Exec error = %v, want ErrMissingToken", err)
	}
	if err := client.AttachDatabase(context.Background(), ""); err != nil {
		t.Fatalf("empty AttachDatabase error = %v", err)
	}
}

func TestNewUsesSingleConnectionPool(t *testing.T) {
	client, err := New(context.Background(), Config{Token: "dummy"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("closing client: %v", err)
		}
	}()

	if got, want := client.db.Stats().MaxOpenConnections, 1; got != want {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, want)
	}
}

func TestContextConnectorInitializesWithCurrentConnectContext(t *testing.T) {
	duckdbConnector, err := duckdb.NewConnector(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	seen := make([]string, 0, 2)
	connector := &contextConnector{Connector: duckdbConnector, initialize: func(ctx context.Context, execer driver.ExecerContext) error {
		seen = append(seen, ctx.Value(contextKey{}).(string))
		_, err := execer.ExecContext(ctx, "SELECT 1", nil)
		return err
	}}
	db := stdsql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	firstCtx := context.WithValue(context.Background(), contextKey{}, "first")
	if _, err := db.ExecContext(firstCtx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(1)
	secondCtx := context.WithValue(context.Background(), contextKey{}, "second")
	if _, err := db.ExecContext(secondCtx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	if got, want := fmt.Sprint(seen), "[first second]"; got != want {
		t.Fatalf("initializer contexts = %s, want %s", got, want)
	}
}

func TestRedactToken(t *testing.T) {
	tests := map[string]struct {
		query string
		token string
		want  string
	}{
		"plain": {
			query: "SET motherduck_token = 'secret-token'",
			token: "secret-token",
			want:  "SET motherduck_token = '<redacted>'",
		},
		"quoted token": {
			query: "SET motherduck_token = 'secret''token'",
			token: "secret'token",
			want:  "SET motherduck_token = '<redacted>'",
		},
		"quoted token in error": {
			query: "parser error near secret''token",
			token: "secret'token",
			want:  "parser error near <redacted>",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := redactToken(tc.query, tc.token); got != tc.want {
				t.Fatalf("redactToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeValue(t *testing.T) {
	ts := time.Date(2026, 6, 19, 10, 11, 12, 13, time.UTC)
	tests := map[string]struct {
		value            any
		databaseTypeName string
		want             any
	}{
		"nil":        {value: nil, want: nil},
		"bytes":      {value: []byte("duck"), want: "6475636b"},
		"uuid bytes": {value: []byte{0xfd, 0xd4, 0x82, 0xf5, 0x74, 0x0b, 0x4e, 0x96, 0xb2, 0x58, 0x27, 0x02, 0xd4, 0xa6, 0x99, 0x45}, databaseTypeName: "UUID", want: "fdd482f5-740b-4e96-b258-2702d4a69945"},
		"time":       {value: ts, want: "2026-06-19T10:11:12.000000013Z"},
		"int":        {value: int64(7), want: int64(7)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeValue(tc.value, tc.databaseTypeName); got != tc.want {
				t.Fatalf("normalizeValue() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
