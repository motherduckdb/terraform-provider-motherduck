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

// ducklingContractREST stores one user's Duckling configuration exactly as
// written, like the live service (entity_service/src/lib/ducklings.ts). A user
// with no stored configuration reads the organization default. Reads fill in
// a default cooldown for a non-Pulse size that has none stored. A plan limit
// lowers the reported sizes but keeps the stored cooldowns, as applyLimits
// does, and a write that exceeds the limit or sets a Pulse cooldown fails.
type ducklingContractREST struct {
	mu         sync.Mutex
	config     *mdrest.DucklingConfig
	pulseLimit bool
	puts       int
	lastPut    *mdrest.DucklingConfig
	server     *httptest.Server
}

const ducklingContractDefaultCooldown = int64(300)

var ducklingContractOrgDefault = mdrest.DucklingConfig{
	ReadWrite:   mdrest.DucklingReadWriteConfig{InstanceSize: "standard"},
	ReadScaling: mdrest.DucklingReadScalingConfig{InstanceSize: "standard", FlockSize: 4},
}

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
		cfg := ducklingContractOrgDefault
		if f.config != nil {
			cfg = *f.config
		}
		if f.pulseLimit {
			cfg.ReadWrite.InstanceSize = "pulse"
			cfg.ReadScaling.InstanceSize = "pulse"
		}
		_ = json.NewEncoder(w).Encode(withDefaultCooldowns(cfg))
	case http.MethodPut:
		var body struct {
			Config mdrest.DucklingConfig `json:"config"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cfg := body.Config
		if f.pulseLimit && (cfg.ReadWrite.InstanceSize != "pulse" || cfg.ReadScaling.InstanceSize != "pulse") {
			http.Error(w, `{"code":"INVALID_ARGUMENT","message":"Invalid config for tier free: [object Object]"}`, http.StatusBadRequest)
			return
		}
		if (cfg.ReadWrite.InstanceSize == "pulse" && cfg.ReadWrite.CooldownSeconds != nil) || (cfg.ReadScaling.InstanceSize == "pulse" && cfg.ReadScaling.CooldownSeconds != nil) {
			http.Error(w, `{"code":"INVALID_ARGUMENT","message":"cooldown_seconds cannot be set for Pulse instances"}`, http.StatusBadRequest)
			return
		}
		f.config = &cfg
		f.lastPut = &cfg
		f.puts++
		_ = json.NewEncoder(w).Encode(withDefaultCooldowns(cfg))
	default:
		http.Error(w, "unexpected method "+req.Method, http.StatusMethodNotAllowed)
	}
}

func withDefaultCooldowns(cfg mdrest.DucklingConfig) mdrest.DucklingConfig {
	cfg.ReadWrite.CooldownSeconds = withDefaultCooldown(cfg.ReadWrite.InstanceSize, cfg.ReadWrite.CooldownSeconds)
	cfg.ReadScaling.CooldownSeconds = withDefaultCooldown(cfg.ReadScaling.InstanceSize, cfg.ReadScaling.CooldownSeconds)
	return cfg
}

func withDefaultCooldown(size string, cooldown *int64) *int64 {
	if cooldown != nil || size == "pulse" {
		return cooldown
	}
	value := ducklingContractDefaultCooldown
	return &value
}

// setReadScalingCooldown stores a cooldown override, as the MotherDuck UI does.
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

func ducklingContractSizes(baseURL, size string, flock int) string {
	return contractProviderConfig(baseURL) + fmt.Sprintf(`
resource "motherduck_duckling_config" "test" {
  username                   = "contract_svc"
  read_write_instance_size   = %[1]q
  read_scaling_instance_size = %[1]q
  read_scaling_flock_size    = %[2]d
}
`, size, flock)
}

// checkLastPutCooldowns checks the cooldowns the last write sent.
func (f *ducklingContractREST) checkLastPutCooldowns(readWrite, readScaling *int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.lastPut == nil {
			return errors.New("no Duckling config write")
		}
		format := func(v *int64) string {
			if v == nil {
				return "none"
			}
			return fmt.Sprint(*v)
		}
		got := format(f.lastPut.ReadWrite.CooldownSeconds) + "/" + format(f.lastPut.ReadScaling.CooldownSeconds)
		if want := format(readWrite) + "/" + format(readScaling); got != want {
			return fmt.Errorf("last write sent cooldowns %s, want %s", got, want)
		}
		return nil
	}
}

func int64Pointer(v int64) *int64 { return &v }

// TestContractDucklingConfigAfterPlanLimitLowersSizeToPulse follows an
// organization that drops to a plan whose limit is Pulse. MotherDuck lowers
// the reported sizes to Pulse but keeps reporting the stored cooldowns, and it
// rejects a write that sends a cooldown for Pulse. A configuration with Pulse
// sizes and no cooldowns must apply without sending the stale cooldowns.
func TestContractDucklingConfigAfterPlanLimitLowersSizeToPulse(t *testing.T) {
	rest := newDucklingContractREST(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		Steps: []resource.TestStep{
			{Config: ducklingContractSizes(rest.server.URL, "standard", 2)},
			{
				// A flock change keeps the live cooldowns and sends them.
				Config: ducklingContractSizes(rest.server.URL, "standard", 4),
				Check:  rest.checkLastPutCooldowns(int64Pointer(300), int64Pointer(300)),
			},
			{
				PreConfig: func() {
					rest.mu.Lock()
					defer rest.mu.Unlock()
					rest.pulseLimit = true
				},
				Config: ducklingContractSizes(rest.server.URL, "pulse", 3),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_duckling_config.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue("motherduck_duckling_config.test", tfjsonpath.New("read_write_cooldown_seconds"), knownvalue.Null()),
						plancheck.ExpectKnownValue("motherduck_duckling_config.test", tfjsonpath.New("read_scaling_cooldown_seconds"), knownvalue.Null()),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("motherduck_duckling_config.test", "read_write_cooldown_seconds"),
					resource.TestCheckNoResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds"),
					rest.checkLastPutCooldowns(nil, nil),
				),
			},
		},
	})
}

// TestContractDucklingConfigCreateSendsNoUnsetCooldown sends no cooldown on
// create when the configuration leaves it unset. A GET fills in MotherDuck's
// default, or an organization-level cooldown, for a user with no stored
// cooldown, so sending the live value back would turn it into a stored
// user-level override. MotherDuck stores the write exactly as sent, so it
// applies its default and replaces a cooldown set outside Terraform.
func TestContractDucklingConfigCreateSendsNoUnsetCooldown(t *testing.T) {
	for name, stored := range map[string]*mdrest.DucklingConfig{
		// The user has no stored configuration and reads the organization
		// default, with the default cooldowns filled in.
		"organization default": nil,
		// The user has cooldowns set in the MotherDuck UI at the same sizes.
		"outside override": {
			ReadWrite:   mdrest.DucklingReadWriteConfig{InstanceSize: "standard", CooldownSeconds: int64Pointer(900)},
			ReadScaling: mdrest.DucklingReadScalingConfig{InstanceSize: "standard", FlockSize: 2, CooldownSeconds: int64Pointer(1200)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rest := newDucklingContractREST(t)
			rest.config = stored
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
				Steps: []resource.TestStep{{
					Config: ducklingContractSizes(rest.server.URL, "standard", 2),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
					Check: resource.ComposeAggregateTestCheckFunc(
						rest.checkLastPutCooldowns(nil, nil),
						resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_write_cooldown_seconds", "300"),
						resource.TestCheckResourceAttr("motherduck_duckling_config.test", "read_scaling_cooldown_seconds", "300"),
					),
				}},
			})
		})
	}
}
