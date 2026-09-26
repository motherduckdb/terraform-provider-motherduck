package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	timeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// scriptedRow scans fixed values into NullString, string, and int targets.
type scriptedRow struct {
	values []any
	err    error
}

func (r scriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan %d targets from %d values", len(dest), len(r.values))
	}
	for i, target := range dest {
		switch target := target.(type) {
		case *stdsql.NullString:
			if r.values[i] == nil {
				*target = stdsql.NullString{}
			} else {
				*target = stdsql.NullString{String: r.values[i].(string), Valid: true}
			}
		case *string:
			*target = r.values[i].(string)
		case *int:
			*target = r.values[i].(int)
		default:
			return fmt.Errorf("unsupported scan target %T", target)
		}
	}
	return nil
}

// scriptedSQLClient answers QueryRow through a handler and records Exec calls.
type scriptedSQLClient struct {
	providerctx.SQLClient
	row     func(query string, args []any) mdsql.RowScanner
	execErr error
	execs   []string
}

func (c *scriptedSQLClient) Available() bool                              { return true }
func (c *scriptedSQLClient) AttachDatabase(context.Context, string) error { return nil }
func (c *scriptedSQLClient) Exec(_ context.Context, query string, _ ...any) error {
	c.execs = append(c.execs, query)
	return c.execErr
}
func (c *scriptedSQLClient) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	return c.row(query, args)
}

func emptyResourceState(ctx context.Context, t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	return tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
}

// DuckDB lowercases secret names even when they are quoted, so readback must
// find `mysecret` for a configured `MySecret` and keep the configured spelling.
func TestSecretCreateFindsLowercasedName(t *testing.T) {
	ctx := t.Context()
	client := &scriptedSQLClient{row: func(query string, args []any) mdsql.RowScanner {
		if strings.Contains(query, "lower(name) = lower(?)") && strings.ToLower(args[0].(string)) == "mysecret" {
			return scriptedRow{values: []any{"s3", "config", "motherduck", "['s3://bucket']"}}
		}
		return scriptedRow{err: stdsql.ErrNoRows}
	}}
	r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	plan := tfsdk.Plan(emptyResourceState(ctx, t, r))
	model := secretModel{
		Name: types.StringValue("MySecret"), Type: types.StringValue("s3"), ID: types.StringUnknown(),
		SecretProvider: types.StringUnknown(), Params: types.MapNull(types.StringType), Storage: types.StringUnknown(),
		Scope: types.StringUnknown(), SecretSQL: types.StringNull(),
	}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: emptyResourceState(ctx, t, r)}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", resp.Diagnostics)
	}
	var got secretModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if got.Name.ValueString() != "MySecret" || got.ID.ValueString() != "MySecret" || got.Storage.ValueString() != "motherduck" {
		t.Fatalf("state = %+v, want configured name with live storage", got)
	}
}

func TestSecretDeleteToleratesMissingSecret(t *testing.T) {
	ctx := t.Context()
	for _, execErr := range []error{nil, errors.New("Invalid Input Error: Failed to remove non-existent secret with name 'tf_gone'")} {
		client := &scriptedSQLClient{execErr: execErr}
		r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
		state := emptyResourceState(ctx, t, r)
		model := secretModel{
			Name: types.StringValue("tf_gone"), Type: types.StringValue("s3"), ID: types.StringValue("tf_gone"),
			SecretProvider: types.StringNull(), Params: types.MapNull(types.StringType), Storage: types.StringValue("motherduck"),
			Scope: types.StringNull(), SecretSQL: types.StringNull(),
		}
		if d := state.Set(ctx, &model); d.HasError() {
			t.Fatal(d)
		}
		resp := resource.DeleteResponse{State: state}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("delete with exec error %v: %v", execErr, resp.Diagnostics)
		}
		if len(client.execs) != 1 || client.execs[0] != `DROP SECRET IF EXISTS "tf_gone" FROM motherduck` {
			t.Fatalf("execs = %q", client.execs)
		}
	}
}

func snapshotStateForTest(ctx context.Context, t *testing.T, r resource.Resource, model snapshotModel) tfsdk.State {
	t.Helper()
	state := emptyResourceState(ctx, t, r)
	model.Timeouts = timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
		"create": types.StringType, "read": types.StringType, "update": types.StringType, "delete": types.StringType,
	})}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}

// Refreshing by snapshot ID reports an out-of-band rename as a name change,
// which plans an in-place rename back instead of a new snapshot.
func TestSnapshotReadFollowsIDAcrossRename(t *testing.T) {
	ctx := t.Context()
	for _, tc := range []struct {
		name     string
		liveName any
		wantName string
		removed  bool
	}{
		{name: "renamed", liveName: "renamed_out_of_band", wantName: "renamed_out_of_band"},
		{name: "unnamed", liveName: "", removed: true},
		{name: "null name", liveName: nil, removed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &scriptedSQLClient{row: func(query string, args []any) mdsql.RowScanner {
				if strings.Contains(query, "snapshot_id::VARCHAR = ?") && args[1] == "snapshot-id" {
					return scriptedRow{values: []any{tc.liveName, "2026-09-18"}}
				}
				return scriptedRow{err: stdsql.ErrNoRows}
			}}
			r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := snapshotStateForTest(ctx, t, r, snapshotModel{
				ID: types.StringValue("snapshot-id"), Database: types.StringValue("db"), Name: types.StringValue("before"), CreatedTS: types.StringValue("2026-09-18"),
			})
			resp := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if tc.removed {
				if !resp.State.Raw.IsNull() {
					t.Fatal("snapshot with a cleared name should be removed from state")
				}
				return
			}
			var got snapshotModel
			if d := resp.State.Get(ctx, &got); d.HasError() {
				t.Fatal(d)
			}
			if got.Name.ValueString() != tc.wantName || got.ID.ValueString() != "snapshot-id" {
				t.Fatalf("state = %+v, want name %q with the same ID", got, tc.wantName)
			}
		})
	}
}

func TestSnapshotReadFallsBackToNameWhenIDIsMissing(t *testing.T) {
	ctx := t.Context()
	client := &scriptedSQLClient{row: func(query string, args []any) mdsql.RowScanner {
		if strings.Contains(query, "snapshot_name = ?") && args[1] == "before" {
			return scriptedRow{values: []any{"replacement-id", "2026-09-19", 1}}
		}
		return scriptedRow{err: stdsql.ErrNoRows}
	}}
	r := &snapshotResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := snapshotStateForTest(ctx, t, r, snapshotModel{
		ID: types.StringValue("expired-id"), Database: types.StringValue("db"), Name: types.StringValue("before"), CreatedTS: types.StringValue("2026-09-18"),
	})
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got snapshotModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "replacement-id" {
		t.Fatalf("id = %q, want name lookup result", got.ID.ValueString())
	}
}

func TestShareIncludePatternRejectsUnquotedCommas(t *testing.T) {
	for pattern, wantErr := range map[string]bool{
		"main.a,main.b":          true,
		`main."dim,region"`:      false,
		`main."a""b",main.c`:     true,
		`main."a"",b"`:           false,
		"main.reporting_*":       false,
		`"quoted,schema".table`:  false,
		`"quoted,schema".t,main`: true,
	} {
		var diags diag.Diagnostics
		validateShareIncludePattern(types.ListValueMust(types.StringType, []attr.Value{types.StringValue(pattern)}), &diags)
		if diags.HasError() != wantErr {
			t.Errorf("pattern %q error = %v, want %v: %v", pattern, diags.HasError(), wantErr, diags)
		}
	}
}

// typeofStringer canonicalizes a few DuckDB type spellings like typeof().
type typeofStringer map[string]string

func (s typeofStringer) ScalarString(_ context.Context, query string, _ ...any) (string, error) {
	spelling := strings.TrimSuffix(strings.TrimPrefix(query, "SELECT typeof(CAST(NULL AS "), "))")
	if canonical, ok := s[spelling]; ok {
		return canonical, nil
	}
	return "", fmt.Errorf("Catalog Error: Type with name %s does not exist", spelling)
}

func TestTableColumnMapsEquivalentOnlyIgnoresAliasesAndKeywordCase(t *testing.T) {
	ctx := context.Background()
	client := typeofStringer{
		"INT": "INTEGER", "INTEGER": "INTEGER", "BIGINT": "BIGINT",
		"ENUM('a', 'b')": "ENUM('a', 'b')", "ENUM('A', 'B')": "ENUM('A', 'B')",
	}
	columns := func(columnType string) types.Map {
		return types.MapValueMust(types.StringType, map[string]attr.Value{"id": types.StringValue(columnType)})
	}
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"INT", "INTEGER", true},
		{"integer", "INTEGER", true},
		{"INT", "BIGINT", false},
		{"ENUM('a', 'b')", "ENUM('A', 'B')", false},
	} {
		var diags diag.Diagnostics
		if got := tableColumnMapsEquivalent(ctx, client, columns(tc.left), columns(tc.right), &diags); got != tc.want || diags.HasError() {
			t.Errorf("%s vs %s = %v (diags %v), want %v", tc.left, tc.right, got, diags, tc.want)
		}
	}
	var diags diag.Diagnostics
	if tableColumnMapsEquivalent(ctx, client, columns("mood"), columns("MOOD2"), &diags) || !diags.HasError() {
		t.Error("an unresolvable type must not be treated as equivalent")
	}
}
