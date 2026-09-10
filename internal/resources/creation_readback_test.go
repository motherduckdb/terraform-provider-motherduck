package resources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	timeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

type failedCreationReadback struct {
	providerctx.SQLClient
	err    error
	writes []string
}

func (c *failedCreationReadback) Available() bool { return true }
func (c *failedCreationReadback) Exec(_ context.Context, query string, _ ...any) error {
	c.writes = append(c.writes, query)
	return nil
}
func (c *failedCreationReadback) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return "[]", c.err
}
func (c *failedCreationReadback) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	if c.err != nil {
		return errRowScanner{err: c.err}
	}
	return errRowScanner{err: sql.ErrNoRows}
}
func (c *failedCreationReadback) ScalarString(context.Context, string, ...any) (string, error) {
	return "memory", nil
}

func TestCreateReadbackFailureKeepsCleanupState(t *testing.T) {
	for _, kind := range []string{"role", "grant", "database"} {
		for _, mode := range []string{"empty", "error"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx := t.Context()
				client := &failedCreationReadback{}
				if mode == "error" {
					client.err = errors.New("catalog lookup failed")
				}
				base := baseResource{provider: &providerctx.Context{SQL: client}}
				var r resource.Resource
				var model any
				var cleanup string
				switch kind {
				case "role":
					r = &roleResource{baseResource: base}
					model = &roleModel{
						Name: types.StringValue("tf_readback"), ID: types.StringUnknown(),
						RoleType: types.StringUnknown(), IncludedRoles: types.ListUnknown(types.StringType), CreatedAt: types.StringUnknown(),
					}
					cleanup = `DROP ROLE IF EXISTS "tf_readback"`
				case "grant":
					r = &roleGrantResource{baseResource: base}
					model = &roleGrantModel{
						RoleName: types.StringValue("tf_readback"), GranteeName: types.StringValue("tf_reader"),
						GranteeType: types.StringValue("user"), ID: types.StringUnknown(), GrantedAt: types.StringUnknown(),
					}
					cleanup = `REVOKE ROLE "tf_readback" FROM USER "tf_reader"`
				case "database":
					r = &databaseResource{baseResource: base}
					model = &databaseModel{
						Name: types.StringValue("tf_readback"), ID: types.StringUnknown(), UUID: types.StringUnknown(), CreatedTS: types.StringUnknown(),
						Transient: types.BoolUnknown(), SnapshotRetentionDays: types.Int64Unknown(), DatabaseType: types.StringUnknown(),
						Timeouts: timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
							"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType,
						})},
					}
					cleanup = `DROP DATABASE IF EXISTS "tf_readback"`
				}
				var schemaResp resource.SchemaResponse
				r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
				plan := tfsdk.Plan{Schema: schemaResp.Schema}
				if d := plan.Set(ctx, model); d.HasError() {
					t.Fatal(d)
				}
				state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
				resp := resource.CreateResponse{State: state}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
				if !resp.Diagnostics.HasError() || len(client.writes) != 1 {
					t.Fatalf("expected one successful write followed by a read error: %v", resp.Diagnostics)
				}
				if resp.State.Raw.IsNull() || !resp.State.Raw.IsFullyKnown() {
					t.Fatal("failed creation must retain known state for cleanup")
				}
				var id types.String
				if d := resp.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() || id.IsNull() || id.ValueString() == "" {
					t.Fatalf("missing created identity: %v", d)
				}
				deleted := resource.DeleteResponse{State: resp.State}
				r.Delete(ctx, resource.DeleteRequest{State: resp.State}, &deleted)
				if deleted.Diagnostics.HasError() {
					t.Fatal(deleted.Diagnostics)
				}
				if len(client.writes) != 2 || client.writes[1] != cleanup {
					t.Fatalf("cleanup writes=%v, want %q", client.writes, cleanup)
				}
			})
		}
	}
}
