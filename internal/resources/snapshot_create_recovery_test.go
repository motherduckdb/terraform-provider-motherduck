package resources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

type snapshotCreateBackend struct {
	*snapshotOrphanBackend
	readError error
	readID    any
	matches   int
}

func (c *snapshotCreateBackend) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	if c.readError != nil {
		return errRowScanner{err: c.readError}
	}
	return snapshotIdentityRow{id: c.readID, matches: c.matches}
}

type snapshotIdentityRow struct {
	id      any
	matches int
}

func (r snapshotIdentityRow) Scan(dest ...any) error {
	if len(dest) != 3 {
		return errors.New("unexpected snapshot lookup")
	}
	if r.id != nil {
		*dest[0].(*sql.NullString) = sql.NullString{String: r.id.(string), Valid: true}
	}
	*dest[1].(*sql.NullString) = sql.NullString{String: "2026-10-02", Valid: true}
	matches := r.matches
	if matches == 0 {
		matches = 1
	}
	*dest[2].(*int) = matches
	return nil
}

func TestSnapshotDeleteWithoutIDRejectsUnsafeLookup(t *testing.T) {
	for name, client := range map[string]*snapshotCreateBackend{
		"denied":    {readError: errors.New("permission denied")},
		"null ID":   {},
		"blank ID":  {readID: ""},
		"ambiguous": {readID: "other-snapshot", matches: 2},
	} {
		t.Run(name, func(t *testing.T) {
			client.snapshotOrphanBackend = &snapshotOrphanBackend{}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := snapshotTestState(t, r)
			var model snapshotModel
			if d := state.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			model.ID = types.StringNull()
			if d := state.Set(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			deleted := resource.DeleteResponse{State: state}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
			if !deleted.Diagnostics.HasError() || len(client.execs) != 0 || deleted.State.Raw.IsNull() {
				t.Fatalf("unsafe lookup must preserve state without writes: diagnostics=%v writes=%v", deleted.Diagnostics, client.execs)
			}
		})
	}
}

func TestSnapshotCreateReadbackFailureCanBeDestroyed(t *testing.T) {
	for _, mode := range []string{"empty", "error", "null ID", "blank ID"} {
		t.Run(mode, func(t *testing.T) {
			client := &snapshotCreateBackend{snapshotOrphanBackend: &snapshotOrphanBackend{}}
			switch mode {
			case "empty":
				client.readError = sql.ErrNoRows
			case "error":
				client.readError = errors.New("snapshot readback failed")
			case "blank ID":
				client.readID = ""
			}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := snapshotTestState(t, r)
			var model snapshotModel
			if d := state.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			model.ID = types.StringUnknown()
			model.CreatedTS = types.StringUnknown()
			plan := tfsdk.Plan{Schema: state.Schema}
			if d := plan.Set(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			created := resource.CreateResponse{State: state}
			r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &created)
			if len(client.execs) != 1 || client.execs[0] != `CREATE SNAPSHOT "nightly" OF "dropped_db"` {
				t.Fatalf("expected remote creation, got %v", client.execs)
			}
			if !created.Diagnostics.HasError() || !created.State.Raw.IsFullyKnown() {
				t.Fatalf("failed readback must diagnose and retain cleanup state: %v", created.Diagnostics)
			}
			client.readError = nil
			client.readID = "snapshot-created-id"
			deleted := resource.DeleteResponse{State: created.State}
			r.Delete(t.Context(), resource.DeleteRequest{State: created.State}, &deleted)
			if deleted.Diagnostics.HasError() {
				t.Fatal(deleted.Diagnostics)
			}
			if len(client.execs) != 2 || client.execs[1] != "ALTER SNAPSHOT 'snapshot-created-id' SET snapshot_name = ''" {
				t.Fatalf("destroy left the created snapshot named: %v", client.execs)
			}
		})
	}
}
