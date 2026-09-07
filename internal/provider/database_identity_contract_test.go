//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

type databaseIdentitySQL struct {
	*contractSQL
	retention     string
	schemaExists  bool
	schemaCreates int
	schemaDrops   int
}

func (c *databaseIdentitySQL) Exec(ctx context.Context, query string, args ...any) error {
	switch query {
	case `CREATE DATABASE "contract_database" (SNAPSHOT_RETENTION_DAYS 7)`:
		if err := c.contractSQL.Exec(ctx, `CREATE DATABASE "contract_database"`, args...); err != nil {
			return err
		}
		c.mu.Lock()
		c.retention = "7 days"
		c.mu.Unlock()
		return nil
	case `ALTER DATABASE "contract_database" SET SNAPSHOT_RETENTION_DAYS = 14`:
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.databaseExists {
			return fmt.Errorf("updating a missing database")
		}
		c.retention = "14 days"
		return nil
	case `CREATE SCHEMA "contract_database"."app"`:
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.databaseExists || c.schemaExists {
			return fmt.Errorf("invalid schema create")
		}
		c.schemaExists = true
		c.schemaCreates++
		return nil
	case `DROP SCHEMA IF EXISTS "contract_database"."app"`:
		c.mu.Lock()
		defer c.mu.Unlock()
		c.schemaExists = false
		c.schemaDrops++
		return nil
	case `DROP DATABASE IF EXISTS "contract_database"`:
		c.mu.Lock()
		populated := c.schemaExists
		c.mu.Unlock()
		if populated {
			return fmt.Errorf("database still contains the managed schema")
		}
	}
	return c.contractSQL.Exec(ctx, query, args...)
}

func (c *databaseIdentitySQL) Exists(ctx context.Context, query string, args ...any) (bool, error) {
	if strings.Contains(query, "information_schema.schemata") {
		if len(args) != 2 || args[0] != "contract_database" || args[1] != "app" {
			return false, fmt.Errorf("unexpected schema lookup: %v", args)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.schemaExists, nil
	}
	return c.contractSQL.Exists(ctx, query, args...)
}

func (c *databaseIdentitySQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES") {
		if len(args) != 1 || args[0] != "contract_database" {
			return contractRow{err: fmt.Errorf("unexpected database lookup: %v", args)}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.databaseExists {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: []any{"00000000-0000-0000-0000-000000000001", "2026-09-01T00:00:00Z", false, c.retention, "MOTHERDUCK"}}
	}
	return c.contractSQL.QueryRow(ctx, query, args...)
}

func TestContractDatabaseOptionUpdatePreservesDependentSchema(t *testing.T) {
	client := &databaseIdentitySQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_database" "test" {
  name = "contract_database"
  snapshot_retention_days = 7
}
resource "motherduck_schema" "test" {
  database = motherduck_database.test.id
  name = "app"
}
`
	updated := strings.Replace(config, "days = 7", "days = 14", 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.mu.Lock()
			defer client.mu.Unlock()
			if client.databaseExists || client.schemaExists || client.schemaCreates != 1 || client.schemaDrops != 1 {
				return fmt.Errorf("unexpected lifecycle: database=%t schema=%t creates=%d drops=%d", client.databaseExists, client.schemaExists, client.schemaCreates, client.schemaDrops)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config},
			{
				Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("motherduck_schema.test", plancheck.ResourceActionNoop),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "snapshot_retention_days", "14"),
			},
			{
				Config:             strings.Replace(updated, "contract_database", "contract_renamed", 1),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionDestroyBeforeCreate),
					plancheck.ExpectResourceAction("motherduck_schema.test", plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
		},
	})
}
