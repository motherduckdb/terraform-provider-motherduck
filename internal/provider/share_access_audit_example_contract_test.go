//go:build contract

package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestContractShareAccessAuditExampleCompliant(t *testing.T) {
	client := &shareGrantsSQL{
		contractSQL:       newContractSQL(),
		rows:              `[{"share_owner":"owner","grantee_name":"analytics readers","grantee_type":"role","privilege":"read","granted_at":"2026-09-02T00:00:00Z"},{"share_owner":"owner","grantee_name":"svc:reader","grantee_type":"user","privilege":"read","granted_at":null}]`,
		functionAvailable: true,
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: shareAccessAuditExampleConfig(t),
			ConfigVariables: shareAccessAuditVariables(map[string][]string{
				"tenant's share": {"user:svc:reader", "role:analytics readers", "user:svc:reader"},
			}, false),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckOutput("compliant", "true"),
				checkShareAccessAuditOutput(map[string]shareAccessAuditResult{
					"tenant's share": {},
				}),
			),
		}},
	})
}

func TestContractShareAccessAuditExampleDriftReports(t *testing.T) {
	tests := map[string]struct {
		rows     string
		expected map[string][]string
		want     shareAccessAuditResult
	}{
		"unexpected user": {
			rows:     shareGrantsContractRows,
			expected: map[string][]string{"tenant's share": {"role:analytics_readers"}},
			want:     shareAccessAuditResult{unexpected: []string{"user:svc_reader"}},
		},
		"missing role": {
			rows:     `[ {"share_owner":"owner","grantee_name":"svc_reader","grantee_type":"user","privilege":"read","granted_at":null} ]`,
			expected: map[string][]string{"tenant's share": {"role:analytics_readers"}},
			want:     shareAccessAuditResult{missing: []string{"role:analytics_readers"}, unexpected: []string{"user:svc_reader"}},
		},
		"organization broadening": {
			rows:     `[{"share_owner":"owner","grantee_name":"ENTIRE_ORGANIZATION","grantee_type":"organization","privilege":"read","granted_at":null}]`,
			expected: map[string][]string{"tenant's share": {"role:analytics_readers"}},
			want:     shareAccessAuditResult{missing: []string{"role:analytics_readers"}, unexpected: []string{"organization:ENTIRE_ORGANIZATION"}},
		},
		"public broadening": {
			rows:     `[{"share_owner":"owner","grantee_name":"ALL_USERS","grantee_type":"domain","privilege":"read","granted_at":null}]`,
			expected: map[string][]string{"tenant's share": {"role:analytics_readers"}},
			want:     shareAccessAuditResult{missing: []string{"role:analytics_readers"}, unexpected: []string{"domain:ALL_USERS"}},
		},
		"empty actual": {
			rows:     `[]`,
			expected: map[string][]string{"tenant's share": {"role:analytics_readers"}},
			want:     shareAccessAuditResult{missing: []string{"role:analytics_readers"}},
		},
		"same name different audience": {
			rows:     `[{"share_owner":"owner","grantee_name":"same_name","grantee_type":"role","privilege":"read","granted_at":null}]`,
			expected: map[string][]string{"tenant's share": {"user:same_name"}},
			want:     shareAccessAuditResult{missing: []string{"user:same_name"}, unexpected: []string{"role:same_name"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client := &shareGrantsSQL{contractSQL: newContractSQL(), rows: tc.rows, functionAvailable: true}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(client),
				Steps: []resource.TestStep{{
					Config:          shareAccessAuditExampleConfig(t),
					ConfigVariables: shareAccessAuditVariables(tc.expected, false),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckOutput("compliant", "false"),
						checkShareAccessAuditOutput(map[string]shareAccessAuditResult{"tenant's share": tc.want}),
					),
				}},
			})
		})
	}
}

func TestContractShareAccessAuditExampleEmptyExpected(t *testing.T) {
	client := &shareGrantsSQL{contractSQL: newContractSQL(), rows: `[]`, functionAvailable: true}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config:          shareAccessAuditExampleConfig(t),
			ConfigVariables: shareAccessAuditVariables(map[string][]string{"tenant's share": {}}, false),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckOutput("compliant", "true"),
				checkShareAccessAuditOutput(map[string]shareAccessAuditResult{"tenant's share": {}}),
			),
		}},
	})
}

func TestContractShareAccessAuditExampleFailOnDrift(t *testing.T) {
	client := &shareGrantsSQL{contractSQL: newContractSQL(), rows: shareGrantsContractRows, functionAvailable: true}
	exampleConfig := shareAccessAuditExampleConfig(t)
	expected := map[string][]string{"tenant's share": {"role:analytics_readers"}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{
				Config:          exampleConfig,
				ConfigVariables: shareAccessAuditVariables(expected, false),
				Check:           resource.TestCheckOutput("compliant", "false"),
			},
			{
				Config:          exampleConfig,
				ConfigVariables: shareAccessAuditVariables(expected, true),
				ExpectError:     regexp.MustCompile(`Share access drift detected.*fail_on_drift=false`),
			},
		},
	})
}

func TestContractShareAccessAuditExampleErrors(t *testing.T) {
	tests := map[string]struct {
		variables config.Variables
		client    *shareGrantsSQL
		wantError string
	}{
		"empty expected map": {
			variables: shareAccessAuditVariables(map[string][]string{}, false),
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `expected_grants must include at least one share`,
		},
		"null expected map": {
			variables: config.Variables{"expected_grants": nullConfigVariable{}},
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `required variable may not be set to null|cannot be null`,
		},
		"blank share name": {
			variables: shareAccessAuditVariables(map[string][]string{" ": {}}, false),
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `Share names must be non-blank`,
		},
		"blank audience": {
			variables: shareAccessAuditVariables(map[string][]string{"tenant's share": {"user:"}}, false),
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `Each audience must be user:<name>`,
		},
		"null audience set": {
			variables: config.Variables{
				"expected_grants": config.MapVariable(map[string]config.Variable{
					"tenant's share": nullConfigVariable{},
				}),
			},
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `Each audience must be user:<name>`,
		},
		"null audience": {
			variables: config.Variables{
				"expected_grants": config.MapVariable(map[string]config.Variable{
					"tenant's share": config.SetVariable(nullConfigVariable{}),
				}),
			},
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `expected_grants.*null|Each audience must be user:<name>`,
		},
		"invalid audience": {
			variables: shareAccessAuditVariables(map[string][]string{"tenant's share": {"viewer:someone"}}, false),
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true},
			wantError: `Each audience must be user:<name>`,
		},
		"inaccessible share": {
			variables: shareAccessAuditVariables(map[string][]string{"tenant's share": {}}, false),
			client:    &shareGrantsSQL{contractSQL: newContractSQL(), functionAvailable: true, rowsErr: errors.New(`Catalog Error: Share "tenant's share" not found`)},
			wantError: `not found`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: contractProviderFactories(tc.client),
				Steps: []resource.TestStep{{
					Config:          shareAccessAuditExampleConfig(t),
					ConfigVariables: tc.variables,
					ExpectError:     regexp.MustCompile(tc.wantError),
				}},
			})
		})
	}
}

func shareAccessAuditExampleConfig(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate the example contract test")
	}
	exampleDir := filepath.Join(filepath.Dir(testFile), "..", "..", "examples", "share-access-audit")

	var example strings.Builder
	example.WriteString(contractProviderConfig("http://127.0.0.1"))
	for _, name := range []string{"main.tf", "variables.tf", "outputs.tf"} {
		contents, err := os.ReadFile(filepath.Join(exampleDir, name))
		if err != nil {
			t.Fatalf("read share access audit example %s: %v", name, err)
		}
		example.Write(contents)
		example.WriteString("\n")
	}
	return example.String()
}

func shareAccessAuditVariables(expected map[string][]string, failOnDrift bool) config.Variables {
	grants := make(map[string]config.Variable, len(expected))
	for shareName, audiences := range expected {
		values := make([]config.Variable, 0, len(audiences))
		for _, audience := range audiences {
			values = append(values, config.StringVariable(audience))
		}
		grants[shareName] = config.SetVariable(values...)
	}
	return config.Variables{
		"expected_grants": config.MapVariable(grants),
		"fail_on_drift":   config.BoolVariable(failOnDrift),
	}
}

type nullConfigVariable struct{}

func (nullConfigVariable) MarshalJSON() ([]byte, error) {
	return []byte("null"), nil
}

type shareAccessAuditResult struct {
	missing    []string
	unexpected []string
}

func checkShareAccessAuditOutput(want map[string]shareAccessAuditResult) resource.TestCheckFunc {
	return func(state *terraform.State) error {
		output, ok := state.RootModule().Outputs["audit"]
		if !ok {
			return errors.New("audit output is missing")
		}
		got, ok := output.Value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("audit output has type %T, want map[string]interface{}", output.Value)
		}
		if len(got) != len(want) {
			return fmt.Errorf("audit output has %d shares, want %d", len(got), len(want))
		}
		for shareName, expected := range want {
			share, ok := got[shareName].(map[string]interface{})
			if !ok {
				return fmt.Errorf("audit output share %q has type %T", shareName, got[shareName])
			}
			missing, err := auditOutputStrings(share["missing"])
			if err != nil {
				return fmt.Errorf("audit output share %q missing: %w", shareName, err)
			}
			unexpected, err := auditOutputStrings(share["unexpected"])
			if err != nil {
				return fmt.Errorf("audit output share %q unexpected: %w", shareName, err)
			}
			if !stringSlicesEqual(missing, expected.missing) || !stringSlicesEqual(unexpected, expected.unexpected) {
				return fmt.Errorf("audit output share %q = missing %#v, unexpected %#v, want missing %#v, unexpected %#v", shareName, missing, unexpected, expected.missing, expected.unexpected)
			}
		}
		return nil
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func auditOutputStrings(value interface{}) ([]string, error) {
	if value == nil {
		return nil, errors.New("value is missing")
	}
	switch values := value.(type) {
	case []interface{}:
		result := make([]string, len(values))
		for index, value := range values {
			stringValue, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("element %d has type %T", index, value)
			}
			result[index] = stringValue
		}
		return result, nil
	case []string:
		return values, nil
	default:
		return nil, fmt.Errorf("has type %T", value)
	}
}
