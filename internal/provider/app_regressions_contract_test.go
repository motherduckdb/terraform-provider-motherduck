//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// appRegressionSQL models the MotherDuck semantics of one Flight, its runs,
// one Dive, and one Guide, as verified against mono v1.5.6-2026-10-484 and
// live probes.
type appRegressionSQL struct {
	*contractSQL
	mu sync.Mutex

	// Flight
	flightName    string
	flightCron    *string
	flightStatus  *string
	flightExists  bool
	flightUpdates int

	// Flight runs
	runs      int
	runStatus string

	// Dive
	diveCreates int
	diveID      string
	diveContent string
	diveStatus  string
	diveVersion int64

	// Guide
	guideExists bool
	guideRefs   string
}

func newAppRegressionSQL() *appRegressionSQL {
	return &appRegressionSQL{contractSQL: newContractSQL(), runStatus: "RUNNING"}
}

func (c *appRegressionSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.contractSQL.record("exists", query, args...)
	if len(args) == 2 { // parameter probe
		return false, nil
	}
	return true, nil
}

var (
	appRegressionCronArg    = regexp.MustCompile(`schedule_cron := '([^']*)'`)
	appRegressionContentArg = regexp.MustCompile(`content := '([^']*)'`)
	appRegressionStatusArg  = regexp.MustCompile(`status := '([^']*)'`)
	appRegressionGuideRef   = regexp.MustCompile(`\{'type': '(\w+)', 'url': (?:NULL::VARCHAR|'([^']*)'), .*?'uuid': (?:NULL::UUID|'([^']*)'::UUID)`)
)

func (c *appRegressionSQL) Exec(_ context.Context, query string, args ...any) error {
	c.contractSQL.record("exec", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.HasPrefix(query, "CALL MD_UPDATE_FLIGHT("):
		c.flightUpdates++
		if !strings.Contains(query, ", ") {
			// job_service updateJob rejects an update without fields.
			return errors.New("Invalid Input Error: At least one of the following parameters must be provided")
		}
		if m := appRegressionCronArg.FindStringSubmatch(query); m != nil {
			if m[1] == "" {
				// job_service applyTowerScheduleChanges rejects clearing a
				// schedule that never existed. Otherwise the clear keeps the
				// cron string for audit and disables the schedule.
				if c.flightCron == nil {
					return errors.New("Invalid Input Error: Cannot clear schedule: Flight has no schedule")
				}
				s := "DISABLED"
				c.flightStatus = &s
			} else {
				cron, s := m[1], "ACTIVE"
				c.flightCron, c.flightStatus = &cron, &s
			}
		}
		return nil
	case strings.HasPrefix(query, "CALL MD_DELETE_FLIGHT("):
		c.flightExists = false
		return nil
	}
	return fmt.Errorf("unexpected exec %q", query)
}

func (c *appRegressionSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.contractSQL.record("query-row", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	// Flight
	case strings.Contains(query, "FROM MD_CREATE_FLIGHT("):
		c.flightExists = true
		c.flightName = "regression"
		if m := appRegressionCronArg.FindStringSubmatch(query); m != nil && m[1] != "" {
			cron, s := m[1], "ACTIVE"
			c.flightCron, c.flightStatus = &cron, &s
		}
		return flightDependencyRow{values: []any{contractInitialFlightID}}
	case strings.Contains(query, "FROM MD_GET_FLIGHT_VERSION("):
		return flightDependencyRow{values: []any{"print(1)", "", "MotherDuck Flights", "[]", "{}", int64(28800), "F16"}}
	case strings.Contains(query, "FROM MD_GET_FLIGHT("):
		if strings.Contains(query, "SELECT flight_id::VARCHAR") {
			return flightDependencyRow{values: []any{contractInitialFlightID}}
		}
		if !c.flightExists {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		var cron, status any
		if c.flightCron != nil {
			cron, status = *c.flightCron, *c.flightStatus
		}
		return flightDependencyRow{values: []any{c.flightName, cron, status, "ACTIVE", int64(1), "c", "u", "owner"}}
	// Flight runs
	case strings.Contains(query, "FROM MD_RUN_FLIGHT("):
		c.runs++
		c.runStatus = "RUNNING"
		return flightDependencyRow{values: appRegressionRunRow(int64(c.runs), c.runStatus)}
	case strings.Contains(query, "FROM MD_GET_FLIGHT_RUN("):
		return flightDependencyRow{values: appRegressionRunRow(int64(c.runs), c.runStatus)}
	// Dive
	case strings.Contains(query, "FROM MD_CREATE_DIVE("):
		c.diveCreates++
		c.diveID = fmt.Sprintf("00000000-0000-4000-8000-%012d", c.diveCreates)
		c.diveContent = appRegressionContentArg.FindStringSubmatch(query)[1]
		c.diveStatus = "draft"
		c.diveVersion = 1
		return flightDependencyRow{values: []any{c.diveID}}
	case strings.Contains(query, "FROM MD_GET_DIVE("):
		if c.diveID == "" || !strings.Contains(query, c.diveID) {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		return flightDependencyRow{values: []any{
			"Regression Dive", nil, c.diveVersion, "c", "u", "owner-id", "owner", c.diveContent, nil, "vc", int64(1), "[]",
			c.diveStatus, nil, nil, nil,
		}}
	// Guide: MotherDuck stores a catalog reference as a database id and renders
	// it as md:<database name>, and returns reference UUIDs in lowercase.
	case strings.Contains(query, "FROM MD_CREATE_GUIDE("):
		c.guideExists = true
		refs := []string{}
		for _, m := range appRegressionGuideRef.FindAllStringSubmatch(query, -1) {
			url, guideID, diveID := "null", "null", "null"
			if m[2] != "" {
				url = fmt.Sprintf("%q", strings.ToLower(m[2]))
			}
			switch m[1] {
			case "guide":
				guideID = fmt.Sprintf("%q", strings.ToLower(m[3]))
			case "dive":
				diveID = fmt.Sprintf("%q", strings.ToLower(m[3]))
			}
			refs = append(refs, fmt.Sprintf(`{"type":%q,"url":%s,"schema":null,"table":null,"column":null,"view":null,"macro":null,"guide_id":%s,"dive_id":%s,"flight_id":null,"description":null}`, m[1], url, guideID, diveID))
		}
		c.guideRefs = "[" + strings.Join(refs, ",") + "]"
		return flightDependencyRow{values: []any{contractGuideID}}
	case strings.Contains(query, "FROM MD_GET_GUIDE("):
		if !c.guideExists {
			return flightDependencyRow{err: stdsql.ErrNoRows}
		}
		return flightDependencyRow{values: []any{
			nil, "Regression Guide", nil, "# Guide", nil, nil, "user", "owner-id", "owner",
			int64(1), "c", "u", "vc", c.guideRefs,
		}}
	}
	return flightDependencyRow{err: fmt.Errorf("unexpected query %q", query)}
}

func appRegressionRunRow(n int64, status string) []any {
	return []any{fmt.Sprintf("00000000-0000-4000-8000-%012d", 500+n), status, n, int64(1), "c", false, "s", nil, "s", nil, nil}
}

func (c *appRegressionSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.contractSQL.record("query-json", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(query, "FROM MD_UPDATE_DIVE_STATUS("):
		status := appRegressionStatusArg.FindStringSubmatch(query)[1]
		if status == "endorsed" {
			return "", errors.New("Permission Error: Only organization admins can endorse a Dive")
		}
		c.diveStatus = status
	case strings.Contains(query, "FROM MD_DELETE_DIVE("):
		c.diveID = ""
	case strings.Contains(query, "FROM MD_DELETE_GUIDE("):
		c.guideExists = false
	default:
		return "", fmt.Errorf("unexpected JSON query %q", query)
	}
	return "[]", nil
}

// Removing schedule_cron clears the schedule. MotherDuck keeps the cron string
// with a DISABLED status, which must still read as no schedule.
func TestContractFlightScheduleRemovalReadsAsCleared(t *testing.T) {
	sqlClient := newAppRegressionSQL()
	base := contractProviderConfig("http://127.0.0.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{Config: base + `
resource "motherduck_flight" "test" {
  name          = "regression"
  source_code   = "print(1)"
  schedule_cron = "0 0 1 1 *"
}
`, Check: resource.TestCheckResourceAttr("motherduck_flight.test", "schedule_status", "ACTIVE")},
			{
				Config: base + `
resource "motherduck_flight" "test" {
  name        = "regression"
  source_code = "print(1)"
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("motherduck_flight.test", "schedule_cron"),
					resource.TestCheckResourceAttr("motherduck_flight.test", "schedule_status", "DISABLED"),
				),
			},
			{
				// Setting the schedule again reactivates it in place.
				Config: base + `
resource "motherduck_flight" "test" {
  name          = "regression"
  source_code   = "print(1)"
  schedule_cron = "0 0 1 1 *"
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_flight.test", plancheck.ResourceActionUpdate)}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight.test", "schedule_cron", "0 0 1 1 *"),
					resource.TestCheckResourceAttr("motherduck_flight.test", "schedule_status", "ACTIVE"),
				),
			},
		},
	})
}

// schedule_cron = "" and an omitted schedule_cron both mean no schedule.
// Switching between them must not send schedule_cron := ”, which MotherDuck
// rejects for a Flight without a schedule, or an update without fields.
func TestContractFlightEmptyAndUnsetScheduleAreEquivalent(t *testing.T) {
	sqlClient := newAppRegressionSQL()
	base := contractProviderConfig("http://127.0.0.1")
	noUpdates := func(*terraform.State) error {
		sqlClient.mu.Lock()
		defer sqlClient.mu.Unlock()
		if sqlClient.flightUpdates != 0 {
			return fmt.Errorf("MD_UPDATE_FLIGHT called %d times, want 0", sqlClient.flightUpdates)
		}
		return nil
	}
	empty := base + `
resource "motherduck_flight" "test" {
  name          = "regression"
  source_code   = "print(1)"
  schedule_cron = ""
}
`
	unset := base + `
resource "motherduck_flight" "test" {
  name        = "regression"
  source_code = "print(1)"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{Config: empty},
			{Config: unset, Check: noUpdates},
			{Config: empty, Check: noUpdates},
		},
	})
}

// A wait_for_status timeout keeps the started run in state with a warning, so
// the next apply does not start a second production run. Refresh then reports
// the run's current status, and only a configuration change starts a new run.
func TestContractFlightRunWaitTimeoutKeepsRun(t *testing.T) {
	sqlClient := newAppRegressionSQL()
	base := contractProviderConfig("http://127.0.0.1")
	runs := func(want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			sqlClient.mu.Lock()
			defer sqlClient.mu.Unlock()
			if sqlClient.runs != want {
				return fmt.Errorf("MD_RUN_FLIGHT called %d times, want %d", sqlClient.runs, want)
			}
			return nil
		}
	}
	waiting := base + `
resource "motherduck_flight_run" "test" {
  flight_id             = "` + contractInitialFlightID + `"
  wait_for_status       = "succeeded"
  timeout_seconds       = 1
  poll_interval_seconds = 1
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{
				Config: waiting,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "status", "RUNNING"),
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "run_number", "1"),
					runs(1),
					func(*terraform.State) error {
						sqlClient.mu.Lock()
						defer sqlClient.mu.Unlock()
						sqlClient.runStatus = "SUCCEEDED"
						return nil
					},
				),
			},
			{
				Config: waiting,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_flight_run.test", plancheck.ResourceActionNoop)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "status", "SUCCEEDED"),
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "run_number", "1"),
					runs(1),
				),
			},
			{
				Config: base + `
resource "motherduck_flight_run" "test" {
  flight_id = "` + contractInitialFlightID + `"
  config    = { MODE = "full" }
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_flight_run.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_flight_run.test", "run_number", "2"),
					runs(2),
				),
			},
		},
	})
}

// A status MotherDuck rejects at creation keeps the new Dive in state with a
// warning, so correcting the status updates the same Dive in place.
func TestContractDiveCreateKeepsDiveWhenStatusIsRejected(t *testing.T) {
	sqlClient := newAppRegressionSQL()
	base := contractProviderConfig("http://127.0.0.1")
	cfg := func(status string) string {
		return base + `
resource "motherduck_dive" "test" {
  title   = "Regression Dive"
  content = "v1"
  status  = "` + status + `"
}
`
	}
	creates := func(want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			sqlClient.mu.Lock()
			defer sqlClient.mu.Unlock()
			if sqlClient.diveCreates != want {
				return fmt.Errorf("MD_CREATE_DIVE called %d times, want %d", sqlClient.diveCreates, want)
			}
			return nil
		}
	}
	firstID := "00000000-0000-4000-8000-000000000001"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{
				// The refresh after apply reads the draft status MotherDuck
				// kept, so the plan retries the status.
				Config:             cfg("endorsed"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_dive.test", "id", firstID),
					resource.TestCheckResourceAttr("motherduck_dive.test", "status", "endorsed"),
					creates(1),
				),
			},
			{
				Config: cfg("ready"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_dive.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_dive.test", "id", firstID),
					resource.TestCheckResourceAttr("motherduck_dive.test", "status", "ready"),
					creates(1),
				),
			},
		},
	})
}

// MotherDuck renders a catalog reference as md:<database name> and reference
// UUIDs in lowercase. Equivalent references keep their configured spelling.
func TestContractGuideReferencesKeepConfiguredSpelling(t *testing.T) {
	sqlClient := newAppRegressionSQL()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_guide" "test" {
  title   = "Regression Guide"
  content = "# Guide"
  references = [
    { type = "catalog", url = "md:Analytics" },
    { type = "dive", uuid = "00000000-0000-4000-8000-0000000000AB" },
  ]
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("motherduck_guide.test", "references.0.url", "md:Analytics"),
				resource.TestCheckResourceAttr("motherduck_guide.test", "references.1.uuid", "00000000-0000-4000-8000-0000000000AB"),
			),
		}},
	})
}

// After import, a configured mount URL spelled differently from the canonical
// URL MotherDuck stores names the same database, so the apply that adopts the
// configured spelling writes no new Dive version.
func TestContractDiveImportWithEquivalentMountURLWritesNoVersion(t *testing.T) {
	client := &diveVersionContractSQL{
		contractSQL: newContractSQL(), canonicalMountURLs: true,
		exists: true, title: "Dive", content: "v1", version: 1, apiVersion: 1,
		mounts: []diveContractMount{{Name: "analytics", Alias: "analytics", URL: "md:analytics", ID: "00000000-0000-4000-8000-0000000000db", ResourceType: "database"}},
	}
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_dive" "test" {
  title   = "Dive"
  content = "v1"
  required_resources = [{ alias = "analytics", url = "md:Analytics" }]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "motherduck_dive.test",
				ImportState:        true,
				ImportStateId:      contractDiveID,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: func(*terraform.State) error {
					client.diveMu.Lock()
					defer client.diveMu.Unlock()
					if len(client.contentWrites) != 0 || client.version != 1 || client.apiVersion != 1 {
						return fmt.Errorf("content writes = %d, version = %d, api_version = %d, want no write", len(client.contentWrites), client.version, client.apiVersion)
					}
					return nil
				},
			},
		},
	})
}
