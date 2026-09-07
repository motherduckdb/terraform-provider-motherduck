//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

const (
	contractInitialFlightID = "00000000-0000-4000-8000-000000000001"
	contractOtherFlightID   = "00000000-0000-4000-8000-000000000002"
)

var (
	contractFlightIDPattern  = regexp.MustCompile(`flight_id := '([^']+)'::UUID`)
	contractRunNumberPattern = regexp.MustCompile(`run_number = ([0-9]+)`)
)

type flightDependencyContractSQL struct {
	*contractSQL

	flightsMu sync.Mutex
	flights   map[string]contractFlightDefinition

	runsMu        sync.Mutex
	runsByID      map[string]contractFlightRun
	runs          atomic.Int32
	flightUpdates atomic.Int32
}

type contractFlightDefinition struct {
	id      string
	name    string
	source  string
	version int64
}

type contractFlightRun struct {
	id            string
	flightID      string
	runNumber     int64
	flightVersion int64
	status        string
	created       string
}

func newFlightDependencyContractSQL() *flightDependencyContractSQL {
	return &flightDependencyContractSQL{
		contractSQL: newContractSQL(),
		flights:     map[string]contractFlightDefinition{},
		runsByID:    map[string]contractFlightRun{},
	}
}

func (c *flightDependencyContractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.contractSQL.record("exists", query, args...)
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function check %q with args %#v", query, args)
	}
	switch strings.ToLower(fmt.Sprint(args[0])) {
	case "md_create_flight", "md_get_flight", "md_get_flight_version", "md_update_flight", "md_delete_flight", "md_run_flight", "md_list_flight_runs":
		return true, nil
	default:
		return false, fmt.Errorf("unexpected function check for %q", args[0])
	}
}

func (c *flightDependencyContractSQL) Exec(_ context.Context, query string, args ...any) error {
	c.contractSQL.record("exec", query, args...)
	if len(args) != 0 {
		return fmt.Errorf("unexpected Flight exec args: %#v", args)
	}

	switch {
	case strings.HasPrefix(query, "CALL MD_UPDATE_FLIGHT("):
		id, err := contractFlightID(query)
		if err != nil {
			return err
		}
		if !strings.Contains(query, "source_code := 'print(''v2'')'") {
			return fmt.Errorf("unexpected Flight update %q", query)
		}
		c.flightsMu.Lock()
		flight, ok := c.flights[id]
		if ok {
			flight.source = "print('v2')"
			c.flights[id] = flight
		}
		c.flightsMu.Unlock()
		if !ok {
			return fmt.Errorf("updated unknown Flight %q", id)
		}
		c.flightUpdates.Add(1)
		return nil
	case strings.HasPrefix(query, "CALL MD_DELETE_FLIGHT("):
		id, err := contractFlightID(query)
		if err != nil {
			return err
		}
		c.flightsMu.Lock()
		defer c.flightsMu.Unlock()
		if _, ok := c.flights[id]; !ok {
			return fmt.Errorf("deleted unknown Flight %q", id)
		}
		delete(c.flights, id)
		return nil
	default:
		return fmt.Errorf("unexpected Flight exec %q", query)
	}
}

func (c *flightDependencyContractSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.contractSQL.record("query-row", query, args...)
	if len(args) != 0 {
		return flightDependencyRow{err: fmt.Errorf("unexpected Flight query args: %#v", args)}
	}

	switch {
	case strings.Contains(query, "FROM MD_CREATE_FLIGHT("):
		flight, err := c.createFlight(query)
		if err != nil {
			return flightDependencyRow{err: err}
		}
		return flightDependencyRow{values: []any{flight.id}}
	case strings.Contains(query, "FROM MD_GET_FLIGHT_VERSION("):
		id, err := contractFlightID(query)
		if err != nil {
			return flightDependencyRow{err: err}
		}
		c.flightsMu.Lock()
		flight, ok := c.flights[id]
		c.flightsMu.Unlock()
		if !ok {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		if !strings.Contains(query, "version_number := 1") || flight.version != 1 {
			return flightDependencyRow{err: fmt.Errorf("unexpected Flight version query %q", query)}
		}
		return flightDependencyRow{values: []any{
			flight.source,
			nil,
			nil,
			nil,
			nil,
			nil,
		}}
	case strings.Contains(query, "FROM MD_GET_FLIGHT("):
		id, err := contractFlightID(query)
		if err != nil {
			return flightDependencyRow{err: err}
		}
		c.flightsMu.Lock()
		flight, ok := c.flights[id]
		c.flightsMu.Unlock()
		if !ok {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		return flightDependencyRow{values: []any{
			flight.name,
			nil,
			"ACTIVE",
			flight.version,
			"2026-09-01T00:00:00Z",
			"2026-09-01T00:00:00Z",
		}}
	case strings.Contains(query, "FROM MD_RUN_FLIGHT("):
		id, err := contractFlightID(query)
		if err != nil {
			return flightDependencyRow{err: err}
		}
		c.flightsMu.Lock()
		flight, ok := c.flights[id]
		c.flightsMu.Unlock()
		if !ok {
			return flightDependencyRow{err: fmt.Errorf("ran unknown Flight %q", id)}
		}
		runNumber := int64(c.runs.Add(1))
		run := contractFlightRun{
			id:            fmt.Sprintf("00000000-0000-4000-8000-%012d", 100+runNumber),
			flightID:      id,
			runNumber:     runNumber,
			flightVersion: flight.version,
			status:        "SUCCEEDED",
			created:       "2026-09-06T00:00:00Z",
		}
		c.runsMu.Lock()
		c.runsByID[run.id] = run
		c.runsMu.Unlock()
		return flightDependencyRow{values: []any{run.id, run.status, run.runNumber, run.flightVersion, run.created}}
	case strings.Contains(query, "FROM MD_LIST_FLIGHT_RUNS("):
		id, err := contractFlightID(query)
		if err != nil {
			return flightDependencyRow{err: err}
		}
		match := contractRunNumberPattern.FindStringSubmatch(query)
		if len(match) != 2 {
			return flightDependencyRow{err: fmt.Errorf("missing Flight run number in %q", query)}
		}
		runNumber, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return flightDependencyRow{err: fmt.Errorf("invalid Flight run number in %q: %w", query, err)}
		}
		c.runsMu.Lock()
		var run contractFlightRun
		found := false
		for _, candidate := range c.runsByID {
			if candidate.flightID == id && candidate.runNumber == runNumber {
				run = candidate
				found = true
				break
			}
		}
		c.runsMu.Unlock()
		if !found {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		return flightDependencyRow{values: []any{run.id, run.status, run.runNumber, run.flightVersion, run.created}}
	default:
		return flightDependencyRow{err: fmt.Errorf("unexpected Flight query %q", query)}
	}
}

func (c *flightDependencyContractSQL) createFlight(query string) (contractFlightDefinition, error) {
	var flight contractFlightDefinition
	switch {
	case strings.Contains(query, "name := 'contract_initial_flight'"):
		flight = contractFlightDefinition{
			id:      contractInitialFlightID,
			name:    "contract_initial_flight",
			source:  "print('v1')",
			version: 1,
		}
		if !strings.Contains(query, "source_code := 'print(''v1'')'") {
			return contractFlightDefinition{}, fmt.Errorf("unexpected initial Flight create %q", query)
		}
	case strings.Contains(query, "name := 'contract_other_flight'"):
		flight = contractFlightDefinition{
			id:      contractOtherFlightID,
			name:    "contract_other_flight",
			source:  "print('other')",
			version: 1,
		}
		if !strings.Contains(query, "source_code := 'print(''other'')'") {
			return contractFlightDefinition{}, fmt.Errorf("unexpected other Flight create %q", query)
		}
	default:
		return contractFlightDefinition{}, fmt.Errorf("unexpected Flight create %q", query)
	}

	c.flightsMu.Lock()
	defer c.flightsMu.Unlock()
	if _, exists := c.flights[flight.id]; exists {
		return contractFlightDefinition{}, fmt.Errorf("duplicate Flight create %q", flight.id)
	}
	c.flights[flight.id] = flight
	return flight, nil
}

func contractFlightID(query string) (string, error) {
	match := contractFlightIDPattern.FindStringSubmatch(query)
	if len(match) != 2 {
		return "", fmt.Errorf("missing Flight ID in %q", query)
	}
	return match[1], nil
}

func (c *flightDependencyContractSQL) flightIDsForRuns() []string {
	c.runsMu.Lock()
	defer c.runsMu.Unlock()
	ids := make([]string, 0, len(c.runsByID))
	for runNumber := int64(1); runNumber <= int64(len(c.runsByID)); runNumber++ {
		for _, run := range c.runsByID {
			if run.runNumber == runNumber {
				ids = append(ids, run.flightID)
				break
			}
		}
	}
	return ids
}

type flightDependencyRow struct {
	values []any
	err    error
}

func (r flightDependencyRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("Flight scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *string:
			if value == nil {
				return fmt.Errorf("nil Flight string at index %d", i)
			}
			*target = value.(string)
		case *int64:
			if value == nil {
				return fmt.Errorf("nil Flight integer at index %d", i)
			}
			*target = value.(int64)
		case *stdsql.NullString:
			if value == nil {
				*target = stdsql.NullString{}
			} else {
				*target = stdsql.NullString{String: value.(string), Valid: true}
			}
		case *stdsql.NullInt64:
			if value == nil {
				*target = stdsql.NullInt64{}
			} else {
				*target = stdsql.NullInt64{Int64: value.(int64), Valid: true}
			}
		default:
			return fmt.Errorf("unsupported Flight scan destination %T at index %d", dest[i], i)
		}
	}
	return nil
}

func TestContractFlightDefinitionDependencyLifecycle(t *testing.T) {
	sqlClient := newFlightDependencyContractSQL()
	config := flightDependencyContractConfig("print('v1')", "motherduck_flight.initial.id")
	updatedDefinition := flightDependencyContractConfig("print('v2')", "motherduck_flight.initial.id")
	updatedReference := flightDependencyContractConfig("print('v2')", "motherduck_flight.other.id")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			sqlClient.flightsMu.Lock()
			flightCount := len(sqlClient.flights)
			sqlClient.flightsMu.Unlock()
			if flightCount != 0 {
				return fmt.Errorf("Flights still exist after destroy: %d", flightCount)
			}
			if got := sqlClient.runs.Load(); got != 2 {
				return fmt.Errorf("Flight executions = %d, want 2 (initial create and changed reference)", got)
			}
			if got := sqlClient.flightUpdates.Load(); got != 1 {
				return fmt.Errorf("Flight updates = %d, want 1 (source code update)", got)
			}
			want := []string{contractInitialFlightID, contractOtherFlightID}
			if got := sqlClient.flightIDsForRuns(); fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("Flight execution references = %v, want %v", got, want)
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
					resource.TestCheckResourceAttr("motherduck_flight.initial", "id", contractInitialFlightID),
					resource.TestCheckResourceAttr("motherduck_flight_run.dependent", "flight_id", contractInitialFlightID),
					checkFlightRunCount(sqlClient, 1),
				),
			},
			{
				Config: updatedDefinition,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_flight.initial", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("motherduck_flight_run.dependent", plancheck.ResourceActionNoop),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight.initial", "source_code", "print('v2')"),
					resource.TestCheckResourceAttr("motherduck_flight_run.dependent", "flight_id", contractInitialFlightID),
					checkFlightRunCount(sqlClient, 1),
				),
			},
			{
				Config: updatedReference,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_flight_run.dependent", plancheck.ResourceActionReplace),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight_run.dependent", "flight_id", contractOtherFlightID),
					checkFlightRunCount(sqlClient, 2),
				),
			},
		},
	})
}

func checkFlightRunCount(client *flightDependencyContractSQL, want int32) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := client.runs.Load(); got != want {
			return fmt.Errorf("Flight executions = %d, want %d", got, want)
		}
		return nil
	}
}

func flightDependencyContractConfig(sourceCode, flightReference string) string {
	return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_flight" "initial" {
  name        = "contract_initial_flight"
  source_code = %q
}

resource "motherduck_flight" "other" {
  name        = "contract_other_flight"
  source_code = "print('other')"
}

resource "motherduck_flight_run" "dependent" {
  flight_id = %s
}
`, sourceCode, flightReference)
}
