//go:build contract

package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestContractRESTInventoryEmptyAndRecovery(t *testing.T) {
	var empty, denied atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected write", 500)
			return
		}
		if denied.Load() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"FORBIDDEN","message":"inventory denied"}`))
			return
		}
		switch r.URL.RequestURI() {
		case "/v1/users/svc/tokens":
			tokens := []mdrest.Token{}
			if !empty.Load() {
				tokens = []mdrest.Token{{ID: "z", Token: "must-not-reach-state"}, {ID: "a", Name: "loader", ReadOnly: true}}
			}
			_ = json.NewEncoder(w).Encode(mdrest.ListTokensResponse{Tokens: tokens})
		case "/v1/users?limit=1000":
			users := []mdrest.User{}
			if !empty.Load() {
				users = []mdrest.User{{ID: "u2", Username: "svc_b", Roles: []string{"loader", "explorer"}, IsServiceAccount: true}, {ID: "u1", Username: "svc_a"}}
			}
			_ = json.NewEncoder(w).Encode(mdrest.ListUsersResponse{Users: users})
		case "/v1/active_accounts":
			accounts := []mdrest.ActiveAccount{}
			if !empty.Load() {
				accounts = []mdrest.ActiveAccount{
					{Username: "svc_b", Ducklings: []mdrest.Duckling{{ID: "rw", Type: "read_write", Status: "active"}, {ID: "rs.0", Type: "read_scaling", Status: "cooldown"}}},
					{Username: "svc_a", Ducklings: []mdrest.Duckling{}},
				}
			}
			_ = json.NewEncoder(w).Encode(mdrest.ActiveAccountsResponse{Accounts: accounts})
		default:
			t.Errorf("unexpected inventory request: %s", r.URL)
			http.Error(w, "unexpected route", 500)
		}
	}))
	defer server.Close()
	config := contractProviderConfig(server.URL) + `
data "motherduck_user_tokens" "test" { username = "svc" }
data "motherduck_users" "test" {}
data "motherduck_active_accounts" "test" {}
`
	populated := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens.#", "2"),
		resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens.0.id", "a"),
		resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens.0.read_only", "true"),
		resource.TestCheckNoResourceAttr("data.motherduck_user_tokens.test", "tokens.1.name"),
		resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens_json", `[{"id":"a","name":"loader","created_ts":"","read_only":true,"token_type":""},{"id":"z","created_ts":"","read_only":false,"token_type":""}]`),
		resource.TestCheckResourceAttr("data.motherduck_users.test", "users.#", "2"),
		resource.TestCheckResourceAttr("data.motherduck_users.test", "users.0.id", "u1"),
		resource.TestCheckResourceAttr("data.motherduck_users.test", "users.0.roles.#", "0"),
		resource.TestCheckNoResourceAttr("data.motherduck_users.test", "users.0.first_name"),
		resource.TestCheckResourceAttr("data.motherduck_users.test", "users.1.roles.0", "explorer"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.#", "2"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.0.username", "svc_a"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.0.ducklings.#", "0"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.1.ducklings.0.id", "rs.0"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.1.ducklings.0.type", "read_scaling"),
		resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.1.ducklings.1.status", "active"),
	)
	noOp := resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{Config: config, Check: populated, ConfigPlanChecks: noOp},
			{Config: config, PreConfig: func() { empty.Store(true) }, ConfigPlanChecks: noOp, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens.#", "0"),
				resource.TestCheckResourceAttr("data.motherduck_user_tokens.test", "tokens_json", "[]"),
				resource.TestCheckResourceAttr("data.motherduck_users.test", "users.#", "0"),
				resource.TestCheckResourceAttr("data.motherduck_active_accounts.test", "accounts.#", "0"),
			)},
			{Config: config, PreConfig: func() { denied.Store(true) }, ExpectError: regexp.MustCompile("inventory denied")},
			{Config: config, PreConfig: func() { denied.Store(false); empty.Store(false) }, Check: populated, ConfigPlanChecks: noOp},
		},
	})
}
