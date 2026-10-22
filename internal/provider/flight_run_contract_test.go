//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

type flightRunContractSQL struct {
	*contractSQL
	runs atomic.Int32
}

func (c *flightRunContractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function check %q", query)
	}
	return args[0] == "md_run_flight" || args[0] == "md_list_flight_runs", nil
}

func (c *flightRunContractSQL) QueryRow(_ context.Context, query string, _ ...any) mdsql.RowScanner {
	if strings.Contains(query, "FROM MD_RUN_FLIGHT(") {
		c.runs.Add(1)
	} else if !strings.Contains(query, "FROM MD_LIST_FLIGHT_RUNS(") {
		return contractRow{err: fmt.Errorf("unexpected Flight query %q", query)}
	}
	return flightRunContractRow{}
}

// flightRunContractRow is the run MD_RUN_FLIGHT returns. An empty status
// reads as SUCCEEDED.
type flightRunContractRow struct{ status string }

func (r flightRunContractRow) Scan(dest ...any) error {
	if len(dest) != 11 {
		return fmt.Errorf("unexpected scan width %d", len(dest))
	}
	status := r.status
	if status == "" {
		status = "SUCCEEDED"
	}
	*dest[0].(*string) = "11111111-1111-4111-8111-111111111112"
	*dest[1].(*string) = status
	*dest[2].(*int64) = 1
	*dest[3].(*int64) = 1
	*dest[4].(*string) = "2026-09-06T00:00:00Z"
	*dest[5].(*stdsql.NullBool) = stdsql.NullBool{Valid: true}
	*dest[6].(*stdsql.NullString) = stdsql.NullString{String: "2026-09-06T00:00:05Z", Valid: true}
	*dest[7].(*stdsql.NullString) = stdsql.NullString{String: "2026-09-06T00:01:00Z", Valid: true}
	*dest[8].(*stdsql.NullString) = stdsql.NullString{}
	*dest[9].(*stdsql.NullString) = stdsql.NullString{}
	*dest[10].(*stdsql.NullInt64) = stdsql.NullInt64{Int64: 0, Valid: true}
	return nil
}

func TestContractFlightWaitPolicyDoesNotRerun(t *testing.T) {
	client := &flightRunContractSQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_flight_run" "test" {
 flight_id = "11111111-1111-4111-8111-111111111111"
 poll_interval_seconds = 10
 timeout_seconds = 600
}
`
	updated := strings.ReplaceAll(strings.ReplaceAll(config, "= 10", "= 20"), "= 600", "= 900")
	updated = strings.Replace(updated, "timeout_seconds = 900", "timeout_seconds = 900\nwait_for_status = \"succeeded\"", 1)
	metadataCheck := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "id", "11111111-1111-4111-8111-111111111112"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "status", "SUCCEEDED"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "run_number", "1"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "flight_version", "1"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "created_at", "2026-09-06T00:00:00Z"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "is_scheduled", "false"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "started_at", "2026-09-06T00:00:05Z"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "ended_at", "2026-09-06T00:01:00Z"),
		resource.TestCheckNoResourceAttr("motherduck_flight_run.test", "scheduled_at"),
		resource.TestCheckNoResourceAttr("motherduck_flight_run.test", "cancelled_at"),
		resource.TestCheckResourceAttr("motherduck_flight_run.test", "exit_code", "0"),
	)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.runs.Load(); got != 1 {
				return fmt.Errorf("execution count = %d, want 1", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: metadataCheck},
			{Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_flight_run.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: metadataCheck,
			},
		},
	})
}
