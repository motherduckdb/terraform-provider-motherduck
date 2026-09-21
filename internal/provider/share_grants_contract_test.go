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
)

const shareGrantsContractQuery = `SELECT * FROM MD_LIST_SHARE_GRANTEES('tenant''s share') ORDER BY grantee_type, grantee_name`

const shareGrantsContractRows = `[
 {"share_owner":"owner","grantee_name":"analytics_readers","grantee_type":"role","privilege":"read","granted_at":"2026-09-02T00:00:00Z"},
 {"share_owner":"owner","grantee_name":"svc_reader","grantee_type":"user","privilege":"read","granted_at":null}
]`

func TestContractShareGrantsAudit(t *testing.T) {
	config := contractProviderConfig("http://127.0.0.1") + `
data "motherduck_share_grants" "test" {
  share_name = "tenant's share"
}
`
	client := &shareGrantsSQL{contractSQL: newContractSQL(), rows: shareGrantsContractRows, functionAvailable: true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.#", "2"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "share_name", "tenant's share"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_name", "analytics_readers"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_type", "role"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.grantee_name", "svc_reader"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.grantee_type", "user"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.privilege", "read"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.share_owner", "owner"),
					resource.TestCheckNoResourceAttr("data.motherduck_share_grants.test", "rows.1.granted_at"),
				),
			},
			{
				Config: config,
				PreConfig: func() {
					client.rows = `[{"share_owner":"owner","grantee_name":"ENTIRE_ORGANIZATION","grantee_type":"organization","privilege":"read","granted_at":null}]`
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.#", "1"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_name", "ENTIRE_ORGANIZATION"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_type", "organization"),
				),
			},
			{
				Config: config,
				PreConfig: func() {
					client.rows = `[{"share_owner":"owner","grantee_name":"ALL_USERS","grantee_type":"domain","privilege":"read","granted_at":null}]`
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.#", "1"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_name", "ALL_USERS"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_type", "domain"),
				),
			},
			{
				Config:    config,
				PreConfig: func() { client.rows = "[]" },
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.#", "0"),
					resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows_json", "[]"),
				),
			},
		},
	})
}

func TestContractShareGrantsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		shareName string
		client    *shareGrantsSQL
		wantError string
	}{
		"missing function":   {"tenant's share", &shareGrantsSQL{contractSQL: newContractSQL()}, "md_list_share_grantees is not exposed"},
		"inaccessible share": {"tenant's share", &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true, rowsErr: errors.New(`Catalog Error: Share "tenant's share" not found`)}, "not found"},
		"blank name":         {"   ", &shareGrantsSQL{contractSQL: newContractSQL()}, "Value must be non-blank"},
		"empty name":         {"", &shareGrantsSQL{contractSQL: newContractSQL()}, "Value must be non-blank"},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(tc.client),
				Steps: []resource.TestStep{{
					Config: contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
data "motherduck_share_grants" "test" { share_name = %q }
`, tc.shareName),
					ExpectError: regexp.MustCompile(tc.wantError),
				}},
			})
		})
	}
}

type shareGrantsSQL struct {
	*contractSQL
	rows              string
	rowsErr           error
	functionAvailable bool
}

func (c *shareGrantsSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	if !strings.Contains(query, "duckdb_functions()") || len(args) != 1 || args[0] != "md_list_share_grantees" {
		return false, fmt.Errorf("unexpected function probe: %q %#v", query, args)
	}
	return c.functionAvailable, nil
}

func (c *shareGrantsSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	if query != shareGrantsContractQuery || len(args) != 0 {
		return "", fmt.Errorf("unexpected share grant query: %q %#v", query, args)
	}
	if c.rowsErr != nil {
		return "", c.rowsErr
	}
	return c.rows, nil
}

func (c *shareGrantsSQL) Exec(context.Context, string, ...any) error {
	return errors.New("share grant audit must not execute mutations")
}
