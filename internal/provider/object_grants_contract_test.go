//go:build contract

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	contractGrantDiveID   = "33333333-3333-4333-8333-333333333333"
	contractGrantFlightID = "44444444-4444-4444-8444-444444444444"
)

// objectGrantsSQL keeps Dive and Flight grants in memory and answers the grant
// statements and grantee listings the way MotherDuck does. The organization
// grant is listed under the organization name.
type objectGrantsSQL struct {
	*contractSQL
	mu      sync.Mutex
	grants  map[string]bool
	deleted map[string]bool
	execs   []string
}

func newObjectGrantsSQL() *objectGrantsSQL {
	return &objectGrantsSQL{contractSQL: newContractSQL(), grants: map[string]bool{}, deleted: map[string]bool{}}
}

var (
	objectGrantStatement = regexp.MustCompile(`^(GRANT|REVOKE) (READ|RUN) ON (DIVE|FLIGHT) '([0-9a-f-]{36})' (?:TO|FROM) (?:(USER|ROLE) "([^"]+)"|(ORGANIZATION))$`)
	objectGranteeListing = regexp.MustCompile(`^SELECT grantee_name, grantee_type, privilege FROM MD_LIST_(DIVE|FLIGHT)_GRANTEES\('([0-9a-f-]{36})'::UUID\)`)
)

func (c *objectGrantsSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function probe: %q %#v", query, args)
	}
	name, _ := args[0].(string)
	return name == "md_list_dive_grantees" || name == "md_list_flight_grantees", nil
}

func (c *objectGrantsSQL) Exec(_ context.Context, query string, args ...any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := objectGrantStatement.FindStringSubmatch(query)
	if m == nil || len(args) != 0 {
		return fmt.Errorf("unexpected statement: %q %#v", query, args)
	}
	object, id := strings.ToLower(m[3]), m[4]
	if (object == "dive") != (m[2] == "READ") {
		return fmt.Errorf("Invalid Input Error: Permission '%s' is not supported on a %s", strings.ToLower(m[2]), object)
	}
	if c.deleted[id] {
		return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Could not find " + strings.ToUpper(object[:1]) + object[1:]}
	}
	key := id + "|organization|"
	if m[7] == "" {
		key = id + "|" + strings.ToLower(m[5]) + "|" + strings.ToLower(m[6])
	}
	c.execs = append(c.execs, query)
	if m[1] == "GRANT" {
		c.grants[key] = true
	} else {
		delete(c.grants, key)
	}
	return nil
}

func (c *objectGrantsSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := objectGranteeListing.FindStringSubmatch(query)
	if m == nil || len(args) != 0 {
		return "", fmt.Errorf("unexpected query: %q %#v", query, args)
	}
	object, id := strings.ToLower(m[1]), m[2]
	if c.deleted[id] {
		return "", &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Could not find " + strings.ToUpper(object[:1]) + object[1:] + " with id '" + id + "'"}
	}
	privilege := "run"
	if object == "dive" {
		privilege = "read"
	}
	rows := []map[string]string{}
	for key := range c.grants {
		parts := strings.Split(key, "|")
		if parts[0] != id {
			continue
		}
		name := parts[2]
		if parts[1] == "organization" {
			name = "Contract Org"
		}
		rows = append(rows, map[string]string{"grantee_name": name, "grantee_type": parts[1], "privilege": privilege})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["grantee_type"]+rows[i]["grantee_name"] < rows[j]["grantee_type"]+rows[j]["grantee_name"]
	})
	data, err := json.Marshal(rows)
	return string(data), err
}

func (c *objectGrantsSQL) revoke(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.grants, key)
}

func (c *objectGrantsSQL) granted(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.grants[key]
}

func (c *objectGrantsSQL) count(fragment string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, query := range c.execs {
		if strings.Contains(query, fragment) {
			n++
		}
	}
	return n
}

// objectGrantsConfig declares a role grant on the Dive and a user grant on the
// Flight, and optionally the organization grant on the Dive and the grantee
// data sources, which fail for a deleted object.
func objectGrantsConfig(roleSpelling string, withOrganization, withDataSources bool) string {
	config := contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_dive_grant" "finance" {
  dive_id      = %q
  grantee_type = "role"
  grantee_name = %q
}

resource "motherduck_flight_grant" "runner" {
  flight_id    = %q
  grantee_name = "svc_runner"
}
`, contractGrantDiveID, roleSpelling, contractGrantFlightID)
	if withDataSources {
		config += fmt.Sprintf(`
data "motherduck_dive_grantees" "test" {
  dive_id    = %q
  depends_on = [motherduck_dive_grant.finance]
}

data "motherduck_flight_grantees" "test" {
  flight_id  = %q
  depends_on = [motherduck_flight_grant.runner]
}
`, contractGrantDiveID, contractGrantFlightID)
	}
	if withOrganization {
		config += fmt.Sprintf(`
resource "motherduck_dive_grant" "everyone" {
  dive_id      = %q
  grantee_type = "organization"
}
`, contractGrantDiveID)
	}
	return config
}

func TestContractObjectGrantLifecycle(t *testing.T) {
	client := newObjectGrantsSQL()
	// MotherDuck can create a Dive with the organization grant in place.
	client.grants[contractGrantDiveID+"|organization|"] = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{
				Config: objectGrantsConfig("finance", true, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_dive_grant.finance", "id", contractGrantDiveID+"/role/finance"),
					resource.TestCheckResourceAttr("motherduck_dive_grant.everyone", "id", contractGrantDiveID+"/organization"),
					resource.TestCheckNoResourceAttr("motherduck_dive_grant.everyone", "grantee_name"),
					resource.TestCheckResourceAttr("motherduck_flight_grant.runner", "id", contractGrantFlightID+"/user/svc_runner"),
					resource.TestCheckResourceAttr("motherduck_flight_grant.runner", "grantee_type", "user"),
					resource.TestCheckResourceAttr("data.motherduck_dive_grantees.test", "rows.#", "2"),
					resource.TestCheckResourceAttr("data.motherduck_dive_grantees.test", "rows.0.grantee_type", "organization"),
					resource.TestCheckResourceAttr("data.motherduck_dive_grantees.test", "rows.0.grantee_name", "Contract Org"),
					resource.TestCheckResourceAttr("data.motherduck_dive_grantees.test", "rows.1.grantee_name", "finance"),
					resource.TestCheckResourceAttr("data.motherduck_flight_grantees.test", "rows.#", "1"),
					resource.TestCheckResourceAttr("data.motherduck_flight_grantees.test", "rows.0.grantee_name", "svc_runner"),
					resource.TestCheckResourceAttr("data.motherduck_flight_grantees.test", "rows.0.privilege", "run"),
				),
			},
			{
				// A change in letter case names the same principal, so it
				// must not revoke and grant again.
				Config: objectGrantsConfig("Finance", true, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_dive_grant.finance", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("motherduck_flight_grant.runner", plancheck.ResourceActionNoop),
				}},
				Check: func(*terraform.State) error {
					if n := client.count(`ROLE "`); n != 1 {
						return fmt.Errorf("role grant statements = %d, want only the first grant", n)
					}
					return nil
				},
			},
			{
				ResourceName:      "motherduck_dive_grant.everyone",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "motherduck_flight_grant.runner",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// A grant revoked outside Terraform is granted again.
				PreConfig: func() { client.revoke(contractGrantFlightID + "|user|svc_runner") },
				Config:    objectGrantsConfig("Finance", true, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_flight_grant.runner", plancheck.ResourceActionCreate),
					plancheck.ExpectResourceAction("motherduck_dive_grant.finance", plancheck.ResourceActionNoop),
				}},
			},
			{
				// Removing the organization grant makes the Dive private to
				// its owner and the remaining grantees.
				Config: objectGrantsConfig("Finance", false, false),
				Check: func(*terraform.State) error {
					if client.granted(contractGrantDiveID+"|organization|") || !client.granted(contractGrantDiveID+"|role|finance") {
						return fmt.Errorf("want only the role grant left on the Dive")
					}
					return nil
				},
			},
			{
				// A deleted Dive takes its grants with it. The grant leaves
				// state, so the plan proposes to grant again and nothing fails.
				PreConfig:          func() { client.deleted[contractGrantDiveID] = true },
				Config:             objectGrantsConfig("Finance", false, false),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Destroy must not fail on the grant of the deleted Dive.
				PreConfig: func() { client.deleted[contractGrantDiveID] = false },
				Config:    objectGrantsConfig("Finance", false, false),
			},
		},
	})
	if got := client.count("REVOKE READ ON DIVE '" + contractGrantDiveID + "' FROM ORGANIZATION"); got != 1 {
		t.Fatalf("organization revokes = %d, want 1", got)
	}
	if len(client.grants) != 0 {
		t.Fatalf("grants left after destroy: %v", client.grants)
	}
}

func TestContractObjectGrantConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		config    string
		wantError string
	}{
		"organization with a name": {
			config:    fmt.Sprintf("resource \"motherduck_dive_grant\" \"test\" {\n  dive_id = %q\n  grantee_type = \"organization\"\n  grantee_name = \"acme\"\n}", contractGrantDiveID),
			wantError: "Omit `grantee_name` when `grantee_type` is `organization`",
		},
		"user without a name": {
			config:    fmt.Sprintf("resource \"motherduck_flight_grant\" \"test\" {\n  flight_id = %q\n}", contractGrantFlightID),
			wantError: "Set `grantee_name` to the user",
		},
		"unknown grantee type": {
			config:    fmt.Sprintf("resource \"motherduck_flight_grant\" \"test\" {\n  flight_id = %q\n  grantee_type = \"group\"\n  grantee_name = \"x\"\n}", contractGrantFlightID),
			wantError: "MotherDuck grantee type",
		},
		"object ID is not a UUID": {
			config:    "resource \"motherduck_dive_grant\" \"test\" {\n  dive_id = \"revenue\"\n  grantee_name = \"alice\"\n}",
			wantError: "UUID",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(newObjectGrantsSQL()),
				Steps: []resource.TestStep{{
					Config:      contractProviderConfig("http://127.0.0.1") + tc.config,
					ExpectError: regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(tc.wantError), " ", `\s+`)),
				}},
			})
		})
	}
}
