//go:build contract

package provider

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// flightRunGrantContractSQL models a principal that holds a Flight run grant
// but cannot see the Flight. MD_RUN_FLIGHT succeeds, while every read of the
// Flight or its runs reports the Flight as not found, as MotherDuck does.
type flightRunGrantContractSQL struct {
	*contractSQL
	runs atomic.Int32
}

func (c *flightRunGrantContractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function check %q", query)
	}
	switch args[0] {
	case "md_run_flight", "md_get_flight_run", "md_list_flight_runs", "md_get_flight":
		return true, nil
	}
	return false, nil
}

func (c *flightRunGrantContractSQL) QueryRow(_ context.Context, query string, _ ...any) mdsql.RowScanner {
	switch {
	case strings.Contains(query, "FROM MD_RUN_FLIGHT("):
		c.runs.Add(1)
		return flightRunContractRow{}
	case strings.Contains(query, "FROM MD_GET_FLIGHT_RUN("), strings.Contains(query, "FROM MD_GET_FLIGHT("):
		return contractRow{err: flightRunGrantNotFound()}
	}
	return contractRow{err: fmt.Errorf("unexpected Flight query %q", query)}
}

func (c *flightRunGrantContractSQL) QueryRowsJSON(_ context.Context, query string, _ ...any) (string, error) {
	if strings.Contains(query, "FROM MD_LIST_FLIGHT_RUNS(") {
		return "", flightRunGrantNotFound()
	}
	return "", fmt.Errorf("unexpected Flight listing %q", query)
}

func flightRunGrantNotFound() error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Flight "11111111-1111-4111-8111-111111111111" not found`}
}

func TestContractFlightRunGrantDoesNotRerunWhenFlightIsNotVisible(t *testing.T) {
	client := &flightRunGrantContractSQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_flight_run" "test" {
  flight_id = "11111111-1111-4111-8111-111111111111"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.runs.Load(); got != 1 {
				return fmt.Errorf("execution count = %d, want 1", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "run_number", "1"),
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "exit_code", "0"),
				),
			},
			{
				// A second refresh and plan must keep the run instead of
				// starting another one.
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
