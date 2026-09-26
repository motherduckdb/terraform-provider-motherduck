//go:build acceptance && iceberg_acceptance

package acceptance

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestPluginTestingIcebergDatabaseLifecycle needs an existing Iceberg REST
// catalog and a MotherDuck secret that can reach it, so it builds only with the
// iceberg_acceptance tag and requires MOTHERDUCK_ICEBERG_SECRET and
// MOTHERDUCK_ICEBERG_DEFAULT_SCHEMA.
// MOTHERDUCK_ICEBERG_ENDPOINT, MOTHERDUCK_ICEBERG_WAREHOUSE, and
// MOTHERDUCK_ICEBERG_ENDPOINT_TYPE are passed through when set.
func TestPluginTestingIcebergDatabaseLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}
	secret := os.Getenv("MOTHERDUCK_ICEBERG_SECRET")
	defaultSchema := os.Getenv("MOTHERDUCK_ICEBERG_DEFAULT_SCHEMA")
	if secret == "" || defaultSchema == "" {
		t.Fatal("MOTHERDUCK_ICEBERG_SECRET and MOTHERDUCK_ICEBERG_DEFAULT_SCHEMA are required for the Iceberg database acceptance test")
	}

	databaseName := fmt.Sprintf("tf_acc_iceberg_%d", time.Now().UTC().UnixNano())
	identity := []string{
		fmt.Sprintf("    secret         = %q", secret),
		fmt.Sprintf("    default_schema = %q", defaultSchema),
	}
	for attribute, env := range map[string]string{
		"endpoint":      "MOTHERDUCK_ICEBERG_ENDPOINT",
		"warehouse":     "MOTHERDUCK_ICEBERG_WAREHOUSE",
		"endpoint_type": "MOTHERDUCK_ICEBERG_ENDPOINT_TYPE",
	} {
		if value := os.Getenv(env); value != "" {
			identity = append(identity, fmt.Sprintf("    %s = %q", attribute, value))
		}
	}
	config := func(staleness string) string {
		return fmt.Sprintf(`
resource "motherduck_database" "test" {
  name          = %q
  database_type = "iceberg"

  iceberg = {
%s
    max_table_staleness = %q
  }
}
`, databaseName, strings.Join(identity, "\n"), staleness)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			return checkDatabaseDestroyed(databaseName)
		},
		Steps: []resource.TestStep{
			{
				Config: config("1 minute"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "database_type", "iceberg"),
			},
			{
				Config: config("2 minutes"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:            "motherduck_database.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"iceberg"},
			},
		},
	})
}
