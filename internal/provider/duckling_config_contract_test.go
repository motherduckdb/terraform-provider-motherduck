//go:build contract

package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

// ducklingContractREST stores one user's Duckling configuration. Like the
// live service it fills in a default cooldown for a non-Pulse instance whose
// write omits one.
type ducklingContractREST struct {
	mu     sync.Mutex
	config *mdrest.DucklingConfig
	puts   int
	server *httptest.Server
}

const ducklingContractDefaultCooldown = int64(300)

func newDucklingContractREST(t *testing.T) *ducklingContractREST {
	t.Helper()
	fake := &ducklingContractREST{}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *ducklingContractREST) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Authorization") != "Bearer contract-admin-token" {
		http.Error(w, "unexpected authorization", http.StatusUnauthorized)
		return
	}
	if req.URL.Path != "/v1/users/contract_svc/instances" {
		http.Error(w, `{"code":"NOT_FOUND","message":"User not found"}`, http.StatusNotFound)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch req.Method {
	case http.MethodGet:
		if f.config == nil {
			http.Error(w, `{"code":"NOT_FOUND","message":"User not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(f.config)
	case http.MethodPut:
		var body struct {
			Config mdrest.DucklingConfig `json:"config"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg := body.Config
		cfg.ReadWrite.CooldownSeconds = withDefaultCooldown(cfg.ReadWrite.InstanceSize, cfg.ReadWrite.CooldownSeconds)
		cfg.ReadScaling.CooldownSeconds = withDefaultCooldown(cfg.ReadScaling.InstanceSize, cfg.ReadScaling.CooldownSeconds)
		f.config = &cfg
		f.puts++
		_ = json.NewEncoder(w).Encode(cfg)
	default:
		http.Error(w, "unexpected method "+req.Method, http.StatusMethodNotAllowed)
	}
}

func withDefaultCooldown(size string, cooldown *int64) *int64 {
	if cooldown != nil || size == "pulse" {
		return cooldown
	}
	value := ducklingContractDefaultCooldown
	return &value
}

func (f *ducklingContractREST) setReadScalingCooldown(value int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.config.ReadScaling.CooldownSeconds = &value
}

func ducklingContractConfig(baseURL, readScaling string) string {
	return contractProviderConfig(baseURL) + fmt.Sprintf(`
resource "motherduck_duckling_config" "test" {
  username                 = "contract_svc"
  read_write_instance_size = "standard"
%s
}
`, readScaling)
}

func TestContractDucklingConfigLifecycle(t *testing.T) {
	rest := newDucklingContractREST(t)
	withCooldown := ducklingContractConfig(rest.server.URL, `
  read_scaling_instance_size    = "standard"
  read_scaling_flock_size       = 2
  read_scaling_cooldown_seconds = 600`)
	resized := strings.Replace(withCooldown, "read_scaling_flock_size       = 2", "read_scaling_flock_size       = 4", 1)
	withoutCooldown := ducklingContractConfig(rest.server.URL, `
  read_scaling_instance_size = "standard"
  read_scaling_flock_size    = 4`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		CheckDestroy: func(*terraform.State) error {
			rest.mu.Lock()
			defer rest.mu.Unlock()
			if rest.config == nil {
				return errors.New("Duckling config must remain after destroy because the API has no reset endpoint")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: withCooldown,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_duckling_config.test", "id", "contract_svc"),
					resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_write_cooldown_seconds", "300"),
					resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "600"),
					resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_flock_size", "2"),
				),
			},
			{
				ResourceName:      "motherduck_duckling_config.test",
				ImportState:       true,
				ImportStateId:     "contract_svc",
				ImportStateVerify: true,
			},
			{
				Config: resized,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_flock_size", "4"),
			},
			{
				Config: resized,
				PreConfig: func() {
					rest.setReadScalingCooldown(900)
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "600"),
			},
			{
				// Removing a configured cooldown hands it back to MotherDuck
				// without a write: the live value stays and the plan is empty.
				Config: withoutCooldown,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "600"),
			},
			{
				// Unmanaged drift is recorded, not reverted.
				Config: withoutCooldown,
				PreConfig: func() {
					rest.setReadScalingCooldown(900)
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "900"),
			},
			{
				// A new instance size lets MotherDuck apply its default for it.
				Config: strings.Replace(withoutCooldown, `read_scaling_instance_size = "standard"`, `read_scaling_instance_size = "jumbo"`, 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectUnknownValue("motherduck_duckling_config.test", tfjsonpath.New("read_scaling_cooldown_seconds")),
						plancheck.ExpectKnownValue("motherduck_duckling_config.test", tfjsonpath.New("read_write_cooldown_seconds"), knownvalue.Int64Exact(300)),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "300"),
			},
			{
				// Switching to Pulse clears the cooldown.
				Config: strings.Replace(withoutCooldown, `read_scaling_instance_size = "standard"`, `read_scaling_instance_size = "pulse"`, 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds"),
			},
		},
	})
}
