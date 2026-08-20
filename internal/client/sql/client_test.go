package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
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

func TestIntegrationCreateQueryDropDatabase(t *testing.T) {
	if os.Getenv("MD_TF_ACC") == "" {
		t.Skip("set MD_TF_ACC=1 to run live SQL integration tests")
	}
	token := os.Getenv("MOTHERDUCK_TOKEN")
	if strings.TrimSpace(token) == "" {
		t.Skip("set MOTHERDUCK_TOKEN to run live SQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := New(ctx, Config{
		Token:           token,
		CustomUserAgent: "terraform-provider-motherduck-integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}

	database := fmt.Sprintf("tf_go_integration_%d", time.Now().UTC().UnixNano())
	quotedDatabase := sqlbuild.QuoteIdentifier(database)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := client.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+quotedDatabase+" CASCADE"); err != nil {
			t.Errorf("cleanup database %q: %v", database, err)
		}
		if err := client.Close(); err != nil {
			t.Errorf("close SQL client: %v", err)
		}
	})

	if err := client.Exec(ctx, "CREATE DATABASE "+quotedDatabase); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, "CREATE SCHEMA "+quotedDatabase+".app"); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, "CREATE TABLE "+quotedDatabase+".app.facts (id INTEGER, label VARCHAR)"); err != nil {
		t.Fatal(err)
	}
	if err := client.Exec(ctx, "INSERT INTO "+quotedDatabase+".app.facts VALUES (1, 'created from go integration')"); err != nil {
		t.Fatal(err)
	}

	rowsJSON, err := client.QueryRowsJSON(ctx, "SELECT id, label FROM "+quotedDatabase+".app.facts ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rowsJSON, `"label":"created from go integration"`) {
		t.Fatalf("rowsJSON = %s", rowsJSON)
	}

	exists, err := client.Exists(ctx, "SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?", database)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("database %q not found in MD_INFORMATION_SCHEMA.DATABASES", database)
	}
}

func TestIntegrationReconnectAfterInitialOperationContextEnds(t *testing.T) {
	if os.Getenv("MD_TF_ACC") == "" {
		t.Skip("set MD_TF_ACC=1 to run live SQL integration tests")
	}
	token := os.Getenv("MOTHERDUCK_TOKEN")
	if strings.TrimSpace(token) == "" {
		t.Skip("set MOTHERDUCK_TOKEN to run live SQL integration tests")
	}

	initialCtx, cancelInitial := context.WithTimeout(context.Background(), 2*time.Minute)
	client, err := New(initialCtx, Config{
		Token:           token,
		CustomUserAgent: "terraform-provider-motherduck-reconnect-test",
	})
	if err != nil {
		cancelInitial()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("closing client: %v", err)
		}
	})

	if err := client.Exec(initialCtx, "SELECT 1"); err != nil {
		cancelInitial()
		t.Fatal(err)
	}
	cancelInitial()

	// Closing the idle connection forces database/sql to invoke the connector's
	// initializer again during the next Terraform-style operation.
	client.db.SetMaxIdleConns(0)
	client.db.SetMaxIdleConns(1)

	reconnectCtx, cancelReconnect := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelReconnect()
	if err := client.Exec(reconnectCtx, "SELECT 1"); err != nil {
		t.Fatalf("reconnect after initial operation context ended: %v", err)
	}
}
