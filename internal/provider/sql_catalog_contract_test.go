//go:build contract

package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

const contractReceivedShareURL = "md:_share/tenant/0a9a026ec5a55946a9de39851087ed81"

// catalogSQL answers the read-only catalog queries of the SQL data sources.
// Unknown queries fail so a changed query is caught by the contract.
type catalogSQL struct {
	*contractSQL
	functions map[string]bool
	scalars   map[string]string
	rows      map[string]string
	rowsErr   map[string]error
	share     []any
	shareErr  error
}

func newCatalogSQL() *catalogSQL {
	return &catalogSQL{
		contractSQL: newContractSQL(),
		functions: map[string]bool{
			"md_user_info": true, "md_pragma_version": true, "md_access_tokens": true, "md_describe_database_share": true,
		},
		scalars: map[string]string{
			"SELECT md_user()":    "svc_writer",
			"SELECT md_version()": "v1.5.6-2026-10-484",
		},
		rows: map[string]string{
			// An older extension omits region, which must read back as null.
			"SELECT * FROM md_user_info()":                                     `[{"user_id":"u-1","username":"svc_writer","org_id":"o-1","org_name":"Analytics","org_type":"business"}]`,
			"SELECT * FROM md_pragma_version()":                                `[{"md_extension_version":"v1.5.6-2026-10-484","md_extension_hash":"9f30707f97"}]`,
			"SELECT * FROM md_access_tokens()":                                 `[{"token_name":"writer","token_type":"read_write","created_ts":"2026-09-01T00:00:00Z","expire_at":null},{"token_name":"ci","token_type":"read_scaling","created_ts":"2026-09-02T00:00:00Z","expire_at":"2027-01-01T00:00:00Z"}]`,
			"SELECT * FROM MD_INFORMATION_SCHEMA.SHARED_WITH_ME ORDER BY name": `[{"name":"tenant","url":"` + contractReceivedShareURL + `","owner":"owner","visibility":"DISCOVERABLE","created_ts":"2026-09-01T00:00:00Z","updated_ts":"2026-09-03T00:00:00Z","update_mode":"AUTOMATIC","access":"ORGANIZATION"}]`,
		},
		rowsErr: map[string]error{},
		share:   []any{"tenant", contractReceivedShareURL, "tenant_db", "00000000-0000-0000-0000-0000000000a1", "ORGANIZATION", "DISCOVERABLE", "AUTOMATIC", "2026-09-01 00:00:00+00"},
	}
}

func (c *catalogSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 {
		return false, fmt.Errorf("unexpected function probe: %q %#v", query, args)
	}
	name, _ := args[0].(string)
	return c.functions[strings.ToLower(name)], nil
}

func (c *catalogSQL) ScalarString(_ context.Context, query string, args ...any) (string, error) {
	value, ok := c.scalars[query]
	if !ok || len(args) != 0 {
		return "", fmt.Errorf("unexpected SQL scalar query %q", query)
	}
	return value, nil
}

func (c *catalogSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	if err, ok := c.rowsErr[query]; ok {
		return "", err
	}
	rows, ok := c.rows[query]
	if !ok || len(args) != 0 {
		return "", fmt.Errorf("unexpected SQL JSON query %q", query)
	}
	return rows, nil
}

func (c *catalogSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if !strings.Contains(query, "MD_DESCRIBE_DATABASE_SHARE(") {
		return c.contractSQL.QueryRow(ctx, query, args...)
	}
	// MotherDuck resolves a share URL case-insensitively.
	if want := "MD_DESCRIBE_DATABASE_SHARE('" + contractReceivedShareURL + "')"; !strings.HasSuffix(strings.ToLower(query), strings.ToLower(want)) {
		return contractRow{err: fmt.Errorf("unexpected share describe query %q", query)}
	}
	if c.shareErr != nil {
		return contractRow{err: c.shareErr}
	}
	return contractRow{values: append([]any(nil), c.share...)}
}

func (c *catalogSQL) Exec(context.Context, string, ...any) error {
	return errors.New("catalog data sources must not execute statements")
}

const catalogContractConfig = `
data "motherduck_current_user" "test" {}

data "motherduck_version" "test" {}

data "motherduck_current_user_tokens" "test" {}

data "motherduck_shared_with_me" "test" {}

data "motherduck_share" "received" {
  url = data.motherduck_shared_with_me.test.rows[0].url
}
`

func TestContractCatalogDataSourcesExposeLiveColumns(t *testing.T) {
	client := newCatalogSQL()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config:           contractProviderConfig("http://127.0.0.1") + catalogContractConfig,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "value", "svc_writer"),
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "user_id", "u-1"),
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "username", "svc_writer"),
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "org_id", "o-1"),
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "org_name", "Analytics"),
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "org_type", "business"),
				resource.TestCheckNoResourceAttr("data.motherduck_current_user.test", "region"),

				resource.TestCheckResourceAttr("data.motherduck_version.test", "value", "v1.5.6-2026-10-484"),
				resource.TestCheckResourceAttr("data.motherduck_version.test", "extension_hash", "9f30707f97"),

				resource.TestCheckResourceAttr("data.motherduck_current_user_tokens.test", "rows.#", "2"),
				resource.TestCheckResourceAttr("data.motherduck_current_user_tokens.test", "rows.0.token_name", "ci"),
				resource.TestCheckResourceAttr("data.motherduck_current_user_tokens.test", "rows.0.expire_at", "2027-01-01T00:00:00Z"),
				resource.TestCheckResourceAttr("data.motherduck_current_user_tokens.test", "rows.1.token_name", "writer"),
				resource.TestCheckNoResourceAttr("data.motherduck_current_user_tokens.test", "rows.1.expire_at"),

				resource.TestCheckResourceAttr("data.motherduck_shared_with_me.test", "rows.#", "1"),
				resource.TestCheckResourceAttr("data.motherduck_shared_with_me.test", "rows.0.access", "ORGANIZATION"),
				resource.TestCheckResourceAttr("data.motherduck_shared_with_me.test", "rows.0.visibility", "DISCOVERABLE"),
				resource.TestCheckResourceAttr("data.motherduck_shared_with_me.test", "rows.0.update_mode", "AUTOMATIC"),
				resource.TestCheckResourceAttr("data.motherduck_shared_with_me.test", "rows.0.updated_ts", "2026-09-03T00:00:00Z"),
				resource.TestCheckNoResourceAttr("data.motherduck_shared_with_me.test", "rows.0.source_database"),

				resource.TestCheckResourceAttr("data.motherduck_share.received", "name", "tenant"),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "url", contractReceivedShareURL),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "source_database", "tenant_db"),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "source_database_uuid", "00000000-0000-0000-0000-0000000000a1"),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "access", "organization"),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "visibility", "discoverable"),
				resource.TestCheckResourceAttr("data.motherduck_share.received", "update_mode", "automatic"),
			),
		}},
	})
}

func TestContractCatalogDataSourcesWithoutOptionalFunctions(t *testing.T) {
	client := newCatalogSQL()
	client.functions = map[string]bool{"md_access_tokens": true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_current_user" "test" {}

data "motherduck_version" "test" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "value", "svc_writer"),
				resource.TestCheckNoResourceAttr("data.motherduck_current_user.test", "org_id"),
				resource.TestCheckResourceAttr("data.motherduck_version.test", "value", "v1.5.6-2026-10-484"),
				resource.TestCheckNoResourceAttr("data.motherduck_version.test", "extension_hash"),
			),
		}},
	})
}

// TestContractShareKeepsConfiguredURL reads a share by a URL spelled
// differently from the canonical URL MotherDuck reports. State keeps the
// configured spelling.
func TestContractShareKeepsConfiguredURL(t *testing.T) {
	client := newCatalogSQL()
	configured := strings.Replace(contractReceivedShareURL, "tenant", "Tenant", 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_share" "test" { url = "` + configured + `" }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_share.test", "url", configured),
				resource.TestCheckResourceAttr("data.motherduck_share.test", "name", "tenant"),
			),
		}},
	})
}

// TestContractCurrentUserDetailFailureKeepsValue fails md_user_info with an
// error other than SaaS mode. The data source still reads `value` and leaves
// the detail attributes null with a warning.
func TestContractCurrentUserDetailFailureKeepsValue(t *testing.T) {
	client := newCatalogSQL()
	client.rowsErr["SELECT * FROM md_user_info()"] = errors.New("Invalid Error: MDExternalException: user service unavailable")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_current_user" "test" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_current_user.test", "value", "svc_writer"),
				resource.TestCheckNoResourceAttr("data.motherduck_current_user.test", "user_id"),
				resource.TestCheckNoResourceAttr("data.motherduck_current_user.test", "org_id"),
			),
		}},
	})
}

func TestContractCatalogDataSourceErrors(t *testing.T) {
	storageQuery := "SELECT * FROM MD_INFORMATION_SCHEMA.STORAGE_INFO ORDER BY database_name, database_id, computed_ts"
	for name, tc := range map[string]struct {
		configure func(*catalogSQL)
		config    string
		wantError string
	}{
		"storage info needs an admin": {
			configure: func(c *catalogSQL) {
				c.rowsErr[storageQuery] = errors.New("Invalid Error: MDExternalException: User not authorized. This is an admin only function.")
			},
			config:    `data "motherduck_storage_info" "test" {}`,
			wantError: "organization admin permission required",
		},
		"share needs exactly one lookup": {
			config:    `data "motherduck_share" "test" {}`,
			wantError: "Set exactly one of name or url",
		},
		"missing received share": {
			configure: func(c *catalogSQL) {
				c.shareErr = errors.New("Catalog Error: Share: '" + contractReceivedShareURL + "' not found.")
			},
			config:    `data "motherduck_share" "test" { url = "` + contractReceivedShareURL + `" }`,
			wantError: "No share at the configured URL is visible",
		},
		"share errors redact the url": {
			configure: func(c *catalogSQL) {
				c.shareErr = errors.New("IO Error: could not resolve " + contractReceivedShareURL)
			},
			config:    `data "motherduck_share" "test" { url = "` + contractReceivedShareURL + `" }`,
			wantError: `could not resolve <redacted share URL>`,
		},
		"share errors redact a respelled url": {
			configure: func(c *catalogSQL) {
				c.shareErr = errors.New("IO Error: could not resolve " + strings.ToUpper(contractReceivedShareURL) + " or md%3A_share%2Ftenant%2F0a9a026ec5a55946a9de39851087ed81")
			},
			config:    `data "motherduck_share" "test" { url = "` + contractReceivedShareURL + `" }`,
			wantError: `could not resolve <redacted share URL> or <redacted share URL>`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newCatalogSQL()
			if tc.configure != nil {
				tc.configure(client)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(client),
				Steps: []resource.TestStep{{
					Config:      contractProviderConfig("http://127.0.0.1") + tc.config,
					ExpectError: regexp.MustCompile(tc.wantError),
				}},
			})
		})
	}
}
