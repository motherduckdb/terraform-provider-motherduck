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
)

const shareGrantsContractQuery = `SELECT * FROM MD_LIST_SHARE_GRANTEES('tenant''s share') ORDER BY grantee_type, grantee_name`

// MotherDuck reports one row per audience. A restricted share carries user and
// role grants, while an organization or unrestricted share reports a single
// whole-audience row instead.
const shareGrantsContractRows = `[
 {"share_owner":"owner","grantee_name":"ALL_USERS","grantee_type":"domain","privilege":"read","granted_at":"2026-09-01T00:00:00Z"},
 {"share_owner":"owner","grantee_name":"analytics_readers","grantee_type":"role","privilege":"read","granted_at":"2026-09-02T00:00:00Z"},
 {"share_owner":"owner","grantee_name":"svc_reader","grantee_type":"user","privilege":"read","granted_at":null}
]`

func TestContractShareGrantsAudit(t *testing.T) {
	config := contractProviderConfig("http://127.0.0.1") + `
data "motherduck_share_grants" "test" {
  share_name = "tenant's share"
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(&shareGrantsSQL{contractSQL: newContractSQL(), rows: shareGrantsContractRows, functionAvailable: true}),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.#", "3"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_name", "ALL_USERS"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.0.grantee_type", "domain"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.grantee_name", "analytics_readers"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.1.grantee_type", "role"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.2.grantee_name", "svc_reader"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.2.grantee_type", "user"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.2.privilege", "read"),
				resource.TestCheckResourceAttr("data.motherduck_share_grants.test", "rows.2.share_owner", "owner"),
				resource.TestCheckNoResourceAttr("data.motherduck_share_grants.test", "rows.2.granted_at"),
			),
		}},
	})

	// A session without the grantee catalog must say so instead of reporting an
	// empty audience for a share that has grants.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(&shareGrantsSQL{contractSQL: newContractSQL(), rows: shareGrantsContractRows}),
		Steps: []resource.TestStep{{
			Config:      config,
			ExpectError: regexp.MustCompile("md_list_share_grantees is not exposed"),
		}},
	})

	// A caller who neither owns the share nor administers the organization gets
	// the MotherDuck lookup error rather than an empty grant list.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(&shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true, rowsErr: errors.New(`Catalog Error: Share "tenant's share" not found`)}),
		Steps: []resource.TestStep{{
			Config:      config,
			ExpectError: regexp.MustCompile("not found"),
		}},
	})
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
