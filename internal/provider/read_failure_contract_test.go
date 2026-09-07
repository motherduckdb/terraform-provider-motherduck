//go:build contract

package provider

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestContractDatabaseReadErrorPreservesStateAndRecovers(t *testing.T) {
	sqlClient := newContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_database" "test" {
  name = "contract_database"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if sqlClient.databaseExists {
				return errors.New("contract database still exists after destroy")
			}
			if got := sqlClient.countCalls(`exec CREATE DATABASE "contract_database"`); got != 1 {
				return fmt.Errorf("database creates = %d, want 1 after read failure recovery", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{
				RefreshState: true,
				PreConfig: func() {
					sqlClient.mu.Lock()
					sqlClient.databaseReadErr = errors.New("database read backend unavailable")
					sqlClient.mu.Unlock()
				},
				ExpectError: regexp.MustCompile("database read backend unavailable"),
				Check:       resource.TestCheckResourceAttr("motherduck_database.test", "id", "contract_database"),
			},
			{
				Config: config,
				PreConfig: func() {
					sqlClient.mu.Lock()
					sqlClient.databaseReadErr = nil
					sqlClient.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr("motherduck_database.test", "id", "contract_database"),
			},
		},
	})
}

func TestContractAccessTokenReadErrorPreservesStateAndRecovers(t *testing.T) {
	sqlClient := newContractSQL()
	restClient := newContractREST(t)
	config := contractProviderConfig(restClient.URL()) + `
resource "motherduck_access_token" "test" {
  username   = "contract_user"
  name       = "contract token"
  token_type = "read_write"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			restClient.mu.Lock()
			defer restClient.mu.Unlock()
			if restClient.token != nil {
				return errors.New("contract access token still exists after destroy")
			}
			if restClient.createCount != 1 {
				return fmt.Errorf("token creates = %d, want 1 after read failure recovery", restClient.createCount)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{
				RefreshState: true,
				PreConfig: func() {
					restClient.mu.Lock()
					restClient.listErr = mdrest.APIError{StatusCode: http.StatusForbidden, Code: "FORBIDDEN", Message: "token list forbidden"}
					restClient.mu.Unlock()
				},
				ExpectError: regexp.MustCompile("token list forbidden"),
			},
			{
				Config: config,
				PreConfig: func() {
					restClient.mu.Lock()
					restClient.listErr = nil
					restClient.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_access_token.test", "id", "token-1"),
					resource.TestCheckResourceAttr("motherduck_access_token.test", "token", "md_contract_secret_1"),
				),
			},
		},
	})
}
