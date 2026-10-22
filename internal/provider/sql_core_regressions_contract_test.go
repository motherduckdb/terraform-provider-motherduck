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

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// duckDBListVarchar renders a value the way duckdb_secrets() reports a
// one-element scope list, using a local DuckDB engine.
func duckDBListVarchar(t *testing.T, value string) string {
	t.Helper()
	db, err := stdsql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var out string
	if err := db.QueryRow("SELECT [?::VARCHAR]::VARCHAR", value).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// scopeSecretSQL stores one secret and reports its scope with DuckDB's list
// cast, which leaves plain words unquoted and escapes quotes with a
// backslash.
type scopeSecretSQL struct {
	*contractSQL
	t         *testing.T
	secretMu  sync.Mutex
	exists    bool
	scope     string
	liveScope string
	writes    int
}

func (c *scopeSecretSQL) Exec(ctx context.Context, query string, args ...any) error {
	c.record("exec", query, args...)
	switch {
	case strings.HasPrefix(query, `CREATE SECRET "contract_secret" IN MOTHERDUCK`),
		strings.HasPrefix(query, `CREATE OR REPLACE SECRET "contract_secret" IN MOTHERDUCK`):
		c.secretMu.Lock()
		c.exists = true
		c.liveScope = duckDBListVarchar(c.t, c.scope)
		c.writes++
		c.secretMu.Unlock()
		return nil
	case query == `DROP SECRET IF EXISTS "contract_secret" FROM motherduck`:
		c.secretMu.Lock()
		c.exists = false
		c.secretMu.Unlock()
		return nil
	}
	return c.contractSQL.Exec(ctx, query, args...)
}

func (c *scopeSecretSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if strings.Contains(query, "duckdb_secrets()") {
		c.secretMu.Lock()
		defer c.secretMu.Unlock()
		if !c.exists {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: []any{"s3", "config", "motherduck", c.liveScope}}
	}
	return c.contractSQL.QueryRow(ctx, query, args...)
}

func (c *scopeSecretSQL) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	if query == "SELECT [?::VARCHAR]::VARCHAR" && len(args) == 1 {
		value, _ := args[0].(string)
		return duckDBListVarchar(c.t, value), nil
	}
	return c.contractSQL.ScalarString(ctx, query, args...)
}

// The planned scope must match DuckDB's rendering, so an unchanged secret
// plans no diff, and a changed live scope plans an update that reads the
// scope back instead of predicting a value that could differ.
func TestContractSecretScopeMatchesDuckDBRendering(t *testing.T) {
	for _, scope := range []string{"mybucket", "s3://bucket/path/with'quote/"} {
		t.Run(scope, func(t *testing.T) {
			client := &scopeSecretSQL{contractSQL: newContractSQL(), t: t, scope: scope}
			config := contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_secret" "test" {
  name = "contract_secret"
  type = "s3"
  params = {
    key_id = "k"
    scope  = %q
  }
}
`, scope)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(client),
				CheckDestroy: func(*terraform.State) error {
					client.secretMu.Lock()
					defer client.secretMu.Unlock()
					if client.writes != 2 {
						return fmt.Errorf("secret writes = %d, want 2 (create and drift repair)", client.writes)
					}
					return nil
				},
				Steps: []resource.TestStep{
					{
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						Check: resource.TestCheckResourceAttr("motherduck_secret.test", "scope", duckDBListVarchar(t, scope)),
					},
					{
						PreConfig: func() {
							client.secretMu.Lock()
							client.liveScope = "[other]"
							client.secretMu.Unlock()
						},
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_secret.test", plancheck.ResourceActionUpdate)},
							PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						Check: resource.TestCheckResourceAttr("motherduck_secret.test", "scope", duckDBListVarchar(t, scope)),
					},
				},
			})
		})
	}
}

// MotherDuck trims each include-pattern entry and drops blank ones, so such
// entries would read back differently and fail the apply. They are rejected
// during planning instead.
func TestContractShareIncludePatternRejectsUntrimmedEntries(t *testing.T) {
	for name, tc := range map[string]struct {
		patterns string
		want     string
	}{
		"trailing space": {patterns: `["main.* "]`, want: `must not have leading or trailing whitespace`},
		"leading tab":    {patterns: `["\tmain.*"]`, want: `must not have leading or trailing whitespace`},
		"blank entry":    {patterns: `["main.*", ""]`, want: `must not be empty or blank`},
		"only blank":     {patterns: `[" "]`, want: `must not be empty or blank`},
		"empty string":   {patterns: `[""]`, want: `must not be empty or blank`},
	} {
		t.Run(name, func(t *testing.T) {
			client := newContractSQL()
			config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_share" "test" {
  name            = "contract_share"
  source_database = "analytics"
  include_pattern = ` + tc.patterns + `
}
`
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(client),
				Steps: []resource.TestStep{{
					Config:      config,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
			if got := client.countCalls(`exec CREATE SHARE "contract_share" FROM "analytics"`); got != 0 {
				t.Fatalf("share creates = %d, want 0", got)
			}
		})
	}
}

// transientTypeSQL fails the type normalization that only the plan-time
// column comparison runs.
type transientTypeSQL struct {
	*contractSQL
	failMu sync.Mutex
	fail   bool
}

func (c *transientTypeSQL) setFail(fail bool) {
	c.failMu.Lock()
	defer c.failMu.Unlock()
	c.fail = fail
}

func (c *transientTypeSQL) ScalarString(ctx context.Context, query string, args ...any) (string, error) {
	c.failMu.Lock()
	fail := c.fail
	c.failMu.Unlock()
	if fail && query == "SELECT typeof(CAST(NULL AS INTEGER))" {
		return "", errors.New("IO Error: connection reset by peer")
	}
	return c.contractSQL.ScalarString(ctx, query, args...)
}

// A failed type comparison during planning must fail the plan. Replacing
// would drop the table and its data although the types may be equivalent.
func TestContractTableTypeCompareErrorFailsPlan(t *testing.T) {
	client := &transientTypeSQL{contractSQL: newContractSQL()}
	config := func(idType string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_table" "test" {
  database = "contract_database"
  schema   = "app"
  name     = "facts"
  columns = {
    id    = %q
    label = "VARCHAR"
  }
}
`, idType)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.countCalls(`exec CREATE TABLE "contract_database"."app"."facts" ("id" INTEGER, "label" VARCHAR)`); got != 1 {
				return fmt.Errorf("table creates = %d, want 1", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("INT")},
			{
				PreConfig:   func() { client.setFail(true) },
				Config:      config("INTEGER"),
				ExpectError: regexp.MustCompile(`Unable to compare MotherDuck table column types`),
			},
			{
				PreConfig: func() { client.setFail(false) },
				Config:    config("INTEGER"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
