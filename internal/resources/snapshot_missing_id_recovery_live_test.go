//go:build acceptance

package resources

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

func TestLiveSnapshotMissingIDRecoveryAfterDatabaseDrop(t *testing.T) {
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
	database := fmt.Sprintf("tf_maintenance_orphan_%d", time.Now().UnixNano())
	anchor := database + "_anchor"
	name := "recovery_probe"
	var id string
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if id != "" {
			if err := client.WithDatabaseUse(cleanupCtx, anchor, func(exec func(string, ...any) error) error { return exec(snapshotUnnameSQL(id)) }); err != nil {
				t.Errorf("unname cleanup: %v", err)
			}
		}
		if id != "" {
			var named int
			if err := client.QueryRow(cleanupCtx, `SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_id::VARCHAR = ? AND coalesce(snapshot_name, '') != ''`, id).Scan(&named); err != nil || named != 0 {
				t.Errorf("snapshot fallback cleanup audit count=%d err=%v", named, err)
			}
		}
		for _, db := range []string{database, anchor} {
			if err := client.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+sqlbuild.QuoteIdentifier(db)+" CASCADE"); err != nil {
				t.Errorf("database cleanup: %v", err)
			}
		}
		var count int
		if err := client.QueryRow(cleanupCtx, `SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name IN (?, ?)`, database, anchor).Scan(&count); err != nil || count != 0 {
			t.Errorf("owned database cleanup count=%d err=%v", count, err)
		}
	}()
	for _, db := range []string{database, anchor} {
		if err := client.Exec(ctx, "CREATE DATABASE "+sqlbuild.QuoteIdentifier(db)); err != nil {
			t.Fatal(err)
		}
	}
	backend := &snapshotLiveReadbackFailure{SQLClient: client, failRead: true}
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
		t.Fatal("expected readback failure with known cleanup state")
	}
	if d := created.State.Get(ctx, &model); d.HasError() || !model.ID.IsNull() {
		t.Fatal("expected missing-ID state")
	}
	backend.failRead = false
	query := `SELECT snapshot_id::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_name = ?`
	if err := client.QueryRow(ctx, query, database, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	verifyImport := func() {
		t.Helper()
		imported := resource.ImportStateResponse{State: created.State}
		r.ImportState(ctx, resource.ImportStateRequest{ID: database + "." + name}, &imported)
		if imported.Diagnostics.HasError() {
			t.Fatal(imported.Diagnostics)
		}
		read := resource.ReadResponse{State: imported.State}
		r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
		if read.Diagnostics.HasError() {
			t.Fatal(read.Diagnostics)
		}
		var importedModel snapshotModel
		if d := read.State.Get(ctx, &importedModel); d.HasError() || importedModel.ID.ValueString() != id {
			t.Fatal("import failed to recover snapshot identity")
		}
	}
	verifyImport()
	if err := client.Exec(ctx, "DROP DATABASE "+sqlbuild.QuoteIdentifier(database)+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	probe, err := mdsql.New(ctx, mdsql.Config{Token: token, Database: anchor, AttachMode: "single"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := probe.QueryRow(ctx, `SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_name = ?`, database, name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("independent catalog matches after drop = %d, want 1", count)
	}
	verifyImport()
	refreshed := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &refreshed)
	if refreshed.Diagnostics.HasError() || refreshed.State.Raw.IsNull() {
		t.Fatal(refreshed.Diagnostics)
	}
	if d := refreshed.State.Get(ctx, &model); d.HasError() || model.ID.ValueString() != id {
		t.Fatal("refresh failed to recover retained snapshot identity")
	}
	if findWarning(refreshed.Diagnostics, "MotherDuck snapshot outlived its database") == nil {
		t.Fatal("expected retained snapshot warning")
	}
	// Exercise destroy without refresh using the original state with a null ID.
	var savedModel snapshotModel
	if d := created.State.Get(ctx, &savedModel); d.HasError() || !savedModel.ID.IsNull() {
		t.Fatal("the original cleanup state must still have a null ID")
	}
	deleted := resource.DeleteResponse{State: created.State}
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	if err := probe.QueryRow(ctx, `SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_id::VARCHAR = ? AND coalesce(snapshot_name, '') != ''`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("independent destroy audit count=%d err=%v", count, err)
	}
	id = ""
	t.Log("independent retained-name audit: 1 before destroy, 0 after destroy")
}
