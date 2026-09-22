//go:build contract

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestContractRoleAuditExample(t *testing.T) {
	source := contractProviderConfig("http://127.0.0.1")
	for _, file := range []string{"main.tf", "variables.tf"} {
		data, err := os.ReadFile("../../examples/role-access-audit/" + file)
		if err != nil {
			t.Fatal(err)
		}
		source += string(data)
	}
	source += "\noutput \"audit_json\" { value = jsonencode(local.audit) }\n"
	variables := func(strict bool) config.Variables {
		return config.Variables{
			"expected_roles": config.MapVariable(map[string]config.Variable{
				"team": config.ObjectVariable(map[string]config.Variable{
					"members":  config.SetVariable(config.StringVariable("user:alice"), config.StringVariable("role:child")),
					"inherits": config.SetVariable(config.StringVariable("builder")),
				}),
			}),
			"fail_on_drift": config.BoolVariable(strict),
		}
	}
	for _, tc := range []struct {
		name       string
		extra      bool
		directness any
		queryError bool
		want       string
	}{
		{"matching", false, true, false, ""},
		{"extra user", true, true, false, "Role access drift detected"},
		{"unknown directness", false, nil, false, "Cannot audit role inheritance"},
		{"missing direct role", false, false, false, "Role access drift detected"},
		{"permission", false, true, true, "audit access denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &roleAuditSQL{contractSQL: newContractSQL(), extra: tc.extra, directness: tc.directness, queryError: tc.queryError}
			step := resource.TestStep{Config: source, ConfigVariables: variables(true)}
			if tc.want != "" {
				step.ExpectError = regexp.MustCompile(tc.want)
			} else {
				step.ConfigPlanChecks = resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
				step.Check = resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckOutput("compliant", "true"),
					resource.TestMatchOutput("audit_json", regexp.MustCompile(`"inherited_roles":\["explorer"\]`)),
					resource.TestMatchOutput("audit_json", regexp.MustCompile(`"missing_members":\[\]`)),
				)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: contractProviderFactories(client), Steps: []resource.TestStep{step}})
		})
	}
	client := &roleAuditSQL{contractSQL: newContractSQL(), extra: true, directness: true}
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: contractProviderFactories(client), Steps: []resource.TestStep{
		{Config: source, ConfigVariables: variables(false), Check: resource.TestCheckOutput("compliant", "false")},
		{Config: source, ConfigVariables: variables(true), ExpectError: regexp.MustCompile("Role access drift detected")},
		{Config: source, ConfigVariables: variables(true), ExpectError: regexp.MustCompile("Role access drift detected")},
	}})
}

type roleAuditSQL struct {
	*contractSQL
	extra      bool
	directness any
	queryError bool
}

func (c *roleAuditSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	if c.queryError {
		return "", errors.New("audit access denied")
	}
	var rows any
	switch query {
	case `SHOW USERS OF ROLE "team"`:
		users := []map[string]any{{"username": "alice"}}
		if c.extra {
			users = append(users, map[string]any{"username": "unexpected"})
		}
		rows = users
	case `SHOW ROLES OF ROLE "team"`:
		rows = []map[string]any{{"role_name": "child"}}
	case `SHOW ROLES TO ROLE "team"`:
		rows = []map[string]any{{"role_name": "builder", "is_direct": c.directness}, {"role_name": "explorer", "is_direct": false}}
	default:
		return "", fmt.Errorf("unexpected audit SQL: %s", query)
	}
	data, err := json.Marshal(rows)
	return string(data), err
}
func (c *roleAuditSQL) Exec(context.Context, string, ...any) error {
	return errors.New("role audit must not mutate")
}
