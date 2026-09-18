package resources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	timeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

type snapshotUpdateBackend struct {
	providerctx.SQLClient
	readError error
	writes    []string
}

func (c *snapshotUpdateBackend) Available() bool                              { return true }
func (c *snapshotUpdateBackend) AttachDatabase(context.Context, string) error { return nil }
func (c *snapshotUpdateBackend) Exec(_ context.Context, query string, _ ...any) error {
	c.writes = append(c.writes, query)
	return nil
}
func (c *snapshotUpdateBackend) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return errRowScanner{err: c.readError}
}
func (c *snapshotUpdateBackend) WithDatabaseUse(ctx context.Context, _ string, fn func(func(string, ...any) error) error) error {
	return fn(func(query string, args ...any) error { return c.Exec(ctx, query, args...) })
}
func TestSnapshotRenameReadbackFailureRetainsIdentity(t *testing.T) {
	for _, readError := range []error{sql.ErrNoRows, errors.New("catalog lookup failed")} {
		t.Run(readError.Error(), func(t *testing.T) {
			ctx := t.Context()
			client := &snapshotUpdateBackend{readError: readError}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			original := snapshotModel{ID: types.StringValue("snapshot-id"), Database: types.StringValue("db"), Name: types.StringValue("before"), CreatedTS: types.StringValue("2026-09-18"), Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType})}}
			prior := tfsdk.State{Schema: schema.Schema}
			if d := prior.Set(ctx, &original); d.HasError() {
				t.Fatal(d)
			}
			next := original
			next.Name = types.StringValue("after")
			next.ID = types.StringUnknown()
			next.CreatedTS = types.StringUnknown()
			plan := tfsdk.Plan{Schema: schema.Schema}
			if d := plan.Set(ctx, &next); d.HasError() {
				t.Fatal(d)
			}
			response := resource.UpdateResponse{State: prior}
			r.Update(ctx, resource.UpdateRequest{State: prior, Plan: plan}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected readback diagnostic")
			}
			if len(client.writes) != 1 {
				t.Fatalf("expected successful rename: %v", client.writes)
			}
			var got snapshotModel
			if d := response.State.Get(ctx, &got); d.HasError() {
				t.Fatal(d)
			}
			if !got.ID.Equal(original.ID) || !got.Name.Equal(next.Name) || !got.CreatedTS.Equal(original.CreatedTS) {
				t.Fatalf("successful rename lost known identity: %+v", got)
			}
			deleted := resource.DeleteResponse{State: response.State}
			r.Delete(ctx, resource.DeleteRequest{State: response.State}, &deleted)
			if deleted.Diagnostics.HasError() {
				t.Fatal(deleted.Diagnostics)
			}
			if len(client.writes) != 2 || client.writes[1] != "ALTER SNAPSHOT 'snapshot-id' SET snapshot_name = ''" {
				t.Fatalf("cleanup did not use retained identity: %v", client.writes)
			}
		})
	}
}
