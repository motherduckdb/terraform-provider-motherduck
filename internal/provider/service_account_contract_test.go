//go:build contract

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

// userContractREST is an organization with service accounts, human users,
// their tokens, and their Duckling configuration. Like MotherDuck it matches
// usernames case-insensitively and keeps the spelling a user was created with.
// It honors the is_service_account listing filter. A hidden user still exists
// but is left out of GET /v1/users, as when an offset page shift skips it.
type userContractREST struct {
	mu           sync.Mutex
	users        map[string]*mdrest.User
	hidden       map[string]bool
	listFilters  []string
	descriptions map[string]string
	tokens       map[string][]mdrest.Token
	ducklings    map[string]mdrest.DucklingConfig
	creates      int
	deletes      []string
	writes       []string
	server       *httptest.Server
}

func newUserContractREST(t *testing.T) *userContractREST {
	t.Helper()
	fake := &userContractREST{
		users:        map[string]*mdrest.User{},
		hidden:       map[string]bool{},
		descriptions: map[string]string{},
		tokens:       map[string][]mdrest.Token{},
		ducklings:    map[string]mdrest.DucklingConfig{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *userContractREST) addHuman(username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[strings.ToLower(username)] = &mdrest.User{ID: "human-" + username, Username: username, FirstName: "Ada", LastName: "Lovelace", Roles: []string{}}
}

func (f *userContractREST) user(username string) *mdrest.User {
	return f.users[strings.ToLower(username)]
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"User not found"}`))
}

func (f *userContractREST) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Authorization") != "Bearer contract-admin-token" {
		http.Error(w, "unexpected credentials", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Method != http.MethodGet {
		f.writes = append(f.writes, req.Method+" "+req.URL.Path)
	}
	segments := strings.Split(strings.TrimPrefix(req.URL.Path, "/v1/users"), "/")
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/v1/users":
		filter := req.URL.Query().Get("is_service_account")
		f.listFilters = append(f.listFilters, filter)
		users := []mdrest.User{}
		for key, user := range f.users {
			if f.hidden[key] || (filter != "" && fmt.Sprint(user.IsServiceAccount) != filter) {
				continue
			}
			users = append(users, *user)
		}
		_ = json.NewEncoder(w).Encode(mdrest.ListUsersResponse{Users: users, TotalCount: int64(len(users))})
	case req.Method == http.MethodPost && req.URL.Path == "/v1/users":
		var body mdrest.CreateServiceAccountRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || f.user(body.Username) != nil {
			http.Error(w, `{"code":"NOT_UNIQUE","message":"That username is already in use"}`, http.StatusBadRequest)
			return
		}
		f.creates++
		f.users[strings.ToLower(body.Username)] = &mdrest.User{ID: fmt.Sprintf("svc-%d", f.creates), Username: body.Username, FirstName: "service", LastName: "account", IsServiceAccount: true, Roles: []string{}}
		f.descriptions[strings.ToLower(body.Username)] = body.Description
		_ = json.NewEncoder(w).Encode(mdrest.ServiceAccount{Username: body.Username, Description: body.Description})
	case len(segments) == 2 && req.Method == http.MethodDelete:
		user := f.user(segments[1])
		if user == nil {
			notFound(w)
			return
		}
		f.deletes = append(f.deletes, user.Username)
		delete(f.users, strings.ToLower(segments[1]))
		delete(f.tokens, strings.ToLower(segments[1]))
		delete(f.ducklings, strings.ToLower(segments[1]))
		w.WriteHeader(http.StatusNoContent)
	case len(segments) == 3 && segments[2] == "instances":
		key := strings.ToLower(segments[1])
		if f.user(segments[1]) == nil {
			notFound(w)
			return
		}
		if req.Method == http.MethodPut {
			var body struct {
				Config mdrest.DucklingConfig `json:"config"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.ducklings[key] = body.Config
		}
		_ = json.NewEncoder(w).Encode(f.ducklings[key])
	case len(segments) >= 3 && segments[2] == "tokens":
		key := strings.ToLower(segments[1])
		if f.user(segments[1]) == nil {
			notFound(w)
			return
		}
		switch {
		case req.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(mdrest.ListTokensResponse{Tokens: append([]mdrest.Token{}, f.tokens[key]...)})
		case req.Method == http.MethodPost:
			var body mdrest.CreateTokenRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			token := mdrest.Token{ID: contractTokenID(len(f.writes)), Name: body.Name, TokenType: body.TokenType, CreatedTS: "2026-10-01T00:00:00Z"}
			f.tokens[key] = append(f.tokens[key], token)
			token.Token = "md_contract_secret"
			_ = json.NewEncoder(w).Encode(token)
		case req.Method == http.MethodDelete && len(segments) == 4:
			kept := f.tokens[key][:0]
			for _, token := range f.tokens[key] {
				if token.ID != segments[3] {
					kept = append(kept, token)
				}
			}
			f.tokens[key] = kept
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected token request", http.StatusBadRequest)
		}
	default:
		http.Error(w, "unexpected endpoint "+req.Method+" "+req.URL.Path, http.StatusBadRequest)
	}
}

func TestContractServiceAccountLifecycle(t *testing.T) {
	fake := newUserContractREST(t)
	config := contractProviderConfig(fake.server.URL) + `
resource "motherduck_service_account" "test" {
  username    = "contract_service"
  description = "Contract loader"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.user("contract_service") != nil || fake.creates != 2 || len(fake.deletes) != 1 {
				return fmt.Errorf("exists=%t creates=%d deletes=%v. Want false,2,1", fake.user("contract_service") != nil, fake.creates, fake.deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_service_account.test", "id", "contract_service"),
					resource.TestCheckResourceAttr("motherduck_service_account.test", "description", "Contract loader"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if got := fake.descriptions["contract_service"]; got != "Contract loader" {
							return fmt.Errorf("description sent on create = %q", got)
						}
						return nil
					},
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			// No API returns a service account description, so import cannot
			// recover it. The import step below checks that import then adopts it.
			{ResourceName: "motherduck_service_account.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"description"}},
			{
				Config: config,
				PreConfig: func() {
					fake.mu.Lock()
					delete(fake.users, "contract_service")
					fake.mu.Unlock()
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_service_account.test", plancheck.ResourceActionCreate)}},
			},
		},
	})
}

// TestContractServiceAccountRefusesHumanUsers proves the provider never adopts
// or deletes a human user. DELETE /v1/users/{username} deletes any user with
// its data, and deleting the last human user drops the organization.
func TestContractServiceAccountRefusesHumanUsers(t *testing.T) {
	fake := newUserContractREST(t)
	fake.addHuman("Ada_Owner")
	humanConfig := contractProviderConfig(fake.server.URL) + `
resource "motherduck_service_account" "human" {
  username = "ada_owner"
}
`
	config := contractProviderConfig(fake.server.URL) + `
resource "motherduck_service_account" "test" {
  username = "contract_service"
}
`
	becomeHuman := func(human bool) func() {
		return func() {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.user("contract_service").IsServiceAccount = !human
		}
	}
	notServiceAccount := regexp.MustCompile("is a human user, not a service account")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.user("Ada_Owner") == nil || strings.Join(fake.deletes, ",") != "contract_service" {
				return fmt.Errorf("deletes = %v, want only the service account", fake.deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: humanConfig, ResourceName: "motherduck_service_account.human", ImportState: true, ImportStateId: "ada_owner",
				ExpectError: notServiceAccount,
			},
			{Config: config},
			// A refresh that finds a human user fails and keeps the state.
			{Config: config, PreConfig: becomeHuman(true), ExpectError: notServiceAccount},
			{Config: config, PreConfig: becomeHuman(false), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			// Destroy finds a human user and refuses to delete it.
			{Config: config, Destroy: true, PreConfig: becomeHuman(true), ExpectError: notServiceAccount},
			{
				Config: config, PreConfig: becomeHuman(false),
				Check: func(*terraform.State) error {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if len(fake.deletes) != 0 {
						return fmt.Errorf("deletes = %v, want none while the user was human", fake.deletes)
					}
					return nil
				},
			},
		},
	})
}

// TestContractServiceAccountPageShiftKeepsState hides a live service account
// from GET /v1/users, as an offset page shift during concurrent deletes does.
// Refresh and destroy must confirm with the Duckling config probe, keep the
// account in state, and never send DELETE for an account they cannot classify.
func TestContractServiceAccountPageShiftKeepsState(t *testing.T) {
	fake := newUserContractREST(t)
	config := contractProviderConfig(fake.server.URL) + `
resource "motherduck_service_account" "test" {
  username = "contract_service"
}
`
	hide := func(hidden bool) func() {
		return func() {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.hidden["contract_service"] = hidden
		}
	}
	unconfirmed := regexp.MustCompile("Unable to confirm MotherDuck service account")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if strings.Join(fake.deletes, ",") != "contract_service" || fake.creates != 1 {
				return fmt.Errorf("creates=%d deletes=%v, want one account deleted once", fake.creates, fake.deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config},
			// Refresh cannot classify the account, so it errors and keeps state.
			{Config: config, PreConfig: hide(true), ExpectError: unconfirmed},
			// Destroy cannot classify the account, so it sends no DELETE.
			{Config: config, Destroy: true, ExpectError: unconfirmed},
			{
				Config: config, PreConfig: hide(false),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: func(*terraform.State) error {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if len(fake.deletes) != 0 || fake.creates != 1 {
						return fmt.Errorf("creates=%d deletes=%v, want the original account untouched", fake.creates, fake.deletes)
					}
					// The lookup lists service accounts first.
					if len(fake.listFilters) == 0 || fake.listFilters[0] != "true" {
						return fmt.Errorf("listing filters = %v, want service accounts first", fake.listFilters)
					}
					return nil
				},
			},
		},
	})
}

// TestContractUsernameCaseChangeUpdatesInPlace changes only the letter case
// of a username. MotherDuck matches usernames case-insensitively, so the
// accounts are the same and nothing is recreated. Replacing the service
// account would delete its data.
func TestContractUsernameCaseChangeUpdatesInPlace(t *testing.T) {
	fake := newUserContractREST(t)
	config := func(username string) string {
		return contractProviderConfig(fake.server.URL) + fmt.Sprintf(`
resource "motherduck_service_account" "test" {
  username    = %q
  description = "Contract loader"
}
resource "motherduck_access_token" "test" {
  username = motherduck_service_account.test.username
  name     = "loader"
}
resource "motherduck_duckling_config" "test" {
  username                   = motherduck_service_account.test.username
  read_write_instance_size   = "pulse"
  read_scaling_instance_size = "pulse"
  read_scaling_flock_size    = 1
}
`, username)
	}
	var tokenID string
	writesAfterCreate := 0
	updatesInPlace := []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("motherduck_service_account.test", plancheck.ResourceActionUpdate),
		plancheck.ExpectResourceAction("motherduck_access_token.test", plancheck.ResourceActionUpdate),
		plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate),
	}
	sameAccount := func(username string) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("motherduck_service_account.test", "username", username),
			resource.TestCheckResourceAttr("motherduck_service_account.test", "id", username),
			resource.TestCheckResourceAttr("motherduck_access_token.test", "username", username),
			resource.TestCheckResourceAttr("motherduck_access_token.test", "token", "md_contract_secret"),
			resource.TestCheckResourceAttr("motherduck_duckling_config.test", "id", username),
			resource.TestCheckResourceAttrWith("motherduck_access_token.test", "id", func(id string) error {
				if tokenID != "" && id != tokenID {
					return fmt.Errorf("token id changed from %s to %s", tokenID, id)
				}
				tokenID = id
				return nil
			}),
			func(*terraform.State) error {
				fake.mu.Lock()
				defer fake.mu.Unlock()
				if fake.creates != 1 || len(fake.deletes) != 0 || len(fake.tokens["contract_service"]) != 1 {
					return fmt.Errorf("creates=%d deletes=%v tokens=%d, want one account and token that were never recreated", fake.creates, fake.deletes, len(fake.tokens["contract_service"]))
				}
				// The case change sends only the idempotent Duckling write.
				if writesAfterCreate > 0 {
					for _, write := range fake.writes[writesAfterCreate:] {
						if !strings.HasPrefix(write, "PUT ") {
							return fmt.Errorf("case change sent %s", write)
						}
					}
				}
				writesAfterCreate = len(fake.writes)
				return nil
			},
		)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{Config: config("contract_service"), Check: sameAccount("contract_service")},
			{
				Config: config("Contract_Service"), Check: sameAccount("Contract_Service"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: updatesInPlace, PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				Config: config("contract_service"), Check: sameAccount("contract_service"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: updatesInPlace, PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				Config: config("contract_reader"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_service_account.test", plancheck.ResourceActionReplace),
				}},
			},
		},
	})
}

// TestContractServiceAccountDescriptionIsCreateOnly proves a description
// change fails the plan instead of replacing the account, which would delete
// its data. An imported account adopts the configured description once.
func TestContractServiceAccountDescriptionIsCreateOnly(t *testing.T) {
	fake := newUserContractREST(t)
	fake.users["contract_imported"] = &mdrest.User{ID: "svc-existing", Username: "contract_imported", IsServiceAccount: true, Roles: []string{}}
	config := func(description string) string {
		return contractProviderConfig(fake.server.URL) + fmt.Sprintf(`
resource "motherduck_service_account" "created" {
  username    = "contract_created"
  description = %[1]q
}
resource "motherduck_service_account" "imported" {
  username    = "contract_imported"
  description = %[1]q
}
`, description)
	}
	withoutDescription := contractProviderConfig(fake.server.URL) + `
resource "motherduck_service_account" "created" {
  username = "contract_created"
}
resource "motherduck_service_account" "imported" {
  username = "contract_imported"
}
`
	createOnly := regexp.MustCompile("Service account description cannot change")
	onlyCreated := func(*terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if got := strings.Join(fake.writes, ","); got != "POST /v1/users" {
			return fmt.Errorf("writes = %s, want only the create of contract_created", got)
		}
		if got := fake.descriptions["contract_created"]; got != "Contract loader" {
			return fmt.Errorf("description sent on create = %q", got)
		}
		return nil
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{
				Config: config("Contract loader"), ResourceName: "motherduck_service_account.imported", ImportState: true,
				ImportStateId: "contract_imported", ImportStatePersist: true,
			},
			// The imported state has no description, so the configured one is
			// recorded in place without an API call.
			{
				Config: config("Contract loader"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_service_account.created", "description", "Contract loader"),
					resource.TestCheckResourceAttr("motherduck_service_account.imported", "description", "Contract loader"),
					onlyCreated,
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_service_account.created", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("motherduck_service_account.imported", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// After creation or adoption the description is create-only, and the
			// error points to the path that keeps the account.
			{Config: config("Contract reader"), ExpectError: createOnly},
			{Config: config("Contract reader"), ExpectError: regexp.MustCompile("terraform state rm")},
			{Config: config("Contract loader"), Check: onlyCreated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			// Removing the description only clears it from state.
			{
				Config: withoutDescription,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("motherduck_service_account.created", "description"),
					resource.TestCheckNoResourceAttr("motherduck_service_account.imported", "description"),
					onlyCreated,
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_service_account.created", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("motherduck_service_account.imported", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// A description added back is adopted once without an API call.
			{
				Config: config("Contract reader"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_service_account.created", "description", "Contract reader"),
					onlyCreated,
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{Config: config("Contract loader"), ExpectError: createOnly},
			{Config: config("Contract reader"), Check: onlyCreated},
		},
	})
}
