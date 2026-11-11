package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
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
	execer := &recordingExecer{attachFailures: 4, attachError: "Your MotherDuck databases are already attached"}
	queries := []string{"INSTALL motherduck", "ATTACH 'md:'"}
	initializer := &oneTimeInitializer{queries: queries, token: "test-token"}

	first := context.WithValue(context.Background(), contextKey{}, "first")
	if err := initializer.run(first, execer); err == nil {
		t.Fatal("first initialization should fail")
	}
	second := context.WithValue(context.Background(), contextKey{}, "second")
	if err := initializer.run(second, execer); err == nil {
		t.Fatal("second initialization should still fail")
	}
	third := context.WithValue(context.Background(), contextKey{}, "third")
	if err := initializer.run(third, execer); err != nil {
		t.Fatal(err)
	}
	if err := initializer.run(context.WithValue(context.Background(), contextKey{}, "fourth"), execer); err != nil {
		t.Fatal(err)
	}
	if initializer.next != 2 || !initializer.initialized {
		t.Fatalf("initializer state = next:%d initialized:%t, want 2,true", initializer.next, initializer.initialized)
	}
	if len(execer.queries) != 6 {
		t.Fatalf("bootstrap query count = %d, want 6 with no fourth-run replay", len(execer.queries))
	}
	if got := execer.contexts[len(execer.contexts)-1].Value(contextKey{}); got != "third" {
		t.Fatalf("retry context = %v, want third", got)
	}
}

func TestOneTimeInitializerDoesNotReplaySetupAfterAttachFailure(t *testing.T) {
	initializer := &oneTimeInitializer{queries: []string{"SETUP", "TOKEN", "ATTACH 'md:'"}, token: "test-token"}
	execer := &recordingExecer{attachFailures: 2, attachError: "Your MotherDuck databases are already attached"}
	if err := initializer.run(context.Background(), execer); err == nil {
		t.Fatal("first attach should fail")
	}
	if err := initializer.run(context.Background(), execer); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(execer.queries); got != "[SETUP TOKEN ATTACH 'md:' ATTACH IF NOT EXISTS 'md:' ATTACH 'md:']" {
		t.Fatalf("bootstrap queries = %s, want setup/token once and attach twice", got)
	}
}

func TestOneTimeInitializerDoesNotMaskInitialAttachError(t *testing.T) {
	initializer := &oneTimeInitializer{queries: []string{"ATTACH 'md:'"}, token: "test-token"}
	execer := &recordingExecer{attachFailures: 1, attachError: "authentication failed"}
	if err := initializer.run(context.Background(), execer); err == nil {
		t.Fatal("initial attach error should be returned")
	}
	if got := fmt.Sprint(execer.queries); got != "[ATTACH 'md:']" {
		t.Fatalf("bootstrap queries = %s, want no IF NOT EXISTS fallback", got)
	}
}

func TestOneTimeInitializerExplainsConnectionFailures(t *testing.T) {
	const connectErr = "IO Error: Request failed: Could not connect to MotherDuck. Please try again later or contact support@motherduck.com for help. (UNAVAILABLE, RPC 'CREATE_SLT', request id: 'id', msg: 'DNS resolution failed for api.motherduck.com:443')"
	initializer := &oneTimeInitializer{queries: []string{"ATTACH 'md:'"}, token: "test-token"}
	err := initializer.run(context.Background(), &recordingExecer{attachFailures: 1, attachError: connectErr})
	if err == nil || !strings.Contains(err.Error(), connectErr) || !strings.Contains(err.Error(), "rather than a token problem") {
		t.Fatalf("connection failure = %v, want the extension error followed by the network hint", err)
	}

	initializer = &oneTimeInitializer{queries: []string{"ATTACH 'md:'"}, token: "test-token"}
	err = initializer.run(context.Background(), &recordingExecer{attachFailures: 1, attachError: "Invalid Input Error: Your request is not authenticated. Please check your MotherDuck token."})
	if err == nil || strings.Contains(err.Error(), "token problem") {
		t.Fatalf("authentication failure = %v, want no network hint", err)
	}
}

type recordingExecer struct {
	contexts       []context.Context
	queries        []string
	attachFailures int
	attachError    string
}

func (r *recordingExecer) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	r.contexts = append(r.contexts, ctx)
	r.queries = append(r.queries, query)
	if strings.HasPrefix(query, "ATTACH") && r.attachFailures > 0 {
		r.attachFailures--
		return nil, errors.New(r.attachError)
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

func TestOneTimeInitializerExplainsRejectedToken(t *testing.T) {
	initializer := &oneTimeInitializer{queries: []string{"ATTACH 'md:'"}, token: "test-token"}
	execer := &recordingExecer{attachFailures: 1, attachError: "Invalid Error: Request failed: Your request is not authenticated. Please check your MotherDuck token. (Jwt is not in the form of Header.Payload.Signature with two dots and 3 sections, request id: 'abc')"}
	err := initializer.run(context.Background(), execer)
	if err == nil {
		t.Fatal("expected an authentication error")
	}
	got := err.Error()
	for _, want := range []string{"MotherDuck rejected the SQL token", "MOTHERDUCK_TOKEN", "Your request is not authenticated"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "boot query") || strings.Contains(got, "network") {
		t.Fatalf("authentication error reads like an internal or network failure: %q", got)
	}
}

// A default database that was dropped after boot must not break every later
// connection. The reconnect keeps the DuckDB default and stops selecting it.
func TestOneTimeInitializerSkipsDroppedDefaultDatabaseOnReconnect(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, useErr := db.Exec(`USE "dropped_default"`)
	if useErr == nil || !isMissingUseTarget(useErr) {
		t.Fatalf("DuckDB error for a missing USE target = %v, want it recognized as a missing database", useErr)
	}

	initializer := &oneTimeInitializer{initialized: true, defaultDatabase: "dropped_default", token: "test-token"}
	execer := &useFailingExecer{err: useErr}
	if err := initializer.run(context.Background(), execer); err != nil {
		t.Fatalf("reconnect after the default database was dropped = %v, want success", err)
	}
	if err := initializer.run(context.Background(), execer); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(execer.queries); got != `[USE "dropped_default"]` {
		t.Fatalf("reconnect queries = %s, want one USE attempt and none after it failed", got)
	}

	initializer = &oneTimeInitializer{initialized: true, defaultDatabase: "analytics", token: "test-token"}
	err = initializer.run(context.Background(), &useFailingExecer{err: errors.New("IO Error: connection reset")})
	if err == nil || !strings.Contains(err.Error(), "select default database on new connection") {
		t.Fatalf("reconnect with a transport failure = %v, want the USE error", err)
	}
}

type useFailingExecer struct {
	queries []string
	err     error
}

func (e *useFailingExecer) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	e.queries = append(e.queries, query)
	return nil, e.err
}

// A statement with no caller deadline is bounded by the client, and the error
// names that bound. A caller deadline is left alone.
func TestStatementDeadlineBoundsStatementsWithoutCallerDeadline(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	client := &Client{db: db, statementTimeout: 100 * time.Millisecond}
	const slow = "SELECT count(*) FROM range(100000000000) AS a(i) WHERE i % 7 = 3"

	checks := map[string]func() error{
		"Exec":          func() error { return client.Exec(context.Background(), slow) },
		"ScalarString":  func() error { _, err := client.ScalarString(context.Background(), slow); return err },
		"Exists":        func() error { _, err := client.Exists(context.Background(), slow); return err },
		"QueryRowsJSON": func() error { _, err := client.QueryRowsJSON(context.Background(), slow); return err },
		"QueryRow": func() error {
			var count int64
			return client.QueryRow(context.Background(), slow).Scan(&count)
		},
	}
	for name, run := range checks {
		started := time.Now()
		err := run()
		if err == nil || !strings.Contains(err.Error(), "did not finish within 100ms") {
			t.Errorf("%s error = %v, want the statement deadline named", name, err)
		}
		if elapsed := time.Since(started); elapsed > 20*time.Second {
			t.Errorf("%s took %s, want the statement canceled near its deadline", name, elapsed)
		}
	}

	// The client stays usable, and a fast statement is unaffected.
	if got, err := client.ScalarString(context.Background(), "SELECT 'ok'"); err != nil || got != "ok" {
		t.Fatalf("statement after a timeout = %q, %v", got, err)
	}

	callerCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = client.Exec(callerCtx, slow)
	if err == nil || strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("caller deadline error = %v, want the caller error without the client bound", err)
	}
}
