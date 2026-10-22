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

const (
	ducklakeContractCreate25 = `CREATE DATABASE "contract_database" (TYPE DUCKLAKE, DATA_INLINING_ROW_LIMIT 25)`
	ducklakeContractCreate50 = `CREATE DATABASE "contract_database" (TYPE DUCKLAKE, DATA_INLINING_ROW_LIMIT 50)`
	ducklakeContractDrop     = `DROP DATABASE IF EXISTS "contract_database"`
)

// databaseDuckLakeSQL records CREATE DATABASE statements. MotherDuck does not
// report the inlining limit, so the database row carries only its type.
type databaseDuckLakeSQL struct {
	*contractSQL
	exists  bool
	creates []string
}

func (c *databaseDuckLakeSQL) Exec(ctx context.Context, query string, args ...any) error {
	c.record("exec", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch query {
	case ducklakeContractCreate25, ducklakeContractCreate50:
		if c.exists {
			return fmt.Errorf("duplicate database create")
		}
		c.exists = true
		c.creates = append(c.creates, query)
	case ducklakeContractDrop:
		c.exists = false
	case "USE memory":
	default:
		return fmt.Errorf("unexpected SQL exec %q", query)
	}
	return nil
}

func (c *databaseDuckLakeSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES") {
		if len(args) != 1 || args[0] != "contract_database" {
			return contractRow{err: fmt.Errorf("unexpected database lookup: %v", args)}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.exists {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: []any{"00000000-0000-0000-0000-000000000003", "2026-09-01T00:00:00Z", false, nil, "DUCKLAKE"}}
	}
	return c.contractSQL.QueryRow(ctx, query, args...)
}

func TestContractDuckLakeDataInliningRowLimitIsCreateOnly(t *testing.T) {
	client := &databaseDuckLakeSQL{contractSQL: newContractSQL()}
	base := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_database" "test" {
  name                    = "contract_database"
  database_type           = "ducklake"
  data_inlining_row_limit = 25
}
`
	changed := strings.Replace(base, "= 25", "= 50", 1)
	removed := strings.Replace(changed, "data_inlining_row_limit = 50\n", "", 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.mu.Lock()
			defer client.mu.Unlock()
			want := []string{ducklakeContractCreate25, ducklakeContractCreate50}
			if client.exists || strings.Join(client.creates, "\n") != strings.Join(want, "\n") {
				return fmt.Errorf("unexpected lifecycle: exists=%t creates=%q, want %q", client.exists, client.creates, want)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: base,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_database.test", "database_type", "ducklake"),
					resource.TestCheckResourceAttr("motherduck_database.test", "data_inlining_row_limit", "25"),
				),
			},
			{
				Config: changed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionReplace)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "data_inlining_row_limit", "50"),
			},
			{
				// Removing the setting does not change the live database, so it
				// must not replace it.
				Config: removed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("motherduck_database.test", "data_inlining_row_limit"),
			},
		},
	})
}
