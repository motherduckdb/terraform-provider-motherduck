//go:build acceptance && secure_views_acceptance

package acceptance

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestPluginTestingShareSecureViewLifecycle needs an account on a plan that
// offers secure views, so it builds only with the secure_views_acceptance tag.
// It covers create, an in-place policy update, share replacement through
// replace_triggered_by, import, and the audit data source.
func TestPluginTestingShareSecureViewLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}
	suffix := time.Now().UTC().UnixNano()
	databaseName := fmt.Sprintf("tf_acc_sv_%d", suffix)
	shareName := fmt.Sprintf("tf_acc_sv_share_%d", suffix)
	config := func(visibility, filter string) string {
		return fmt.Sprintf(`
resource "motherduck_database" "test" {
  name = %q
}

resource "motherduck_schema" "app" {
  database = motherduck_database.test.name
  name     = "app"
}

resource "motherduck_table" "usage" {
  database = motherduck_database.test.name
  schema   = motherduck_schema.app.name
  name     = "daily_usage"
  columns = {
    tenant_id   = "VARCHAR"
    event_count = "BIGINT"
  }
}

resource "motherduck_share" "test" {
  name            = %q
  source_database = motherduck_database.test.name
  access          = "restricted"
  visibility      = %q
  update_mode     = "automatic"
  include_pattern = []
  depends_on      = [motherduck_table.usage]
}

resource "motherduck_share_secure_view" "usage" {
  share    = motherduck_share.test.name
  schema   = motherduck_table.usage.schema
  name     = motherduck_table.usage.name
  replaces = true
  query    = <<-SQL
    SELECT tenant_id, event_count
    FROM app.daily_usage
    WHERE %s -- tenant filter
  SQL

  lifecycle {
    replace_triggered_by = [motherduck_share.test.created_ts]
  }
}

data "motherduck_share_secure_views" "audit" {
  share_name = motherduck_share.test.name
  depends_on = [motherduck_share_secure_view.usage]
}
`, databaseName, shareName, visibility, filter)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			return checkDatabaseDestroyed(databaseName)
		},
		Steps: []resource.TestStep{
			{
				Config:           config("hidden", "tenant_id = current_user"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "replaces", "true"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "columns.#", "2"),
					resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.#", "1"),
					resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.0.replaces_table", "true"),
				),
			},
			{
				Config: config("hidden", "tenant_id = current_user AND event_count > 0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share_secure_view.usage", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacing the share drops its secure views. The trigger
				// recreates the policy in the same apply.
				Config: config("discoverable", "tenant_id = current_user AND event_count > 0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share_secure_view.usage", plancheck.ResourceActionDestroyBeforeCreate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:            "motherduck_share_secure_view.usage",
				ImportState:             true,
				ImportStateId:           shareName + ".app.daily_usage",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"query"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					want := "SELECT tenant_id, event_count\nFROM app.daily_usage\nWHERE tenant_id = current_user AND event_count > 0 -- tenant filter"
					if len(states) != 1 || states[0].Attributes["query"] != want {
						return fmt.Errorf("imported query does not match the stored body %q", want)
					}
					return nil
				},
			},
		},
	})
}
