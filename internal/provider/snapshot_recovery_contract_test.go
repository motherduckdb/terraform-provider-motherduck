//go:build contract

package provider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

type snapshotContractSQL struct {
	*contractSQL
	snapshotMu   sync.Mutex
	mode         string
	failRead     bool
	databaseGone bool
	named        bool
	creates      int
	unnames      int
	databaseUses []string
}

func (c *snapshotContractSQL) AttachDatabase(_ context.Context, database string) error {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	if database == "contract_database" && c.databaseGone {
		return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Database contract_database does not exist"}
	}
	if database == "contract_native" && !c.databaseGone {
		return errors.New("native database fallback used before creating database was dropped")
	}
	if database != "contract_database" && database != "contract_native" {
		return fmt.Errorf("unexpected database %q", database)
	}
	return nil
}

func (c *snapshotContractSQL) Exec(_ context.Context, query string, args ...any) error {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	if len(args) != 0 {
		return errors.New("unexpected snapshot write arguments")
	}
	switch query {
	case `CREATE SNAPSHOT "contract_snapshot" OF "contract_database"`:
		if c.named {
			return errors.New("snapshot name still retained from previous create")
		}
		c.named = true
		c.creates++
	case "ALTER SNAPSHOT 'snapshot-contract-id' SET snapshot_name = ''":
		if !c.named {
			return errors.New("snapshot already unnamed")
		}
		c.named = false
		c.unnames++
	default:
		return fmt.Errorf("unexpected snapshot write %q", query)
	}
	return nil
}

func (c *snapshotContractSQL) WithDatabaseUse(ctx context.Context, database string, fn func(func(string, ...any) error) error) error {
	if err := c.AttachDatabase(ctx, database); err != nil {
		return err
	}
	c.snapshotMu.Lock()
	c.databaseUses = append(c.databaseUses, database)
	c.snapshotMu.Unlock()
	return fn(func(query string, args ...any) error { return c.Exec(ctx, query, args...) })
}

func (c *snapshotContractSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	byName := `SELECT snapshot_id::VARCHAR, created_ts::VARCHAR, count(*) OVER () FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_name = ?`
	byID := `SELECT snapshot_name, created_ts::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_id::VARCHAR = ?`
	native := `SELECT name FROM MD_INFORMATION_SCHEMA.DATABASES WHERE upper(type) = 'DEFAULT' ORDER BY name LIMIT 1`
	want := "contract_snapshot"
	if query == native {
		if !c.databaseGone {
			return contractRow{err: errors.New("native database lookup used before creating database was dropped")}
		}
		if len(args) != 0 {
			return contractRow{err: fmt.Errorf("unexpected native database lookup args %v", args)}
		}
		return snapshotNativeRow{name: "contract_native"}
	} else if query == byID {
		want = "snapshot-contract-id"
	} else if query != byName {
		return contractRow{err: fmt.Errorf("unexpected snapshot read %q", query)}
	}
	if len(args) != 2 || args[0] != "contract_database" || args[1] != want {
		return contractRow{err: fmt.Errorf("unexpected snapshot lookup args %v", args)}
	}
	if !c.named {
		return contractRow{err: sql.ErrNoRows}
	}
	if c.failRead {
		switch c.mode {
		case "empty":
			return contractRow{err: sql.ErrNoRows}
		case "error":
			return contractRow{err: errors.New("snapshot readback failed")}
		case "null ID":
			return snapshotContractRow{identity: nil}
		case "blank ID":
			return snapshotContractRow{identity: ""}
		}
	}
	if query == byID {
		return contractRow{values: []any{"contract_snapshot", "2026-10-02"}}
	}
	return snapshotContractRow{identity: "snapshot-contract-id"}
}

type snapshotNativeRow struct{ name string }

func (r snapshotNativeRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return errors.New("unexpected native database scan")
	}
	name, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("unsupported native database scan destination %T", dest[0])
	}
	*name = r.name
	return nil
}

type snapshotContractRow struct{ identity any }

func (r snapshotContractRow) Scan(dest ...any) error {
	if len(dest) != 3 {
		return errors.New("unexpected snapshot scan")
	}
	if err := (contractRow{values: []any{r.identity, "2026-10-02"}}).Scan(dest[:2]...); err != nil {
		return err
	}
	*dest[2].(*int) = 1
	return nil
}

func TestContractSnapshotFailedCreateReleasesNameBeforeReplacement(t *testing.T) {
	for _, mode := range []string{"empty", "error", "null ID", "blank ID"} {
		t.Run(mode, func(t *testing.T) {
			client := &snapshotContractSQL{contractSQL: newContractSQL(), mode: mode, failRead: true}
			config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_snapshot" "test" {
  database = "contract_database"
  name = "contract_snapshot"
}


`
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(client),
				CheckDestroy: func(*terraform.State) error {
					client.snapshotMu.Lock()
					defer client.snapshotMu.Unlock()
					if client.named || client.creates != 2 || client.unnames != 2 {
						return fmt.Errorf("snapshot named=%t creates=%d unnames=%d", client.named, client.creates, client.unnames)
					}
					return nil
				},
				Steps: []resource.TestStep{
					{Config: config, ExpectError: regexp.MustCompile(`snapshot readback failed|Unable to read MotherDuck snapshot`)},
					{
						PreConfig: func() { client.snapshotMu.Lock(); client.failRead = false; client.snapshotMu.Unlock() },
						Config:    config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_snapshot.test", plancheck.ResourceActionReplace)},
							PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						Check: resource.TestCheckResourceAttr("motherduck_snapshot.test", "id", "snapshot-contract-id"),
					},
					{Config: config, ResourceName: "motherduck_snapshot.test", ImportState: true, ImportStateId: "contract_database.contract_snapshot", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"timeouts"}},
				},
			})
		})
	}
}

// A destroy with refresh disabled must still release a partially created
// snapshot. Its saved state has the database and name, but no catalog ID.
func TestContractSnapshotFailedCreateDestroyWithoutRefresh(t *testing.T) {
	for _, mode := range []string{"empty", "error"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			client := &snapshotContractSQL{contractSQL: newContractSQL(), mode: mode, failRead: true}
			server, err := contractProviderFactories(client)["motherduck"]()
			if err != nil {
				t.Fatal(err)
			}
			schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
			if err != nil {
				t.Fatal(err)
			}
			value := func(typ tftypes.Type, overrides map[string]any) *tfprotov6.DynamicValue {
				attrs := map[string]tftypes.Value{}
				for name, attrType := range typ.(tftypes.Object).AttributeTypes {
					attrs[name] = tftypes.NewValue(attrType, overrides[name])
				}
				dynamic, valueErr := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, attrs))
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				return &dynamic
			}
			providerConfig := value(schemas.Provider.ValueType(), map[string]any{"token": "contract-sql-token"})
			configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: providerConfig})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range configured.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatal(d)
				}
			}
			typ := schemas.ResourceSchemas["motherduck_snapshot"].ValueType()
			nullValue, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
			if err != nil {
				t.Fatal(err)
			}
			inputs := map[string]any{"database": "contract_database", "name": "contract_snapshot"}
			config := value(typ, inputs)
			inputs["id"] = tftypes.UnknownValue
			inputs["created_ts"] = tftypes.UnknownValue
			created, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "motherduck_snapshot", PriorState: &nullValue, PlannedState: value(typ, inputs), Config: config,
			})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for _, d := range created.Diagnostics {
				failed = failed || d.Severity == tfprotov6.DiagnosticSeverityError
			}
			if !failed || created.NewState == nil {
				t.Fatal("expected saved state and a failed catalog readback")
			}
			saved, err := created.NewState.Unmarshal(typ)
			if err != nil || saved.IsNull() || !saved.IsFullyKnown() {
				t.Fatalf("failed creation lost known cleanup state: %v", err)
			}
			refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "motherduck_snapshot", CurrentState: created.NewState})
			if err != nil {
				t.Fatal(err)
			}
			failed = false
			for _, d := range refreshed.Diagnostics {
				failed = failed || d.Severity == tfprotov6.DiagnosticSeverityError
			}
			retained, err := refreshed.NewState.Unmarshal(typ)
			if !failed || err != nil || retained.IsNull() {
				t.Fatalf("unresolved refresh must preserve cleanup state: failed=%t err=%v", failed, err)
			}
			unresolved, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "motherduck_snapshot", PriorState: created.NewState, PlannedState: &nullValue, Config: &nullValue,
			})
			if err != nil {
				t.Fatal(err)
			}
			failed = false
			for _, d := range unresolved.Diagnostics {
				failed = failed || d.Severity == tfprotov6.DiagnosticSeverityError
			}
			retained, err = unresolved.NewState.Unmarshal(typ)
			if !failed || err != nil || retained.IsNull() || !client.named || client.unnames != 0 {
				t.Fatalf("unresolved identity must preserve state and name: failed=%t err=%v named=%t unnames=%d", failed, err, client.named, client.unnames)
			}
			client.failRead = false
			deleted, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "motherduck_snapshot", PriorState: created.NewState, PlannedState: &nullValue, Config: &nullValue,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range deleted.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatal(d)
				}
			}
			removed, err := deleted.NewState.Unmarshal(typ)
			if err != nil || !removed.IsNull() {
				t.Fatalf("successful destroy must remove state: %v", err)
			}
			if client.named || client.creates != 1 || client.unnames != 1 {
				t.Fatalf("destroy orphaned snapshot: named=%t creates=%d unnames=%d", client.named, client.creates, client.unnames)
			}
		})
	}
}

func TestContractSnapshotFailedCreateThenDroppedDatabaseRecoversAndDestroys(t *testing.T) {
	ctx := t.Context()
	client := &snapshotContractSQL{contractSQL: newContractSQL(), mode: "empty", failRead: true}
	server, err := contractProviderFactories(client)["motherduck"]()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	value := func(typ tftypes.Type, overrides map[string]any) *tfprotov6.DynamicValue {
		attrs := map[string]tftypes.Value{}
		for name, attrType := range typ.(tftypes.Object).AttributeTypes {
			attrs[name] = tftypes.NewValue(attrType, overrides[name])
		}
		dynamic, valueErr := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, attrs))
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		return &dynamic
	}
	providerConfig := value(schemas.Provider.ValueType(), map[string]any{"token": "contract-sql-token"})
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: providerConfig})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range configured.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatal(d)
		}
	}
	typ := schemas.ResourceSchemas["motherduck_snapshot"].ValueType()
	nullValue, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string]any{"database": "contract_database", "name": "contract_snapshot"}
	config := value(typ, inputs)
	inputs["id"] = tftypes.UnknownValue
	inputs["created_ts"] = tftypes.UnknownValue
	created, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "motherduck_snapshot", PriorState: &nullValue, PlannedState: value(typ, inputs), Config: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasProtocolError(created.Diagnostics) || created.NewState == nil {
		t.Fatal("expected failed create with retained cleanup state")
	}
	saved, err := created.NewState.Unmarshal(typ)
	if err != nil || saved.IsNull() {
		t.Fatalf("failed creation lost state: %v", err)
	}

	client.failRead = false
	client.databaseGone = true
	refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "motherduck_snapshot", CurrentState: created.NewState})
	if err != nil {
		t.Fatal(err)
	}
	if hasProtocolError(refreshed.Diagnostics) {
		t.Fatalf("dropped database refresh failed: %v", refreshed.Diagnostics)
	}
	if !hasProtocolWarning(refreshed.Diagnostics, "MotherDuck snapshot outlived its database") {
		t.Fatalf("expected retained snapshot warning, got %v", refreshed.Diagnostics)
	}
	recovered, err := refreshed.NewState.Unmarshal(typ)
	if err != nil {
		t.Fatal(err)
	}
	var recoveredAttrs map[string]tftypes.Value
	if err := recovered.As(&recoveredAttrs); err != nil {
		t.Fatal(err)
	}
	id := recoveredAttrs["id"]
	var idString string
	if err := id.As(&idString); err != nil || idString != "snapshot-contract-id" {
		t.Fatalf("recovered snapshot ID = %q, want snapshot-contract-id", idString)
	}
	if !client.named || client.unnames != 0 {
		t.Fatalf("refresh must retain the remote name: named=%t unnames=%d", client.named, client.unnames)
	}

	deleted, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "motherduck_snapshot", PriorState: created.NewState, PlannedState: &nullValue, Config: &nullValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasProtocolError(deleted.Diagnostics) {
		t.Fatalf("destroy after dropped database failed: %v", deleted.Diagnostics)
	}
	removed, err := deleted.NewState.Unmarshal(typ)
	if err != nil || !removed.IsNull() {
		t.Fatalf("successful destroy must remove state: %v", err)
	}
	if client.named || client.unnames != 1 {
		t.Fatalf("destroy did not clear the retained name: named=%t unnames=%d", client.named, client.unnames)
	}
	if len(client.databaseUses) != 1 || client.databaseUses[0] != "contract_native" {
		t.Fatalf("destroy used databases = %v, want [contract_native]", client.databaseUses)
	}
}

func hasProtocolError(diags []*tfprotov6.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

func hasProtocolWarning(diags []*tfprotov6.Diagnostic, summary string) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityWarning && d.Summary == summary {
			return true
		}
	}
	return false
}
