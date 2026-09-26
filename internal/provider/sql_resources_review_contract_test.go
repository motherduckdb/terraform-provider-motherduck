//go:build contract

package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

const contractShareConfig = `
resource "motherduck_share" "test" {
  name            = "contract_share"
  source_database = "analytics"
}
`

// Changing include_pattern is documented as an in-place update. Omitted share
// options must keep their refreshed values instead of becoming unknown, which
// would trigger their replace modifiers and drop the share.
func TestContractShareIncludePatternUpdatesInPlace(t *testing.T) {
	sqlClient := newContractSQL()
	withPattern := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_share" "test" {
  name            = "contract_share"
  source_database = "analytics"
  include_pattern = ["main.*"]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if got := sqlClient.countCalls(`exec CREATE SHARE "contract_share" FROM "analytics"`); got != 1 {
				return fmt.Errorf("share creates = %d, want 1", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: contractProviderConfig("http://127.0.0.1") + contractShareConfig},
			{
				Config: withPattern,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_share.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue("motherduck_share.test", tfjsonpath.New("access"), knownvalue.StringExact("organization")),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_share.test", "include_pattern.0", "main.*"),
			},
			{
				Config: contractProviderConfig("http://127.0.0.1") + contractShareConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share.test", plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

type flakyShareReadSQL struct {
	*contractSQL
	readMu    sync.Mutex
	failed    bool
	failReads bool
}

func (c *flakyShareReadSQL) Exec(ctx context.Context, query string, args ...any) error {
	err := c.contractSQL.Exec(ctx, query, args...)
	if err == nil && strings.HasPrefix(query, "CREATE SHARE") {
		c.readMu.Lock()
		// Fail only the readback after the first create.
		if !c.failed {
			c.failed = true
			c.failReads = true
		}
		c.readMu.Unlock()
	}
	return err
}

func (c *flakyShareReadSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	c.readMu.Lock()
	fail := c.failReads
	c.readMu.Unlock()
	if fail && strings.Contains(query, "OWNED_SHARES") {
		return contractRow{err: errors.New("owned share catalog lookup failed")}
	}
	return c.contractSQL.QueryRow(ctx, query, args...)
}

// A failed readback after CREATE SHARE must save a fully known state so the
// share is tainted and replaced, not reported as an invalid provider result.
func TestContractShareCreateReadbackFailureTaintsShare(t *testing.T) {
	sqlClient := &flakyShareReadSQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + contractShareConfig
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile(`owned share catalog lookup failed`)},
			{
				PreConfig: func() {
					sqlClient.readMu.Lock()
					sqlClient.failReads = false
					sqlClient.readMu.Unlock()
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share.test", plancheck.ResourceActionReplace)},
				},
			},
		},
	})
}

// Import stores canonical INTEGER types. A configuration that spells the same
// type as INT must update state in place instead of dropping the table.
func TestContractTableImportWithTypeAliasDoesNotReplace(t *testing.T) {
	sqlClient := newContractSQL()
	sqlClient.tableExists = true
	sqlClient.tableColumns = map[string]string{"id": "INTEGER", "label": "VARCHAR"}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_table" "test" {
  database = "contract_database"
  schema   = "app"
  name     = "facts"
  columns = {
    id    = "INT"
    label = "VARCHAR"
  }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if got := sqlClient.countCalls(`exec CREATE TABLE "contract_database"."app"."facts" ("id" INTEGER, "label" VARCHAR)`); got != 0 {
				return fmt.Errorf("table creates = %d, want 0 after import", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				ResourceName:       "motherduck_table.test",
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateId:      "contract_database.app.facts",
				Config:             config,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_table.test", "columns.id", "INT"),
			},
		},
	})
}
