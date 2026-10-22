//go:build contract

package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func (f *userContractREST) addServiceAccount(username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[strings.ToLower(username)] = &mdrest.User{ID: "svc-" + username, Username: username, IsServiceAccount: true, Roles: []string{}}
}

func (f *userContractREST) addToken(username string, token mdrest.Token) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.ToLower(username)
	f.tokens[key] = append(f.tokens[key], token)
}

func accessTokenContractConfig(baseURL, tokenType string) string {
	tokenTypeLine := ""
	if tokenType != "" {
		tokenTypeLine = fmt.Sprintf("token_type = %q", tokenType)
	}
	return contractProviderConfig(baseURL) + fmt.Sprintf(`
resource "motherduck_access_token" "test" {
  username = "contract_svc"
  name     = "loader"
  %s
}
`, tokenTypeLine)
}

// TestContractAccessTokenTypeKeepsReportedValue removes token_type from the
// configuration of a read_scaling token. A static read_write default planned
// an in-place update that Update could not apply, so apply failed with an
// inconsistent result. The reported type is kept instead, and only a
// configured change replaces the token.
func TestContractAccessTokenTypeKeepsReportedValue(t *testing.T) {
	fake := newUserContractREST(t)
	fake.addServiceAccount("contract_svc")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{
				Config: accessTokenContractConfig(fake.server.URL, "read_scaling"),
				Check:  resource.TestCheckResourceAttr("motherduck_access_token.test", "token_type", "read_scaling"),
			},
			{
				Config: accessTokenContractConfig(fake.server.URL, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_access_token.test", "token_type", "read_scaling"),
			},
			{
				Config: accessTokenContractConfig(fake.server.URL, "read_write"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_access_token.test", plancheck.ResourceActionReplace)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_access_token.test", "token_type", "read_write"),
			},
		},
	})
}

// TestContractAccessTokenImport imports a read_scaling token whose
// configuration omits token_type, which planned an in-place change to the
// read_write default. It also imports by an upper-case token ID. UUIDs are
// case-insensitive, and the import validator accepts upper case, but Read
// compared IDs exactly and dropped the token from state.
func TestContractAccessTokenImport(t *testing.T) {
	const tokenID = "abcdef00-0000-4000-8000-0000000000ab"
	for name, importID := range map[string]string{
		"lower-case ID": tokenID,
		"upper-case ID": strings.ToUpper(tokenID),
	} {
		t.Run(name, func(t *testing.T) {
			fake := newUserContractREST(t)
			fake.addServiceAccount("contract_svc")
			fake.addToken("contract_svc", mdrest.Token{ID: tokenID, Name: "loader", TokenType: "read_scaling", CreatedTS: "2026-10-01T00:00:00Z"})
			config := accessTokenContractConfig(fake.server.URL, "")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
				Steps: []resource.TestStep{
					{
						Config: config, ResourceName: "motherduck_access_token.test", ImportState: true,
						ImportStateId: "contract_svc/" + importID, ImportStatePersist: true,
					},
					{
						Config: config,
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
						},
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr("motherduck_access_token.test", "id", tokenID),
							resource.TestCheckResourceAttr("motherduck_access_token.test", "token_type", "read_scaling"),
						),
					},
				},
			})
		})
	}
}

// TestContractServiceAccountLegacyUsername imports and manages an existing
// service account whose username predates the creation rule, as
// mdw-writer@motherduck-com does live. Creating an account with such a name
// still fails at plan time.
func TestContractServiceAccountLegacyUsername(t *testing.T) {
	const legacy = "mdw-writer@motherduck-com"
	fake := newUserContractREST(t)
	fake.addServiceAccount(legacy)
	config := contractProviderConfig(fake.server.URL) + fmt.Sprintf(`
resource "motherduck_service_account" "legacy" {
  username = %q
}
`, legacy)
	withNew := config + `
resource "motherduck_service_account" "new" {
  username = "new-writer"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{
				Config: config, ResourceName: "motherduck_service_account.legacy", ImportState: true,
				ImportStateId: legacy, ImportStatePersist: true,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_service_account.legacy", "id", legacy),
			},
			{Config: withNew, ExpectError: regexp.MustCompile("MotherDuck cannot create service account new-writer")},
		},
	})
}
