//go:build acceptance

package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type snapshotLiveReadbackFailure struct {
	providerctx.SQLClient
	failRead bool
}

func (c *snapshotLiveReadbackFailure) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if c.failRead && strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS") {
		return errRowScanner{err: errors.New("injected snapshot catalog readback failure")}
	}
	return c.SQLClient.QueryRow(ctx, query, args...)
}

func TestLiveSnapshotFailedCreateDestroyWithoutRefresh(t *testing.T) {
	token := os.Getenv("MOTHERDUCK_TOKEN")
	if token == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	client, err := mdsql.New(ctx, mdsql.Config{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	database := fmt.Sprintf("tf_snapshot_recovery_%d", time.Now().UnixNano())
	name := "tf_recovery_snapshot"
	if err := client.Exec(ctx, "CREATE DATABASE "+sqlbuild.QuoteIdentifier(database)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := client.WithDatabaseUse(cleanupCtx, database, func(exec func(string, ...any) error) error {
			return exec("ALTER SNAPSHOT " + sqlbuild.QuoteQualifiedIdentifier(database, name) + " SET snapshot_name = ''")
		}); err != nil && !isSnapshotNotFound(err) {
			t.Errorf("snapshot cleanup: %v", err)
		}
		if err := client.Exec(cleanupCtx, "DROP DATABASE "+sqlbuild.QuoteIdentifier(database)+" CASCADE"); err != nil {
			t.Errorf("database cleanup: %v", err)
		}
		var count string
		if err := client.QueryRow(cleanupCtx, `SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?`, database).Scan(&count); err != nil || count != "0" {
			t.Errorf("database cleanup audit count=%q err=%v", count, err)
		}
	}()
	backend := &snapshotLiveReadbackFailure{SQLClient: client, failRead: true}
	probe, err := mdsql.New(ctx, mdsql.Config{Token: token, Database: database, AttachMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	}()
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: backend}}}
	state := snapshotTestState(t, r)
	var model snapshotModel
	if d := state.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	model.Database = types.StringValue(database)
	model.Name = types.StringValue(name)
	model.ID = types.StringUnknown()
	model.CreatedTS = types.StringUnknown()
	plan := tfsdk.Plan{Schema: state.Schema}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: state}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() || !created.State.Raw.IsFullyKnown() {
		t.Fatalf("expected failed readback with known cleanup state: %v", created.Diagnostics)
	}
	var count string
	query := `SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_name = ?`
	if err := probe.QueryRow(ctx, query, database, name).Scan(&count); err != nil || count != "1" {
		t.Fatalf("independent create audit count=%q err=%v", count, err)
	}
	backend.failRead = false
	deleted := resource.DeleteResponse{State: created.State}
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	if err := probe.QueryRow(ctx, query, database, name).Scan(&count); err != nil || count != "0" {
		t.Fatalf("destroy left a named snapshot: count=%q err=%v", count, err)
	}
}
