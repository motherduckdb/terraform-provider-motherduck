package resources

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	timeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// snapshotOrphanBackend models a snapshot whose creating database may be gone.
type snapshotOrphanBackend struct {
	providerctx.SQLClient
	attachErr error
	execErr   error
	rows      []scannedRow
	execs     []string
	used      []string
}

func (c *snapshotOrphanBackend) Available() bool { return true }
func (c *snapshotOrphanBackend) AttachDatabase(context.Context, string) error {
	return c.attachErr
}
func (c *snapshotOrphanBackend) Exec(_ context.Context, query string, _ ...any) error {
	c.execs = append(c.execs, query)
	return c.execErr
}
func (c *snapshotOrphanBackend) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	if len(c.rows) == 0 {
		return errRowScanner{err: sql.ErrNoRows}
	}
	row := c.rows[0]
	c.rows = c.rows[1:]
	return row
}
func (c *snapshotOrphanBackend) WithDatabaseUse(ctx context.Context, database string, fn func(func(string, ...any) error) error) error {
	c.used = append(c.used, database)
	return fn(func(query string, args ...any) error { return c.Exec(ctx, query, args...) })
}

func snapshotTestState(t *testing.T, r *snapshotResource) tfsdk.State {
	t.Helper()
	ctx := t.Context()
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := snapshotModel{
		ID:        types.StringValue("00000000-0000-0000-0000-000000000042"),
		Database:  types.StringValue("dropped_db"),
		Name:      types.StringValue("nightly"),
		CreatedTS: types.StringValue("2026-09-18"),
		Timeouts:  timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType})},
	}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}

func missingDatabaseError() error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Database dropped_db does not exist"}
}

func TestSnapshotDeleteClearsNameWhenDatabaseWasDropped(t *testing.T) {
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError()}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotTestState(t, r)
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	want := "ALTER SNAPSHOT '00000000-0000-0000-0000-000000000042' SET snapshot_name = ''"
	if len(client.execs) != 1 || client.execs[0] != want {
		t.Fatalf("expected the snapshot name to be cleared by ID, got %v", client.execs)
	}
	if len(client.used) != 0 {
		t.Fatalf("a dropped database must not be selected with USE, got %v", client.used)
	}
}

func TestSnapshotDeleteTreatsMissingSnapshotAsDeleted(t *testing.T) {
	notFound := &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: "Invalid Input Error: Snapshot not found"}
	for name, attachErr := range map[string]error{"database exists": nil, "database dropped": missingDatabaseError()} {
		t.Run(name, func(t *testing.T) {
			client := &snapshotOrphanBackend{attachErr: attachErr, execErr: notFound}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := snapshotTestState(t, r)
			response := resource.DeleteResponse{State: state}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}

func TestSnapshotDeleteReportsOtherUnnameErrors(t *testing.T) {
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError(), execErr: &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: write access denied"}}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotTestState(t, r)
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unname failure to be reported")
	}
}

func TestSnapshotReadWarnsWhenSnapshotIsNoLongerListed(t *testing.T) {
	for name, client := range map[string]*snapshotOrphanBackend{
		"database dropped":   {attachErr: missingDatabaseError()},
		"database recreated": {},
	} {
		t.Run(name, func(t *testing.T) {
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := snapshotTestState(t, r)
			response := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			if !response.State.Raw.IsNull() {
				t.Fatal("unlisted snapshot should be removed from state")
			}
			warning := findWarning(response.Diagnostics, "MotherDuck snapshot is no longer listed")
			if warning == nil || !strings.Contains(warning.Detail(), "ALTER SNAPSHOT '00000000-0000-0000-0000-000000000042' SET snapshot_name = ''") {
				t.Fatalf("expected a release hint warning, got %v", response.Diagnostics)
			}
		})
	}
}

func TestSnapshotReadDoesNotWarnWhenNameWasCleared(t *testing.T) {
	client := &snapshotOrphanBackend{rows: []scannedRow{{values: []any{"", "2026-09-18"}}}}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotTestState(t, r)
	response := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() || response.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("cleared snapshot should be removed quietly, got %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("cleared snapshot should be removed from state")
	}
}

func findWarning(diags diag.Diagnostics, summary string) diag.Diagnostic {
	for _, d := range diags.Warnings() {
		if d.Summary() == summary {
			return d
		}
	}
	return nil
}
