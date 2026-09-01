//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

const contractProviderConfig = `
provider "motherduck" {
  token       = "contract-sql-token"
  admin_token = "contract-admin-token"
}
`

func TestContractDatabaseLifecycle(t *testing.T) {
	sqlClient := newContractSQL()
	restClient := newContractREST()
	config := contractProviderConfig + `
resource "motherduck_database" "test" {
  name = "contract_database"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient, restClient),
		CheckDestroy: func(*terraform.State) error {
			if sqlClient.databaseExists {
				return errors.New("contract database still exists after destroy")
			}
			if got := sqlClient.countCalls(`exec CREATE DATABASE "contract_database"`); got != 2 {
				return fmt.Errorf("database creates = %d, want 2 (initial create and drift recreation)", got)
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
					resource.TestCheckResourceAttr("motherduck_database.test", "id", "contract_database"),
					resource.TestCheckResourceAttr("motherduck_database.test", "database_type", "motherduck"),
				),
			},
			{
				ResourceName:      "motherduck_database.test",
				ImportState:       true,
				ImportStateId:     "contract_database",
				ImportStateVerify: true,
			},
			{
				Config: config,
				PreConfig: func() {
					sqlClient.mu.Lock()
					sqlClient.databaseExists = false
					sqlClient.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "id", "contract_database"),
			},
		},
	})
}

func TestContractTableCanonicalTypesAndDriftReplacement(t *testing.T) {
	sqlClient := newContractSQL()
	restClient := newContractREST()
	config := contractProviderConfig + `
resource "motherduck_table" "test" {
  database = "contract_database"
  schema   = "app"
  name     = "facts"
  columns = {
    id    = "INT"
    label = "VARCHAR"
  }
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient, restClient),
		CheckDestroy: func(*terraform.State) error {
			if sqlClient.tableExists {
				return errors.New("contract table still exists after destroy")
			}
			if got := sqlClient.countCalls(`exec CREATE TABLE "contract_database"."app"."facts" ("id" INTEGER, "label" VARCHAR)`); got != 2 {
				return fmt.Errorf("table creates = %d, want 2 (initial create and drift replacement)", got)
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
					resource.TestCheckResourceAttr("motherduck_table.test", "columns.id", "INT"),
					resource.TestCheckResourceAttr("motherduck_table.test", "columns.label", "VARCHAR"),
				),
			},
			{
				Config: config,
				PreConfig: func() {
					sqlClient.mu.Lock()
					sqlClient.tableColumns["id"] = "BIGINT"
					sqlClient.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.TestCheckResourceAttr("motherduck_table.test", "columns.id", "INT"),
			},
		},
	})
}

func TestContractAccessTokenPreservesSecretAndRecreatesAfterDeletion(t *testing.T) {
	sqlClient := newContractSQL()
	restClient := newContractREST()
	config := contractProviderConfig + `
resource "motherduck_access_token" "test" {
  username   = "contract_user"
  name       = "contract token"
  token_type = "read_write"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient, restClient),
		CheckDestroy: func(*terraform.State) error {
			restClient.mu.Lock()
			defer restClient.mu.Unlock()
			if restClient.token != nil {
				return errors.New("contract access token still exists after destroy")
			}
			if restClient.createCount != 2 {
				return fmt.Errorf("token creates = %d, want 2 (initial create and drift recreation)", restClient.createCount)
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
					resource.TestCheckResourceAttr("motherduck_access_token.test", "token", "md_contract_secret_1"),
					resource.TestCheckResourceAttr("motherduck_access_token.test", "id", "token-1"),
				),
			},
			{
				RefreshState: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_access_token.test", "token", "md_contract_secret_1"),
					resource.TestCheckResourceAttr("motherduck_access_token.test", "id", "token-1"),
				),
			},
			{
				ResourceName:            "motherduck_access_token.test",
				ImportState:             true,
				ImportStateId:           "contract_user/token-1",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token"},
			},
			{
				Config: config,
				PreConfig: func() {
					restClient.mu.Lock()
					restClient.token = nil
					restClient.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_access_token.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("motherduck_access_token.test", "token", "md_contract_secret_2"),
			},
		},
	})
}

func TestContractOwnedShareTypedNullState(t *testing.T) {
	sqlClient := newContractSQL()
	sqlClient.ownedShare = []any{
		"md:_share/contract",
		"analytics",
		"RESTRICTED",
		nil,
		"AUTOMATIC",
		nil,
		"2026-09-01T00:00:00Z",
	}
	restClient := newContractREST()
	config := contractProviderConfig + `
data "motherduck_owned_share" "test" {
  name = "contract_share"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(sqlClient, restClient),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_owned_share.test", "source_database", "analytics"),
				resource.TestCheckResourceAttr("data.motherduck_owned_share.test", "access", "restricted"),
				resource.TestCheckResourceAttr("data.motherduck_owned_share.test", "update_mode", "automatic"),
				resource.TestCheckResourceAttr("data.motherduck_owned_share.test", "url", "md:_share/contract"),
				resource.TestCheckNoResourceAttr("data.motherduck_owned_share.test", "visibility"),
				resource.TestCheckNoResourceAttr("data.motherduck_owned_share.test", "include_pattern"),
			),
		}},
	})

	failingSQL := newContractSQL()
	failingSQL.ownedShareErr = errors.New("owned share backend unavailable")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(failingSQL, newContractREST()),
		Steps: []resource.TestStep{{
			Config:      config,
			ExpectError: regexp.MustCompile("owned share backend unavailable"),
		}},
	})
}

func contractProviderFactories(sqlClient providerctx.SQLClient, restClient providerctx.RESTClient) map[string]func() (tfprotov6.ProviderServer, error) {
	newSQL := func(context.Context, mdsql.Config) (providerctx.SQLClient, error) {
		return sqlClient, nil
	}
	newREST := func(string, string, string, time.Duration) (providerctx.RESTClient, error) {
		return restClient, nil
	}
	return map[string]func() (tfprotov6.ProviderServer, error){
		"motherduck": providerserver.NewProtocol6WithError(newWithClients("contract", newSQL, newREST)()),
	}
}

type contractCall struct {
	method string
	query  string
	args   []any
}

type contractSQL struct {
	mu             sync.Mutex
	calls          []contractCall
	databaseExists bool
	tableExists    bool
	tableColumns   map[string]string
	ownedShare     []any
	ownedShareErr  error
}

func newContractSQL() *contractSQL {
	return &contractSQL{tableColumns: map[string]string{}}
}

func (c *contractSQL) Available() bool { return true }
func (c *contractSQL) Close() error    { return nil }

func (c *contractSQL) AttachDatabase(_ context.Context, database string) error {
	c.record("attach", database)
	if database != "contract_database" {
		return fmt.Errorf("unexpected database attach %q", database)
	}
	return nil
}

func (c *contractSQL) Exec(_ context.Context, query string, args ...any) error {
	c.record("exec", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch query {
	case `CREATE DATABASE "contract_database"`:
		c.databaseExists = true
	case `DROP DATABASE IF EXISTS "contract_database"`:
		c.databaseExists = false
	case `CREATE TABLE "contract_database"."app"."facts" ("id" INTEGER, "label" VARCHAR)`:
		c.tableExists = true
		c.tableColumns = map[string]string{"id": "INTEGER", "label": "VARCHAR"}
	case `DROP TABLE IF EXISTS "contract_database"."app"."facts"`:
		c.tableExists = false
		c.tableColumns = map[string]string{}
	case "USE memory":
	default:
		return fmt.Errorf("unexpected SQL exec %q", query)
	}
	return nil
}

func (c *contractSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.record("exists", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.Contains(query, "information_schema.tables") {
		return c.tableExists, nil
	}
	return false, fmt.Errorf("unexpected SQL exists query %q", query)
}

func (c *contractSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.record("query-row", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES"):
		if !c.databaseExists {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: []any{
			"00000000-0000-0000-0000-000000000001",
			"2026-09-01T00:00:00Z",
			false,
			nil,
			"MOTHERDUCK",
		}}
	case strings.Contains(query, "MD_INFORMATION_SCHEMA.OWNED_SHARES"):
		if c.ownedShareErr != nil {
			return contractRow{err: c.ownedShareErr}
		}
		if c.ownedShare == nil {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: append([]any(nil), c.ownedShare...)}
	default:
		return contractRow{err: fmt.Errorf("unexpected SQL row query %q", query)}
	}
}

func (c *contractSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.record("query-json", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !strings.Contains(query, "information_schema.columns") {
		return "", fmt.Errorf("unexpected SQL JSON query %q", query)
	}
	names := make([]string, 0, len(c.tableColumns))
	for name := range c.tableColumns {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]map[string]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, map[string]string{"column_name": name, "data_type": c.tableColumns[name]})
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

func (c *contractSQL) ScalarString(_ context.Context, query string, args ...any) (string, error) {
	c.record("scalar", query, args...)
	switch query {
	case "SELECT current_database()":
		return "memory", nil
	case "SELECT typeof(CAST(NULL AS INT))", "SELECT typeof(CAST(NULL AS INTEGER))":
		return "INTEGER", nil
	case "SELECT typeof(CAST(NULL AS BIGINT))":
		return "BIGINT", nil
	case "SELECT typeof(CAST(NULL AS VARCHAR))":
		return "VARCHAR", nil
	default:
		return "", fmt.Errorf("unexpected SQL scalar query %q", query)
	}
}

func (c *contractSQL) WithDatabaseUse(_ context.Context, database string, fn func(func(string, ...any) error) error) error {
	c.record("with-database", database)
	return fn(func(query string, args ...any) error {
		return c.Exec(context.Background(), query, args...)
	})
}

func (c *contractSQL) record(method, query string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, contractCall{method: method, query: query, args: append([]any(nil), args...)})
}

func (c *contractSQL) countCalls(call string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, got := range c.calls {
		if got.method+" "+got.query == call {
			count++
		}
	}
	return count
}

type contractRow struct {
	values []any
	err    error
}

func (r contractRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *stdsql.NullString:
			if value == nil {
				*target = stdsql.NullString{}
			} else {
				*target = stdsql.NullString{String: value.(string), Valid: true}
			}
		case *stdsql.NullBool:
			if value == nil {
				*target = stdsql.NullBool{}
			} else {
				*target = stdsql.NullBool{Bool: value.(bool), Valid: true}
			}
		default:
			return fmt.Errorf("unsupported scan destination %T at index %d", dest[i], i)
		}
	}
	return nil
}

type contractREST struct {
	mu          sync.Mutex
	token       *mdrest.Token
	createCount int
}

func newContractREST() *contractREST    { return &contractREST{} }
func (c *contractREST) Available() bool { return true }

func (c *contractREST) CreateToken(_ context.Context, username string, req mdrest.CreateTokenRequest) (*mdrest.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if username != "contract_user" || req.Name != "contract token" || req.TokenType != "read_write" {
		return nil, fmt.Errorf("unexpected token create: username=%q request=%#v", username, req)
	}
	c.createCount++
	token := &mdrest.Token{
		ID:        fmt.Sprintf("token-%d", c.createCount),
		Name:      req.Name,
		Token:     fmt.Sprintf("md_contract_secret_%d", c.createCount),
		TokenType: req.TokenType,
		CreatedTS: "2026-09-01T00:00:00Z",
	}
	c.token = token
	copy := *token
	return &copy, nil
}

func (c *contractREST) ListTokens(_ context.Context, username string) ([]mdrest.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if username != "contract_user" {
		return nil, fmt.Errorf("unexpected token list username %q", username)
	}
	if c.token == nil {
		return nil, nil
	}
	copy := *c.token
	copy.Token = ""
	return []mdrest.Token{copy}, nil
}

func (c *contractREST) DeleteToken(_ context.Context, username, tokenID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if username != "contract_user" {
		return fmt.Errorf("unexpected token delete username %q", username)
	}
	if c.token != nil && c.token.ID == tokenID {
		c.token = nil
	}
	return nil
}

func (*contractREST) ActiveAccounts(context.Context) (*mdrest.ActiveAccountsResponse, error) {
	return nil, errors.New("unexpected ActiveAccounts call")
}
func (*contractREST) CreateDiveEmbedSession(context.Context, string, mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error) {
	return nil, errors.New("unexpected CreateDiveEmbedSession call")
}
func (*contractREST) CreateServiceAccount(context.Context, string) (*mdrest.ServiceAccount, error) {
	return nil, errors.New("unexpected CreateServiceAccount call")
}
func (*contractREST) DeleteUser(context.Context, string) error {
	return errors.New("unexpected DeleteUser call")
}
func (*contractREST) GetDucklingConfig(context.Context, string) (*mdrest.DucklingConfig, error) {
	return nil, errors.New("unexpected GetDucklingConfig call")
}
func (*contractREST) SetDucklingConfig(context.Context, string, mdrest.DucklingConfig) (*mdrest.DucklingConfig, error) {
	return nil, errors.New("unexpected SetDucklingConfig call")
}
