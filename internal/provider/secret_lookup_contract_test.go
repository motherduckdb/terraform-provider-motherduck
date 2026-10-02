//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestContractSecretLookup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		row       []any
		err       error
		wantError string
		check     resource.TestCheckFunc
	}{
		{name: "metadata", row: []any{"ICEBERG", "CONFIG", true, "s3://catalog/"}, check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.motherduck_secret.test", "name", "Lakehouse'Catalog"),
			resource.TestCheckResourceAttr("data.motherduck_secret.test", "type", "ICEBERG"),
			resource.TestCheckResourceAttr("data.motherduck_secret.test", "secret_provider", "CONFIG"),
			resource.TestCheckResourceAttr("data.motherduck_secret.test", "persistent", "true"),
			resource.TestCheckResourceAttr("data.motherduck_secret.test", "scope", "s3://catalog/"),
		)},
		{name: "nullable", row: []any{nil, nil, nil, nil}, check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "type"),
			resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "secret_provider"),
			resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "persistent"),
			resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "scope"),
		)},
		{name: "missing", err: stdsql.ErrNoRows, wantError: "MotherDuck secret not found"},
		{name: "false", row: []any{"s3", "config", false, nil}, check: resource.TestCheckResourceAttr("data.motherduck_secret.test", "persistent", "false")},
		{name: "permission", err: errors.New("catalog access denied"), wantError: "catalog access denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &secretLookupSQL{contractSQL: newContractSQL(), row: tc.row, err: tc.err}
			step := resource.TestStep{
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_secret" "test" {
  name = "Lakehouse'Catalog"
}

`,
				Check: tc.check,
			}
			if tc.wantError != "" {
				step.ExpectError = regexp.MustCompile(tc.wantError)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: contractProviderFactories(client), Steps: []resource.TestStep{step}})
		})
	}
}

func TestContractSecretLookupRejectsBlankName(t *testing.T) {
	client := &secretLookupSQL{contractSQL: newContractSQL()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_secret" "test" {
  name = "  "
}
`,
			ExpectError: regexp.MustCompile("Invalid MotherDuck secret name"),
		}},
	})
}

func TestContractSecretLookupReadFailureRecovers(t *testing.T) {
	client := &secretLookupSQL{contractSQL: newContractSQL(), row: []any{"iceberg", "config", true, nil}}
	config := contractProviderConfig("http://127.0.0.1") + `
data "motherduck_secret" "test" {
  name = "Lakehouse'Catalog"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{
			{Config: config},
			{
				Config:      config,
				PreConfig:   func() { client.err = errors.New("catalog access denied") },
				ExpectError: regexp.MustCompile("Unable to read MotherDuck secret"),
			},
			{
				Config:           config,
				PreConfig:        func() { client.err = nil },
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr("data.motherduck_secret.test", "name", "Lakehouse'Catalog"),
			},
		},
	})
}

func TestContractSecretLookupDeferredName(t *testing.T) {
	client := &secretLookupSQL{contractSQL: newContractSQL(), row: []any{"iceberg", "config", true, nil}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		Steps: []resource.TestStep{{
			Config: contractProviderConfig("http://127.0.0.1") + `
resource "terraform_data" "name" {
  input = "Lakehouse'Catalog"
}
data "motherduck_secret" "test" {
  name = terraform_data.name.output
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			Check:            resource.TestCheckResourceAttr("data.motherduck_secret.test", "name", "Lakehouse'Catalog"),
		}},
	})
}

type secretLookupSQL struct {
	*contractSQL
	row []any
	err error
}

func (c *secretLookupSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	const want = `SELECT type, provider, persistent, scope::VARCHAR FROM duckdb_secrets() WHERE lower(name) = lower(?) AND storage = 'motherduck'`
	if query != want || len(args) != 1 || args[0] != "Lakehouse'Catalog" {
		return contractRow{err: fmt.Errorf("unexpected exact-name lookup: %q %#v", query, args)}
	}
	return contractRow{values: c.row, err: c.err}
}

func (c *secretLookupSQL) Exec(context.Context, string, ...any) error {
	return errors.New("secret lookup must not execute mutations")
}
