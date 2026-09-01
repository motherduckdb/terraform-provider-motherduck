//go:build acceptance

package sql

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

func TestIntegrationCreateQueryDropDatabase(t *testing.T) {
	requireLiveSQL(t)
	token := os.Getenv("MOTHERDUCK_TOKEN")

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
	requireLiveSQL(t)
	token := os.Getenv("MOTHERDUCK_TOKEN")

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

	client.db.SetMaxIdleConns(0)
	client.db.SetMaxIdleConns(1)

	reconnectCtx, cancelReconnect := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelReconnect()
	if err := client.Exec(reconnectCtx, "SELECT 1"); err != nil {
		t.Fatalf("reconnect after initial operation context ended: %v", err)
	}
}

func requireLiveSQL(t *testing.T) {
	t.Helper()
	if os.Getenv("MD_TF_ACC") != "1" {
		t.Fatal("MD_TF_ACC=1 is required for live SQL integration tests")
	}
	if strings.TrimSpace(os.Getenv("MOTHERDUCK_TOKEN")) == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for live SQL integration tests")
	}
}
