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
func (c *snapshotOrphanBackend) AttachDatabase(_ context.Context, database string) error {
	if database != "dropped_db" {
		return nil
	}
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

func nativeDatabaseRow() scannedRow { return scannedRow{values: []any{"my_db"}} }

func TestSnapshotDeleteClearsNameWhenDatabaseWasDropped(t *testing.T) {
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError(), rows: []scannedRow{nativeDatabaseRow()}}
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
	// ALTER SNAPSHOT binds to the selected database, which must be native.
	if len(client.used) != 1 || client.used[0] != "my_db" {
		t.Fatalf("expected the release to run from a native database, got %v", client.used)
	}
}

func TestSnapshotDeleteReportsMissingNativeDatabase(t *testing.T) {
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError()}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotTestState(t, r)
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "ALTER SNAPSHOT '00000000-0000-0000-0000-000000000042'") {
		t.Fatalf("expected an error with the release statement, got %v", response.Diagnostics)
	}
	if len(client.execs) != 0 {
		t.Fatalf("no statement may run without a native database, got %v", client.execs)
	}
}

func TestSnapshotReadKeepsRetainedSnapshotOfDroppedDatabase(t *testing.T) {
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError(), rows: []scannedRow{{values: []any{"nightly", "2026-09-18"}}}}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotTestState(t, r)
	response := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if response.State.Raw.IsNull() {
		t.Fatal("a retained snapshot must stay in state so destroy can release it")
	}
	if findWarning(response.Diagnostics, "MotherDuck snapshot outlived its database") == nil {
		t.Fatalf("expected a retained snapshot warning, got %v", response.Diagnostics)
	}
}

type snapshotMissingIDBackend struct {
	*snapshotOrphanBackend
	readErr      error
	readID       any
	matches      int
	catalogReads int
}

func (c *snapshotMissingIDBackend) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES") {
		return c.snapshotOrphanBackend.QueryRow(ctx, query, args...)
	}
	c.catalogReads++
	if c.readErr != nil {
		return errRowScanner{err: c.readErr}
	}
	return snapshotIdentityRow{id: c.readID, matches: c.matches}
}

func missingIDSnapshotState(t *testing.T, r *snapshotResource) tfsdk.State {
	t.Helper()
	state := snapshotTestState(t, r)
	var model snapshotModel
	if d := state.Get(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	model.ID = types.StringNull()
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}

func TestSnapshotReadRecoversMissingIDAfterDatabaseDrop(t *testing.T) {
	client := &snapshotMissingIDBackend{
		snapshotOrphanBackend: &snapshotOrphanBackend{attachErr: missingDatabaseError()},
		readID:                "snapshot-recovered-id",
	}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := missingIDSnapshotState(t, r)
	response := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var got snapshotModel
	if d := response.State.Get(t.Context(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "snapshot-recovered-id" {
		t.Fatalf("recovered ID = %q, want snapshot-recovered-id", got.ID.ValueString())
	}
	if findWarning(response.Diagnostics, "MotherDuck snapshot outlived its database") == nil {
		t.Fatalf("expected retained snapshot warning, got %v", response.Diagnostics)
	}
}

func TestSnapshotDestroyRecoversMissingIDAndUsesNativeDatabase(t *testing.T) {
	client := &snapshotMissingIDBackend{
		snapshotOrphanBackend: &snapshotOrphanBackend{
			attachErr: missingDatabaseError(),
			rows:      []scannedRow{nativeDatabaseRow()},
		},
		readID: "snapshot-recovered-id",
	}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := missingIDSnapshotState(t, r)
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	want := "ALTER SNAPSHOT 'snapshot-recovered-id' SET snapshot_name = ''"
	if len(client.execs) != 1 || client.execs[0] != want {
		t.Fatalf("unname statements = %v, want %q", client.execs, want)
	}
	if len(client.used) != 1 || client.used[0] != "my_db" {
		t.Fatalf("native database selections = %v, want [my_db]", client.used)
	}
}

func TestSnapshotMissingIDDroppedDatabaseFailuresStayConservative(t *testing.T) {
	cases := map[string]struct {
		attachErr error
		readErr   error
		readID    any
		matches   int
	}{
		"denied attach": {
			attachErr: &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: access denied"},
			readID:    "snapshot-recovered-id",
		},
		"unrelated missing database": {
			attachErr: &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Database other_db does not exist"},
			readID:    "snapshot-recovered-id",
		},
		"empty catalog": {readErr: sql.ErrNoRows},
		"null ID":       {readID: nil},
		"blank ID":      {readID: ""},
		"ambiguous":     {readID: "snapshot-recovered-id", matches: 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			attachErr := tc.attachErr
			if attachErr == nil {
				attachErr = missingDatabaseError()
			}
			client := &snapshotMissingIDBackend{
				snapshotOrphanBackend: &snapshotOrphanBackend{attachErr: attachErr},
				readErr:               tc.readErr,
				readID:                tc.readID,
				matches:               tc.matches,
			}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := missingIDSnapshotState(t, r)
			response := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
			if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
				t.Fatalf("unsafe lookup must diagnose and preserve state: diagnostics=%v state-null=%t", response.Diagnostics, response.State.Raw.IsNull())
			}
			if len(client.execs) != 0 {
				t.Fatalf("read failure must not write snapshot state: %v", client.execs)
			}
			if (name == "denied attach" || name == "unrelated missing database") && client.catalogReads != 0 {
				t.Fatalf("unsafe attach error must not permit catalog lookup, reads=%d", client.catalogReads)
			}

			deleteClient := &snapshotMissingIDBackend{
				snapshotOrphanBackend: &snapshotOrphanBackend{attachErr: attachErr},
				readErr:               tc.readErr,
				readID:                tc.readID,
				matches:               tc.matches,
			}
			deleteResource := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: deleteClient}}}
			deleteState := missingIDSnapshotState(t, deleteResource)
			deleted := resource.DeleteResponse{State: deleteState}
			deleteResource.Delete(t.Context(), resource.DeleteRequest{State: deleteState}, &deleted)
			if !deleted.Diagnostics.HasError() || deleted.State.Raw.IsNull() {
				t.Fatalf("unsafe destroy must diagnose and preserve state: diagnostics=%v state-null=%t", deleted.Diagnostics, deleted.State.Raw.IsNull())
			}
			if len(deleteClient.execs) != 0 {
				t.Fatalf("destroy failure must not write snapshot state: %v", deleteClient.execs)
			}
			if (name == "denied attach" || name == "unrelated missing database") && deleteClient.catalogReads != 0 {
				t.Fatalf("unsafe attach error must not permit destroy lookup, reads=%d", deleteClient.catalogReads)
			}
		})
	}
}

func TestSnapshotDeleteTreatsMissingSnapshotAsDeleted(t *testing.T) {
	notFound := &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: "Invalid Input Error: Snapshot not found"}
	for name, attachErr := range map[string]error{"database exists": nil, "database dropped": missingDatabaseError()} {
		t.Run(name, func(t *testing.T) {
			client := &snapshotOrphanBackend{attachErr: attachErr, execErr: notFound, rows: []scannedRow{nativeDatabaseRow()}}
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
	client := &snapshotOrphanBackend{attachErr: missingDatabaseError(), execErr: &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: write access denied"}, rows: []scannedRow{nativeDatabaseRow()}}
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
