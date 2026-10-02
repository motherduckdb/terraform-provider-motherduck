//go:build contract

package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestContractSecretLookupIcebergExample(t *testing.T) {
	example, err := os.ReadFile("../../examples/data-sources/motherduck_secret/data-source.tf")
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the database lifecycle fixture's identity and warehouse.
	config := contractProviderConfig("http://127.0.0.1") + strings.NewReplacer(
		`name          = "lakehouse"`, `name          = "contract_database"`,
		`"analytics_warehouse"`, `"analytics"`,
	).Replace(string(example))
	client := &secretLookupExampleSQL{databaseIcebergSQL: &databaseIcebergSQL{contractSQL: newContractSQL()}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if client.exists || client.creates != 1 {
				return fmt.Errorf("example lifecycle: exists=%t creates=%d", client.exists, client.creates)
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config:           config,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			Check:            resource.TestCheckResourceAttr("motherduck_database.lakehouse", "iceberg.secret", "lakehouse_catalog"),
		}},
	})
}

type secretLookupExampleSQL struct{ *databaseIcebergSQL }

func (c *secretLookupExampleSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	const lookup = `SELECT type, provider, persistent, scope::VARCHAR FROM duckdb_secrets() WHERE lower(name) = lower(?) AND storage = 'motherduck'`
	if query == lookup && len(args) == 1 && args[0] == "lakehouse_catalog" {
		return contractRow{values: []any{"iceberg", "config", true, nil}}
	}
	return c.databaseIcebergSQL.QueryRow(ctx, query, args...)
}

func (c *secretLookupExampleSQL) Exec(ctx context.Context, query string, args ...any) error {
	const create = `CREATE DATABASE "contract_database" (TYPE ICEBERG, "secret" 'lakehouse_catalog', DEFAULT_SCHEMA 'default', ENDPOINT 'https://catalog.example.com', WAREHOUSE 'analytics')`
	if query == create {
		return c.databaseIcebergSQL.Exec(ctx, icebergContractCreate, args...)
	}
	return c.databaseIcebergSQL.Exec(ctx, query, args...)
}
