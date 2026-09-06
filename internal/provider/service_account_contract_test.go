//go:build contract

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestContractServiceAccountLifecycle(t *testing.T) {
	var mu sync.Mutex
	exists, creates, deletes := false, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Header.Get("Authorization") != "Bearer contract-admin-token" {
			http.Error(w, "unexpected credentials", http.StatusUnauthorized)
			return
		}
		switch req.Method + " " + req.URL.Path {
		case "POST /v1/users":
			var body struct {
				Username string `json:"username"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Username != "contract_service" || exists {
				http.Error(w, "invalid or duplicate creation", http.StatusBadRequest)
				return
			}
			exists = true
			creates++
			_, _ = w.Write([]byte(`{"username":"contract_service"}`))
		case "GET /v1/users/contract_service/instances":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"User not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case "DELETE /v1/users/contract_service":
			exists = false
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected endpoint", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	config := contractProviderConfig(server.URL) + `
resource "motherduck_service_account" "test" {
  username = "contract_service"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(newContractSQL()),
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if exists || creates != 2 || deletes != 1 {
				return fmt.Errorf("exists=%t creates=%d deletes=%d; want false,2,1", exists, creates, deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: resource.TestCheckResourceAttr("motherduck_service_account.test", "id", "contract_service"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{ResourceName: "motherduck_service_account.test", ImportState: true, ImportStateVerify: true},
			{Config: config, PreConfig: func() { mu.Lock(); exists = false; mu.Unlock() },
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_service_account.test", plancheck.ResourceActionCreate)}}},
		},
	})
}
