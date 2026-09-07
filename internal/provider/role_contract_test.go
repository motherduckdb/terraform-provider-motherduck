//go:build contract

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestContractRoleAndGrantLifecycle(t *testing.T) {
	sqlClient := newRoleContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_role" "new" {
  name = "contract_readers"
}

resource "motherduck_role_grant" "admin_parent" {
  role_name    = "admin"
  grantee_name = motherduck_role.new.name
  grantee_type = "role"
}

resource "motherduck_role_grant" "builder_parent" {
  role_name    = "builder"
  grantee_name = motherduck_role.new.name
  grantee_type = "role"
}

resource "motherduck_role_grant" "explorer_parent" {
  role_name    = "explorer"
  grantee_name = motherduck_role.new.name
  grantee_type = "role"
}

resource "motherduck_role_grant" "existing_to_user" {
  role_name    = "existing_custom"
  grantee_name = "alice@example.com"
}

resource "motherduck_role_grant" "new_to_service_account" {
  role_name    = motherduck_role.new.name
  grantee_name = "svc_reader"
}

resource "motherduck_role_grant" "existing_to_new_role" {
  role_name    = "existing_custom"
  grantee_name = motherduck_role.new.name
  grantee_type = "role"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		CheckDestroy: func(*terraform.State) error {
			if got := sqlClient.roleNames(); fmt.Sprint(got) != "[admin builder existing_custom explorer]" {
				return fmt.Errorf("remaining roles = %#v, want system roles and pre-existing custom role", got)
			}
			if grants := sqlClient.grants(); len(grants) != 0 {
				return fmt.Errorf("remaining grants = %#v, want none", grants)
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
					resource.TestCheckResourceAttr("motherduck_role.new", "name", "contract_readers"),
					resource.TestCheckResourceAttr("motherduck_role_grant.admin_parent", "role_name", "admin"),
					resource.TestCheckResourceAttr("motherduck_role_grant.builder_parent", "role_name", "builder"),
					resource.TestCheckResourceAttr("motherduck_role_grant.explorer_parent", "role_name", "explorer"),
					resource.TestCheckResourceAttr("motherduck_role_grant.existing_to_user", "grantee_name", "alice@example.com"),
					resource.TestCheckResourceAttr("motherduck_role_grant.new_to_service_account", "grantee_name", "svc_reader"),
					resource.TestCheckResourceAttr("motherduck_role_grant.existing_to_new_role", "grantee_type", "role"),
				),
			},
			{
				ResourceName:      "motherduck_role.new",
				ImportState:       true,
				ImportStateId:     "contract_readers",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "motherduck_role_grant.admin_parent",
				ImportState:       true,
				ImportStateId:     "admin/role/contract_readers",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "motherduck_role_grant.existing_to_user",
				ImportState:       true,
				ImportStateId:     "existing_custom/user/alice@example.com",
				ImportStateVerify: true,
			},
		},
	})

	for _, want := range []string{
		`exec GRANT ROLE "admin" TO ROLE "contract_readers"`,
		`exec GRANT ROLE "builder" TO ROLE "contract_readers"`,
		`exec GRANT ROLE "explorer" TO ROLE "contract_readers"`,
		`exec GRANT ROLE "existing_custom" TO USER "alice@example.com"`,
		`exec GRANT ROLE "contract_readers" TO USER "svc_reader"`,
		`exec GRANT ROLE "existing_custom" TO ROLE "contract_readers"`,
	} {
		if got := sqlClient.countCalls(want); got != 1 {
			t.Errorf("%s calls = %d, want 1", want, got)
		}
	}
}

func TestContractRoleGrantRecreatesInheritedOnlyMembership(t *testing.T) {
	sqlClient := newRoleContractSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_role_grant" "builder" {
  role_name    = "builder"
  grantee_name = "alice@example.com"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient),
		Steps: []resource.TestStep{
			{Config: config},
			{
				Config:    config,
				PreConfig: func() { sqlClient.setDirect("user/alice@example.com", "builder", false) },
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_role_grant.builder", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

type roleContractSQL struct {
	mu          sync.Mutex
	calls       []contractCall
	roles       map[string]bool
	memberships map[string]map[string]bool
}

func newRoleContractSQL() *roleContractSQL {
	return &roleContractSQL{
		roles:       map[string]bool{"admin": true, "builder": true, "explorer": true, "existing_custom": true},
		memberships: map[string]map[string]bool{},
	}
}

func (c *roleContractSQL) Available() bool { return true }
func (c *roleContractSQL) Close() error    { return nil }

func (c *roleContractSQL) AttachDatabase(_ context.Context, database string) error {
	return fmt.Errorf("unexpected database attach %q", database)
}

func (c *roleContractSQL) Exec(_ context.Context, query string, args ...any) error {
	c.record("exec", query, args...)
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments for %q: %#v", query, args)
	}
	fields := strings.Fields(query)
	if len(fields) == 3 && fields[0] == "CREATE" && fields[1] == "ROLE" {
		name := unquoteRole(fields[2])
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.roles[name] {
			return fmt.Errorf("role %q already exists", name)
		}
		c.roles[name] = true
		return nil
	}
	if len(fields) == 5 && fields[0] == "DROP" && fields[1] == "ROLE" && fields[2] == "IF" && fields[3] == "EXISTS" {
		name := unquoteRole(fields[4])
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.roles, name)
		return nil
	}
	if len(fields) == 6 && (fields[0] == "GRANT" || fields[0] == "REVOKE") && fields[1] == "ROLE" {
		action, roleName, granteeType, granteeName := fields[0], unquoteRole(fields[2]), strings.ToLower(fields[4]), unquoteRole(fields[5])
		if fields[3] != "TO" && fields[3] != "FROM" {
			return fmt.Errorf("unexpected grant preposition in %q", query)
		}
		key := granteeType + "/" + granteeName
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.roles[roleName] {
			return fmt.Errorf("missing role %q", roleName)
		}
		if c.memberships[key] == nil {
			c.memberships[key] = map[string]bool{}
		}
		if action == "GRANT" {
			c.memberships[key][roleName] = true
		} else {
			delete(c.memberships[key], roleName)
			if len(c.memberships[key]) == 0 {
				delete(c.memberships, key)
			}
		}
		return nil
	}
	return fmt.Errorf("unexpected SQL exec %q", query)
}

func (c *roleContractSQL) Exists(context.Context, string, ...any) (bool, error) {
	return false, errors.New("unexpected SQL exists query")
}

func (c *roleContractSQL) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return roleContractRow{err: errors.New("unexpected SQL row query")}
}

func (c *roleContractSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.record("query-json", query, args...)
	if len(args) != 0 {
		return "", fmt.Errorf("unexpected arguments for %q: %#v", query, args)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if query == "SHOW ALL ROLES" {
		roles := make([]string, 0, len(c.roles))
		for name := range c.roles {
			roles = append(roles, name)
		}
		sort.Strings(roles)
		rows := make([]map[string]any, 0, len(roles))
		for _, name := range roles {
			roleType := "CUSTOM"
			if name == "admin" || name == "builder" || name == "explorer" {
				roleType = "SYSTEM"
			}
			rows = append(rows, map[string]any{"role_name": name, "role_type": roleType, "included_roles": []string{}, "created_at": "2026-09-07T00:00:00Z"})
		}
		encoded, err := json.Marshal(rows)
		return string(encoded), err
	}
	prefix := "SHOW ROLES TO "
	if !strings.HasPrefix(query, prefix) {
		return "", fmt.Errorf("unexpected SQL JSON query %q", query)
	}
	parts := strings.Fields(strings.TrimPrefix(query, prefix))
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected role membership query %q", query)
	}
	key := strings.ToLower(parts[0]) + "/" + unquoteRole(parts[1])
	rows := make([]map[string]any, 0, len(c.memberships[key]))
	for roleName, direct := range c.memberships[key] {
		rows = append(rows, map[string]any{"role_name": roleName, "is_direct": direct, "granted_at": "2026-09-07T00:00:00Z"})
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

func (c *roleContractSQL) ScalarString(context.Context, string, ...any) (string, error) {
	return "", errors.New("unexpected SQL scalar query")
}

func (c *roleContractSQL) WithDatabaseUse(context.Context, string, func(func(string, ...any) error) error) error {
	return errors.New("unexpected database use")
}

func (c *roleContractSQL) record(method, query string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, contractCall{method: method, query: query, args: append([]any(nil), args...)})
}

func (c *roleContractSQL) countCalls(want string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if call.method+" "+call.query == want {
			count++
		}
	}
	return count
}

func (c *roleContractSQL) roleNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	roles := make([]string, 0, len(c.roles))
	for name := range c.roles {
		roles = append(roles, name)
	}
	sort.Strings(roles)
	return roles
}

func (c *roleContractSQL) grants() map[string]map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]map[string]bool, len(c.memberships))
	for key, roles := range c.memberships {
		result[key] = make(map[string]bool, len(roles))
		for role := range roles {
			result[key][role] = true
		}
	}
	return result
}

func (c *roleContractSQL) setDirect(key, roleName string, direct bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.memberships[key][roleName] = direct
}

func unquoteRole(value string) string {
	return strings.Trim(strings.ReplaceAll(value, `""`, `"`), `"`)
}

type roleContractRow struct{ err error }

func (r roleContractRow) Scan(...any) error { return r.err }
