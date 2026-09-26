package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
)

func newEmbeddedClient(t *testing.T) *Client {
	t.Helper()
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return &Client{db: db}
}

func TestQueuedOperationObservesContextDeadline(t *testing.T) {
	client := newEmbeddedClient(t)
	if err := client.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			client.release()
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	errs := map[string]error{
		"exec":   client.Exec(ctx, "SELECT 1"),
		"scalar": func() error { _, err := client.ScalarString(ctx, "SELECT 1"); return err }(),
		"exists": func() error { _, err := client.Exists(ctx, "SELECT 1"); return err }(),
		"rows":   func() error { _, err := client.QueryRowsJSON(ctx, "SELECT 1"); return err }(),
		"row":    func() error { var v int; return client.QueryRow(ctx, "SELECT 1").Scan(&v) }(),
		"use": client.WithDatabaseUse(ctx, "memory", func(func(string, ...any) error) error {
			return nil
		}),
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("queued operations waited %s past their deadline", elapsed)
	}
	for name, err := range errs {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s error = %v, want context.DeadlineExceeded", name, err)
		}
	}

	client.release()
	released = true
	if err := client.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatalf("Exec after release: %v", err)
	}
}

func TestRowScanReleasesAdmissionOnce(t *testing.T) {
	client := newEmbeddedClient(t)
	row := client.QueryRow(t.Context(), "SELECT 1")
	var value int
	if err := row.Scan(&value); err != nil {
		t.Fatal(err)
	}
	// A second Scan must not release admission held by another operation.
	_ = row.Scan(&value)
	if err := client.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := client.Exec(ctx, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Exec while admission is held = %v, want context.DeadlineExceeded", err)
	}
}

func TestWithDatabaseUseDiscardsConnectionWhenRestoreFails(t *testing.T) {
	client := newEmbeddedClient(t)
	for _, query := range []string{"ATTACH ':memory:' AS previous", "ATTACH ':memory:' AS other", "USE previous"} {
		if err := client.Exec(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	err := client.WithDatabaseUse(t.Context(), "other", func(exec func(string, ...any) error) error {
		return exec("DETACH previous")
	})
	if err == nil || !strings.Contains(err.Error(), "restore previous database") {
		t.Fatalf("error = %v, want restore failure", err)
	}
	current, err := client.ScalarString(t.Context(), "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	if current == "other" {
		t.Fatal("pooled connection kept the temporary USE target after a failed restore")
	}
}

func TestWithDatabaseUseDiscardsConnectionAfterPanic(t *testing.T) {
	client := newEmbeddedClient(t)
	if err := client.Exec(t.Context(), "ATTACH ':memory:' AS other"); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = client.WithDatabaseUse(t.Context(), "other", func(func(string, ...any) error) error {
			panic("callback failed")
		})
	}()
	current, err := client.ScalarString(t.Context(), "SELECT current_database()")
	if err != nil {
		t.Fatal(err)
	}
	if current == "other" {
		t.Fatal("pooled connection kept the temporary USE target after a panic")
	}
}

func TestReplacementConnectionSelectsBootDefaultDatabase(t *testing.T) {
	duckdbConnector, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	initializer := &oneTimeInitializer{queries: []string{"ATTACH ':memory:' AS warehouse", "USE warehouse"}}
	db := stdsql.OpenDB(&contextConnector{Connector: duckdbConnector, initialize: initializer.run})
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	client := &Client{db: db}
	for attempt := 0; attempt < 2; attempt++ {
		current, err := client.ScalarString(t.Context(), "SELECT current_database()")
		if err != nil {
			t.Fatal(err)
		}
		if current != "warehouse" {
			t.Fatalf("attempt %d current_database() = %q, want warehouse", attempt, current)
		}
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}
}

func TestBootQueriesUseTrimmedToken(t *testing.T) {
	queries := bootQueries("token-value", Config{Database: "analytics"})
	if got, want := queries[2], "SET motherduck_token = 'token-value'"; got != want {
		t.Fatalf("token query = %q, want %q", got, want)
	}
	client, err := New(t.Context(), Config{Token: " \n\t"})
	if err != nil {
		t.Fatal(err)
	}
	if client.Available() {
		t.Fatal("whitespace token should not create an available client")
	}
}

func TestNULQueryIsRejectedWithoutEchoingStatement(t *testing.T) {
	client := newEmbeddedClient(t)
	query := "SELECT 'secret-prefix\x00suffix'"
	errs := []error{
		client.Exec(t.Context(), query),
		func() error { _, err := client.ScalarString(t.Context(), query); return err }(),
		func() error { _, err := client.Exists(t.Context(), query); return err }(),
		func() error { _, err := client.QueryRowsJSON(t.Context(), query); return err }(),
		func() error { var v string; return client.QueryRow(t.Context(), query).Scan(&v) }(),
		client.WithDatabaseUse(t.Context(), "memory", func(exec func(string, ...any) error) error {
			return exec(query)
		}),
	}
	for i, err := range errs {
		if !errors.Is(err, errNULQuery) {
			t.Errorf("operation %d error = %v, want errNULQuery", i, err)
			continue
		}
		if strings.Contains(err.Error(), "secret-prefix") {
			t.Errorf("operation %d echoed the statement: %v", i, err)
		}
	}
	if err := client.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatalf("admission leaked after rejection: %v", err)
	}
}

func TestQueryRowsJSONNormalizesNestedValues(t *testing.T) {
	client := newEmbeddedClient(t)
	got, err := client.QueryRowsJSON(t.Context(), `SELECT
		['fdd482f5-740b-4e96-b258-2702d4a69945'::UUID] AS uuids,
		{'id': 'fdd482f5-740b-4e96-b258-2702d4a69945'::UUID, 'amount': 1.5::DECIMAL(10,2), 'wait': INTERVAL 7 DAY, 'raw': '\xAA'::BLOB} AS details,
		MAP {'k': 12.25::DECIMAL(10,2)} AS amounts,
		12345678901234567.89::DECIMAL(38,2) AS big_amount,
		INTERVAL 7 DAY AS retention,
		[INTERVAL 1 DAY] AS waits,
		['\xAA'::BLOB] AS blobs,
		union_value(id := 'fdd482f5-740b-4e96-b258-2702d4a69945'::UUID) AS tagged`)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"amounts":{"k":12.25},"big_amount":12345678901234567.89,"blobs":["aa"],"details":{"amount":1.5,"id":"fdd482f5-740b-4e96-b258-2702d4a69945","raw":"aa","wait":"7 days"},"retention":"7 days","tagged":{"value":"fdd482f5-740b-4e96-b258-2702d4a69945","tag":"id"},"uuids":["fdd482f5-740b-4e96-b258-2702d4a69945"],"waits":["1 day"]}]`
	if got != want {
		t.Fatalf("rows =\n%s\nwant\n%s", got, want)
	}
	var decoded []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestQueryRowsJSONKeepsLastDuplicateFoldedColumn(t *testing.T) {
	client := newEmbeddedClient(t)
	got, err := client.QueryRowsJSON(t.Context(), `SELECT 1 AS "X", 2 AS "x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `[{"x":2}]` {
		t.Fatalf("rows = %s, want the later duplicate column to win", got)
	}
}

func TestFormatIntervalMatchesDuckDBCast(t *testing.T) {
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	expressions := []string{
		"INTERVAL 7 DAY",
		"INTERVAL 1 DAY",
		"INTERVAL 0 DAY",
		"INTERVAL 1 MONTH",
		"INTERVAL 12 MONTH",
		"INTERVAL 14 MONTH",
		"INTERVAL 100 HOUR",
		"INTERVAL 1000000 HOUR",
		"INTERVAL 1 MICROSECOND",
		"INTERVAL 1 MILLISECOND",
		"INTERVAL 1 DAY + INTERVAL 1 SECOND",
		"INTERVAL '1 year 2 months 3 days 04:05:06.789'",
		"INTERVAL '-1 year'",
		"INTERVAL '-14 months'",
		"INTERVAL '-1 month'",
		"INTERVAL '-3 days'",
		"INTERVAL '-1 hour'",
		"INTERVAL '-00:00:00.5'",
		"INTERVAL '1 year -2 months'",
		"INTERVAL '1 day -01:00:00'",
		"INTERVAL '-1 year -1 day -00:00:01'",
		"INTERVAL '0.123456 seconds'",
	}
	for _, expression := range expressions {
		t.Run(expression, func(t *testing.T) {
			var value duckdb.Interval
			var want string
			if err := db.QueryRowContext(t.Context(), "SELECT "+expression+", ("+expression+")::VARCHAR").Scan(&value, &want); err != nil {
				t.Fatal(err)
			}
			if got := formatInterval(value); got != want {
				t.Fatalf("formatInterval(%+v) = %q, DuckDB cast = %q", value, got, want)
			}
		})
	}
}

func TestParseColumnType(t *testing.T) {
	parsed := parseColumnType(`STRUCT("a ""b" UUID, c MAP(VARCHAR, UUID[]), d DECIMAL(10,2))[2][]`)
	inner := parsed.element().element()
	if !inner.is("STRUCT") {
		t.Fatalf("inner type = %+v, want STRUCT", inner)
	}
	if !inner.field(`a "b`).is("UUID") {
		t.Fatalf("quoted field type = %+v", inner.field(`a "b`))
	}
	if !inner.field("C").mapValue().element().is("UUID") {
		t.Fatalf("map value type = %+v", inner.field("c").mapValue())
	}
	if !inner.field("d").is("DECIMAL") {
		t.Fatalf("decimal field type = %+v", inner.field("d"))
	}
}
