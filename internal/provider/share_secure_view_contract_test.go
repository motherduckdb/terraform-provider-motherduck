//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
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
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// secureViewSQL models the secure views on one owned share. It accepts only
// the statements the provider is expected to send, so an unexpected statement
// fails the test.
type secureViewSQL struct {
	*contractSQL
	mu         sync.Mutex
	shareGone  bool
	showErr    error
	views      map[string]secureViewRow
	statements []string
}

type secureViewRow struct {
	schema   string
	name     string
	sql      string
	replaces bool
}

var secureViewStatementPattern = regexp.MustCompile(`(?s)^ALTER SHARE "usage_share" (ADD SECURE VIEW |ADD OR REPLACE SECURE VIEW )(REPLACES )?"([^"]+)"\."([^"]+)" AS \(\n(.*)\n\)$`)

func newSecureViewSQL() *secureViewSQL {
	return &secureViewSQL{contractSQL: newContractSQL(), views: map[string]secureViewRow{}}
}

func secureViewKey(schema, name string) string {
	return strings.ToLower(schema) + "." + strings.ToLower(name)
}

func noShareError() error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: No share named "usage_share" is owned by current user.`}
}

func (c *secureViewSQL) Exec(_ context.Context, query string, args ...any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statements = append(c.statements, query)
	if len(args) != 0 {
		return fmt.Errorf("unexpected args %#v", args)
	}
	if c.shareGone {
		return noShareError()
	}
	if strings.HasPrefix(query, `ALTER SHARE "usage_share" DROP SECURE VIEW IF EXISTS `) {
		target := strings.TrimPrefix(query, `ALTER SHARE "usage_share" DROP SECURE VIEW IF EXISTS `)
		parts := strings.Split(strings.ReplaceAll(target, `"`, ""), ".")
		if len(parts) != 2 {
			return fmt.Errorf("unexpected drop target %q", target)
		}
		delete(c.views, secureViewKey(parts[0], parts[1]))
		return nil
	}
	match := secureViewStatementPattern.FindStringSubmatch(query)
	if match == nil {
		return fmt.Errorf("unexpected SQL exec %q", query)
	}
	key := secureViewKey(match[3], match[4])
	if _, exists := c.views[key]; exists && match[1] == "ADD SECURE VIEW " {
		return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: fmt.Sprintf("Catalog Error: A secure view named %s.%s already exists on this share", match[3], match[4])}
	}
	c.views[key] = secureViewRow{schema: match[3], name: match[4], sql: strings.TrimSpace(match[5]), replaces: match[2] != ""}
	return nil
}

func (c *secureViewSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.Contains(query, "MD_INFORMATION_SCHEMA.OWNED_SHARES") || len(args) != 1 || !strings.EqualFold(args[0].(string), "usage_share") {
		return contractRow{err: fmt.Errorf("unexpected SQL row query %q %#v", query, args)}
	}
	if c.shareGone {
		return contractRow{err: stdsql.ErrNoRows}
	}
	return contractRow{values: []any{"md:_share/usage", "analytics", "RESTRICTED", "HIDDEN", "AUTOMATIC", "[]", "2026-10-01T00:00:00Z", "00000000-0000-0000-0000-0000000000a1"}}
}

func (c *secureViewSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if query != `SHOW SECURE VIEWS ON SHARE "usage_share"` || len(args) != 0 {
		return "", fmt.Errorf("unexpected SQL JSON query %q %#v", query, args)
	}
	if c.shareGone {
		return "", noShareError()
	}
	if c.showErr != nil {
		err := c.showErr
		c.showErr = nil
		return "", err
	}
	keys := make([]string, 0, len(c.views))
	for key := range c.views {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		view := c.views[key]
		rows = append(rows, map[string]any{
			"schema_name":    view.schema,
			"view_name":      view.name,
			"sql":            view.sql,
			"replaces_table": view.replaces,
			"columns":        []string{"tenant_id"},
		})
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

func (c *secureViewSQL) countStatementsWithPrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, statement := range c.statements {
		if strings.HasPrefix(statement, prefix) {
			count++
		}
	}
	return count
}

func secureViewContractConfig(filter string) string {
	return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_share_secure_view" "usage" {
  share    = "usage_share"
  schema   = "app"
  name     = "daily_usage"
  replaces = true
  query    = <<-SQL
    SELECT tenant_id
    FROM app.daily_usage
    WHERE %s
  SQL
}

resource "motherduck_share_secure_view" "summary" {
  share = "usage_share"
  name  = "acme_summary"
  query = "SELECT tenant_id FROM app.daily_usage WHERE tenant_id = 'acme'"
}
`, filter)
}

func TestContractShareSecureViewLifecycle(t *testing.T) {
	client := newSecureViewSQL()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if len(client.views) != 0 {
				return fmt.Errorf("secure views left after destroy: %#v", client.views)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:           secureViewContractConfig("tenant_id = current_user"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "id", "usage_share.app.daily_usage"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "replaces", "true"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "columns.#", "1"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.usage", "columns.0", "tenant_id"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.summary", "id", "usage_share.main.acme_summary"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.summary", "schema", "main"),
					resource.TestCheckResourceAttr("motherduck_share_secure_view.summary", "replaces", "false"),
					func(*terraform.State) error {
						if got := client.countStatementsWithPrefix(`ALTER SHARE "usage_share" ADD SECURE VIEW REPLACES "app"."daily_usage"`); got != 1 {
							return fmt.Errorf("replacing create statements = %d, want 1", got)
						}
						if client.countCalls("attach analytics") == 0 {
							return fmt.Errorf("the share's source database was not attached before the write")
						}
						return nil
					},
				),
			},
			{
				// A changed policy updates in place with ADD OR REPLACE, so the
				// table is never exposed without a policy.
				Config: secureViewContractConfig("tenant_id = current_user AND false"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share_secure_view.usage", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					if got := client.countStatementsWithPrefix(`ALTER SHARE "usage_share" ADD OR REPLACE SECURE VIEW REPLACES "app"."daily_usage"`); got != 1 {
						return fmt.Errorf("in-place update statements = %d, want 1", got)
					}
					if got := client.countStatementsWithPrefix(`ALTER SHARE "usage_share" DROP SECURE VIEW`); got != 0 {
						return fmt.Errorf("an update dropped a secure view %d times", got)
					}
					return nil
				},
			},
			{
				ResourceName:      "motherduck_share_secure_view.usage",
				ImportState:       true,
				ImportStateId:     "usage_share.app.daily_usage",
				ImportStateVerify: true,
				// MotherDuck stores the body without the heredoc's surrounding
				// whitespace, so the imported value is the trimmed body.
				ImportStateVerifyIgnore: []string{"query"},
			},
			{
				// A secure view dropped outside Terraform is planned again.
				Config:             secureViewContractConfig("tenant_id = current_user AND false"),
				PreConfig:          func() { client.mu.Lock(); delete(client.views, "app.daily_usage"); client.mu.Unlock() },
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:           secureViewContractConfig("tenant_id = current_user AND false"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				// A body changed outside Terraform is detected as drift.
				Config: secureViewContractConfig("tenant_id = current_user AND false"),
				PreConfig: func() {
					client.mu.Lock()
					client.views["main.acme_summary"] = secureViewRow{schema: "main", name: "acme_summary", sql: "SELECT tenant_id FROM app.daily_usage"}
					client.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share_secure_view.summary", plancheck.ResourceActionUpdate)},
				},
			},
		},
	})
}

func TestContractShareSecureViewShareGone(t *testing.T) {
	client := newSecureViewSQL()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{Config: secureViewContractConfig("true")},
			{
				// A replaced or dropped share loses its secure views, so a
				// refresh removes them from state instead of failing.
				Config:             secureViewContractConfig("true"),
				PreConfig:          func() { client.mu.Lock(); client.shareGone = true; client.mu.Unlock() },
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      secureViewContractConfig("true"),
				ExpectError: regexp.MustCompile(`not owned by the identity of the\s+SQL token`),
			},
		},
	})
}

func TestContractShareSecureViewCreateConflict(t *testing.T) {
	client := newSecureViewSQL()
	client.views["main.acme_summary"] = secureViewRow{schema: "main", name: "acme_summary", sql: "SELECT 1"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config:      secureViewContractConfig("true"),
			ExpectError: regexp.MustCompile(`import it with the ID\s+"usage_share.main.acme_summary"`),
		}},
	})
}

func TestContractShareSecureViewRejectsSemicolons(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newSecureViewSQL()),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_share_secure_view" "bad" {
  share = "usage_share"
  name  = "bad"
  query = "SELECT 1); DROP SHARE usage_share; SELECT (1"
}
`,
			ExpectError: regexp.MustCompile(`must not contain\s+semicolons`),
		}},
	})
}

func TestContractShareSecureViewsDataSource(t *testing.T) {
	client := newSecureViewSQL()
	client.views["main.b_view"] = secureViewRow{schema: "main", name: "b_view", sql: "SELECT 2"}
	client.views["app.daily_usage"] = secureViewRow{schema: "app", name: "daily_usage", sql: "SELECT tenant_id FROM app.daily_usage", replaces: true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_share_secure_views" "audit" {
  share_name = "usage_share"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.#", "2"),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.0.schema_name", "app"),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.0.view_name", "daily_usage"),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.0.replaces_table", "true"),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.0.columns", `["tenant_id"]`),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.1.view_name", "b_view"),
				resource.TestCheckResourceAttr("data.motherduck_share_secure_views.audit", "rows.1.sql", "SELECT 2"),
			),
		}},
	})
}

func TestContractShareSecureViewKeepsStateWhenReadBackFails(t *testing.T) {
	client := newSecureViewSQL()
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_share_secure_view" "summary" {
  share = "usage_share"
  name  = "acme_summary"
  query = "SELECT 1"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{
				Config: config,
				PreConfig: func() {
					client.mu.Lock()
					client.showErr = fmt.Errorf("IO Error: connection reset")
					client.mu.Unlock()
				},
				ExpectError: regexp.MustCompile(`connection reset`),
			},
			{
				// The secure view was written before the read failed. State
				// tracks it, so the next apply replaces the tainted resource
				// instead of failing with "already exists".
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}
