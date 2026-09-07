package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func TestWithDatabaseUseRestoresAfterCancellation(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	client := &Client{db: db}
	if err := client.Exec(t.Context(), "ATTACH ':memory:' AS other"); err != nil {
		t.Fatal(err)
	}
	before, err := client.ScalarString(t.Context(), "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = client.WithDatabaseUse(ctx, "other", func(exec func(string, ...any) error) error {
		cancel()
		return exec("SELECT 1")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	after, err := client.ScalarString(t.Context(), "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("database leaked: got %q, want %q", after, before)
	}
}

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

func TestOneTimeInitializerRetriesFailureAndSkipsReconnectBootstrap(t *testing.T) {
	type contextKey struct{}
	execer := &recordingExecer{failAttachOnce: true}
	queries := []string{"INSTALL motherduck", "ATTACH"}
	initializer := &oneTimeInitializer{queries: queries, token: "test-token"}

	first := context.WithValue(context.Background(), contextKey{}, "first")
	if err := initializer.run(first, execer); err == nil {
		t.Fatal("first initialization should fail")
	}
	second := context.WithValue(context.Background(), contextKey{}, "second")
	if err := initializer.run(second, execer); err != nil {
		t.Fatal(err)
	}
	third := context.WithValue(context.Background(), contextKey{}, "third")
	if err := initializer.run(third, execer); err != nil {
		t.Fatal(err)
	}
	if initializer.next != 2 || !initializer.initialized {
		t.Fatalf("initializer state = next:%d initialized:%t, want 2,true", initializer.next, initializer.initialized)
	}
}

func TestOneTimeInitializerDoesNotReplaySetupAfterAttachFailure(t *testing.T) {
	initializer := &oneTimeInitializer{queries: []string{"SETUP", "TOKEN", "ATTACH"}, token: "test-token"}
	execer := &recordingExecer{failAttachOnce: true}
	if err := initializer.run(context.Background(), execer); err == nil {
		t.Fatal("first attach should fail")
	}
	if err := initializer.run(context.Background(), execer); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(execer.queries); got != "[SETUP TOKEN ATTACH ATTACH]" {
		t.Fatalf("bootstrap queries = %s, want setup/token once and attach twice", got)
	}
}

type recordingExecer struct {
	queries        []string
	failAttachOnce bool
}

func (r *recordingExecer) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	r.queries = append(r.queries, query)
	if query == "ATTACH" && r.failAttachOnce {
		r.failAttachOnce = false
		return nil, errors.New("attach canceled after remote attach")
	}
	return driver.RowsAffected(0), nil
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

func BenchmarkQueryRowsJSON(b *testing.B) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	client := &Client{db: db}
	query := `SELECT i AS "ID", 'row-' || i AS "NAME" FROM range(1000) t(i)`
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := client.QueryRowsJSON(b.Context(), query); err != nil {
			b.Fatal(err)
		}
	}
}

func TestQueryRowsJSONKeepsDistinctRows(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	client := &Client{db: db}
	got, err := client.QueryRowsJSON(t.Context(), `SELECT i AS "ID", CASE WHEN i = 1 THEN NULL ELSE 'row-' || i END AS "NAME" FROM range(3) t(i) ORDER BY i`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"id":0,"name":"row-0"},{"id":1,"name":null},{"id":2,"name":"row-2"}]`; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
}
