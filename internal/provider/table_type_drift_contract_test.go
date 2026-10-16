//go:build contract

package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type enumTableSQL struct {
	*contractSQL
	creates int
}

func (c *enumTableSQL) Exec(ctx context.Context, query string, args ...any) error {
	if query == `CREATE TABLE "contract_database"."app"."facts" ("id" INTEGER, "label" ENUM('open', 'closed'))` {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.tableExists || len(args) != 0 {
			return fmt.Errorf("unexpected duplicate create or arguments")
		}
		c.tableExists = true
		c.tableColumns = map[string]string{"id": "INTEGER", "label": "ENUM('open', 'closed')"}
		c.creates++
		return nil
	}
	return c.contractSQL.Exec(ctx, query, args...)
}
func (c *enumTableSQL) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	switch query {
	case "SELECT typeof(CAST(NULL AS ENUM('open', 'closed')))":
		return "ENUM('open', 'closed')", nil
	case "SELECT typeof(CAST(NULL AS ENUM('OPEN', 'closed')))":
		return "ENUM('OPEN', 'closed')", nil
	}
	return c.contractSQL.ScalarString(ctx, query, args...)
}

func TestContractTableRefreshDetectsEnumCaseDrift(t *testing.T) {
	client := &enumTableSQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_table" "test" {
 database = "contract_database"
 schema = "app"
 name = "facts"
 columns = { id = "INT", label = "ENUM('open', 'closed')" }
}
`
	setLabel := func(value string) func() {
		return func() { client.mu.Lock(); defer client.mu.Unlock(); client.tableColumns["label"] = value }
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.mu.Lock()
			defer client.mu.Unlock()
			if client.tableExists || client.creates != 1 {
				return fmt.Errorf("exists=%v creates=%d, want false and 1", client.tableExists, client.creates)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: config, ResourceName: "motherduck_table.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"columns"}, ImportStateCheck: func(s []*terraform.InstanceState) error {
				if len(s) != 1 || s[0].Attributes["columns.label"] != "ENUM('open', 'closed')" || s[0].Attributes["columns.id"] != "INTEGER" {
					return fmt.Errorf("unexpected imported types: %#v", s)
				}
				return nil
			}},
			{PreConfig: setLabel("ENUM('OPEN', 'closed')"), Config: config, PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionReplace)}}},
			{Config: config, ResourceName: "motherduck_table.test", ImportState: true, ImportStateCheck: func(s []*terraform.InstanceState) error {
				if len(s) != 1 || s[0].Attributes["columns.label"] != "ENUM('OPEN', 'closed')" {
					return fmt.Errorf("import missed drift: %#v", s)
				}
				return nil
			}},
			{PreConfig: setLabel("ENUM('open', 'closed')"), Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
