//go:build acceptance && admin_acceptance

package acceptance

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestPluginTestingAdminGraphLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" || os.Getenv("MOTHERDUCK_ADMIN_TOKEN") == "" {
		t.Fatal("SQL and admin tokens are required")
	}
	admin, err := mdrest.New(mdrest.DefaultBaseURL, os.Getenv("MOTHERDUCK_ADMIN_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	probe, err := mdsql.New(t.Context(), mdsql.Config{Token: os.Getenv("MOTHERDUCK_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := probe.Close(); err != nil {
			t.Errorf("close admin SQL probe: %v", err)
		}
	})
	name := fmt.Sprintf("tf_admin_graph_%d", time.Now().UnixNano())
	config := func(cooldown int, tokenName string) string {
		return fmt.Sprintf(`
resource "motherduck_service_account" "test" { username = %q }
resource "motherduck_duckling_config" "test" {
 username = motherduck_service_account.test.username
 read_write_instance_size = "standard"
 read_write_cooldown_seconds = %d
 read_scaling_instance_size = "standard"
 read_scaling_cooldown_seconds = 60
 read_scaling_flock_size = 1
}
resource "motherduck_access_token" "test" {
 username = motherduck_service_account.test.username
 name = %q
 token_type = "read_write"
 ttl = 3600
}
resource "motherduck_role" "test" { name = %q }
resource "motherduck_role_grant" "test" {
 role_name = motherduck_role.test.name
 grantee_name = motherduck_service_account.test.username
 grantee_type = "user"
}
`, name, cooldown, tokenName, name)
	}
	initial := config(60, "initial")
	updated := config(90, "rotated")
	var tokenID string
	check := func(wantCooldown int) resource.TestCheckFunc {
		return func(state *terraform.State) error {
			cfg, err := admin.GetDucklingConfig(t.Context(), name)
			if err != nil {
				return err
			}
			if cfg.ReadWrite.CooldownSeconds == nil || *cfg.ReadWrite.CooldownSeconds != int64(wantCooldown) {
				return fmt.Errorf("read-write cooldown does not match %d", wantCooldown)
			}
			tokens, err := admin.ListTokens(t.Context(), name)
			if err != nil {
				return err
			}
			tokenID = state.RootModule().Resources["motherduck_access_token.test"].Primary.ID
			if len(tokens) != 1 || tokens[0].ID != tokenID {
				return fmt.Errorf("expected only the current managed token")
			}
			exists, err := probe.QueryRowsJSON(t.Context(), fmt.Sprintf("SHOW USERS OF ROLE %s", name))
			if err != nil {
				return err
			}
			if !strings.Contains(exists, name) {
				return fmt.Errorf("managed role membership missing")
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			_, err := admin.GetDucklingConfig(t.Context(), name)
			var apiError *mdrest.APIError
			if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusNotFound {
				return fmt.Errorf("test account still exists or could not be checked: %v", err)
			}
			rows, err := probe.QueryRowsJSON(t.Context(), "SHOW ALL ROLES")
			if err != nil {
				return err
			}
			if strings.Contains(rows, name) {
				return fmt.Errorf("test role still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: check(60), ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: initial, ResourceName: "motherduck_service_account.test", ImportState: true, ImportStateVerify: true},
			{Config: initial, ResourceName: "motherduck_duckling_config.test", ImportState: true, ImportStateVerify: true},
			{Config: initial, ResourceName: "motherduck_role_grant.test", ImportState: true, ImportStateVerify: true},
			{Config: updated, Check: check(90), ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate), plancheck.ExpectResourceAction("motherduck_access_token.test", plancheck.ResourceActionReplace)},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			}},
			{Config: updated, PreConfig: func() {
				if err := probe.Exec(t.Context(), fmt.Sprintf("REVOKE ROLE %s FROM USER %s", name, name)); err != nil {
					t.Fatal(err)
				}
				if err := admin.DeleteToken(t.Context(), name, tokenID); err != nil {
					t.Fatal(err)
				}
			}, Check: check(90), ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_role_grant.test", plancheck.ResourceActionCreate), plancheck.ExpectResourceAction("motherduck_access_token.test", plancheck.ResourceActionCreate)},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			}},
		},
	})
}
