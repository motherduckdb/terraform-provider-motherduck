//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

const (
	contractDiveID  = "00000000-0000-4000-8000-00000000d1fe"
	contractGuideID = "00000000-0000-4000-8000-0000000091de"
)

// appContractSQL keeps one Dive and one Guide in memory. It answers only the
// public SQL functions the app resources call, so any other query fails.
type appContractSQL struct {
	*contractSQL

	appMu        sync.Mutex
	dive         bool
	guide        bool
	diveCreates  int
	guideCreates int
}

func newAppContractSQL() *appContractSQL {
	return &appContractSQL{contractSQL: newContractSQL()}
}

func (c *appContractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.contractSQL.record("exists", query, args...)
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function check %q", query)
	}
	switch strings.ToLower(fmt.Sprint(args[0])) {
	case "md_create_dive", "md_get_dive", "md_delete_dive", "md_create_guide", "md_get_guide", "md_delete_guide":
		return true, nil
	case "md_update_dive_status":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected function check for %q", args[0])
	}
}

func (c *appContractSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.contractSQL.record("query-row", query, args...)
	c.appMu.Lock()
	defer c.appMu.Unlock()
	switch {
	case strings.Contains(query, "FROM MD_CREATE_DIVE("):
		if c.dive {
			return flightDependencyRow{err: errors.New("duplicate Dive create")}
		}
		c.dive = true
		c.diveCreates++
		return flightDependencyRow{values: []any{contractDiveID}}
	case strings.Contains(query, "FROM MD_GET_DIVE("):
		if !c.dive || !strings.Contains(query, contractDiveID) {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		return flightDependencyRow{values: []any{"Contract Dive", nil, int64(1), "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z", "owner", "export default () => null"}}
	case strings.Contains(query, "FROM MD_CREATE_GUIDE("):
		if c.guide {
			return flightDependencyRow{err: errors.New("duplicate Guide create")}
		}
		c.guide = true
		c.guideCreates++
		return flightDependencyRow{values: []any{contractGuideID}}
	case strings.Contains(query, "FROM MD_GET_GUIDE("):
		if !c.guide || !strings.Contains(query, contractGuideID) {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		// topic and description are reported as empty strings when unset.
		return flightDependencyRow{values: []any{
			"", "Contract Guide", "", "# Guide", nil, nil, "USER", "owner-id", "owner",
			int64(1), "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z", "[]",
		}}
	default:
		return flightDependencyRow{err: fmt.Errorf("unexpected app query %q", query)}
	}
}

func (c *appContractSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.contractSQL.record("query-json", query, args...)
	c.appMu.Lock()
	defer c.appMu.Unlock()
	switch {
	case strings.Contains(query, "FROM MD_DELETE_DIVE("):
		c.dive = false
	case strings.Contains(query, "FROM MD_DELETE_GUIDE("):
		c.guide = false
	default:
		return "", fmt.Errorf("unexpected app JSON query %q", query)
	}
	return "[]", nil
}

func importNoopSteps(address, id, config string) []resource.TestStep {
	return []resource.TestStep{
		{
			Config: config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
		},
		{
			ResourceName:      address,
			ImportState:       true,
			ImportStateId:     id,
			ImportStateVerify: true,
		},
		{
			// Plan an import block against the same configuration. The
			// harness fails unless the imported object plans as a no-op.
			ResourceName:    address,
			ImportState:     true,
			ImportStateKind: resource.ImportBlockWithID,
			ImportStateId:   id,
			Config:          config,
		},
	}
}

func TestContractDiveImportIsNoop(t *testing.T) {
	sqlClient := newAppContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_dive" "test" {
  title   = "Contract Dive"
  content = "export default () => null"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if sqlClient.dive {
				return errors.New("contract Dive still exists after destroy")
			}
			if sqlClient.diveCreates != 1 {
				return fmt.Errorf("Dive creates = %d, want 1 (import must not recreate)", sqlClient.diveCreates)
			}
			return nil
		},
		Steps: importNoopSteps("motherduck_dive.test", contractDiveID, config),
	})
}

func TestContractGuideImportIsNoop(t *testing.T) {
	sqlClient := newAppContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_guide" "test" {
  title   = "Contract Guide"
  content = "# Guide"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if sqlClient.guide {
				return errors.New("contract Guide still exists after destroy")
			}
			if sqlClient.guideCreates != 1 {
				return fmt.Errorf("Guide creates = %d, want 1 (import must not recreate)", sqlClient.guideCreates)
			}
			return nil
		},
		Steps: importNoopSteps("motherduck_guide.test", contractGuideID, config),
	})
}

func TestContractFlightImportIsNoop(t *testing.T) {
	sqlClient := newFlightDependencyContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_flight" "initial" {
  name        = "contract_initial_flight"
  source_code = "print('v1')"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			sqlClient.flightsMu.Lock()
			defer sqlClient.flightsMu.Unlock()
			if len(sqlClient.flights) != 0 {
				return errors.New("contract Flight still exists after destroy")
			}
			if got := countCallsContaining(sqlClient.contractSQL, "FROM MD_CREATE_FLIGHT("); got != 1 {
				return fmt.Errorf("Flight creates = %d, want 1 (import must not recreate)", got)
			}
			return nil
		},
		Steps: importNoopSteps("motherduck_flight.initial", contractInitialFlightID, config),
	})
}

func countCallsContaining(c *contractSQL, fragment string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if strings.Contains(call.query, fragment) {
			count++
		}
	}
	return count
}
