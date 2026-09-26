//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

const (
	icebergContractCreate = `CREATE DATABASE "contract_database" (TYPE ICEBERG, "secret" 'catalog_secret', DEFAULT_SCHEMA 'default', ENDPOINT 'https://catalog.example.com', WAREHOUSE 'analytics', ACCESS_DELEGATION_MODE 'vended_credentials', REMOVE_FILES_ON_DELETE FALSE)`
	icebergContractAlter  = `ALTER DATABASE "contract_database" SET "secret" = 'rotated_secret', DEFAULT_SCHEMA = 'analytics', ACCESS_DELEGATION_MODE = NULL, PURGE_REQUESTED = 'true'`
	icebergContractAdopt  = `ALTER DATABASE "contract_database" SET "secret" = 'catalog_secret', DEFAULT_SCHEMA = 'default', ACCESS_DELEGATION_MODE = 'vended_credentials', REMOVE_FILES_ON_DELETE = 'false'`
	icebergContractDrop   = `DROP DATABASE IF EXISTS "contract_database"`
)

type databaseIcebergSQL struct {
	*contractSQL
	exists  bool
	creates int
	alters  []string
}

func (c *databaseIcebergSQL) Exec(ctx context.Context, query string, args ...any) error {
	c.record("exec", query, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case query == icebergContractCreate:
		if c.exists {
			return fmt.Errorf("duplicate database create")
		}
		c.exists = true
		c.creates++
	case strings.HasPrefix(query, `ALTER DATABASE "contract_database" SET `):
		if !c.exists {
			return fmt.Errorf("altering a missing database")
		}
		c.alters = append(c.alters, query)
	case query == icebergContractDrop:
		c.exists = false
	case query == "USE memory":
	default:
		return fmt.Errorf("unexpected SQL exec %q", query)
	}
	return nil
}

func (c *databaseIcebergSQL) QueryRow(ctx context.Context, query string, args ...any) mdsql.RowScanner {
	if strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES") {
		if len(args) != 1 || args[0] != "contract_database" {
			return contractRow{err: fmt.Errorf("unexpected database lookup: %v", args)}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.exists {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return contractRow{values: []any{"00000000-0000-0000-0000-000000000002", "2026-09-01T00:00:00Z", false, nil, "iceberg"}}
	}
	return c.contractSQL.QueryRow(ctx, query, args...)
}

const icebergContractConfig = `
resource "motherduck_database" "test" {
  name          = "contract_database"
  database_type = "iceberg"

  iceberg = {
    secret                 = "catalog_secret"
    default_schema         = "default"
    endpoint               = "https://catalog.example.com"
    warehouse              = "analytics"
    access_delegation_mode = "vended_credentials"
    remove_files_on_delete = false
  }
}
`

func TestContractIcebergDatabaseLifecycle(t *testing.T) {
	client := &databaseIcebergSQL{contractSQL: newContractSQL()}
	config := contractProviderConfig("http://127.0.0.1") + icebergContractConfig
	altered := strings.NewReplacer(
		`secret                 = "catalog_secret"`, `secret                 = "rotated_secret"`,
		`default_schema         = "default"`, `default_schema         = "analytics"`,
		`access_delegation_mode = "vended_credentials"`, `purge_requested        = true`,
	).Replace(config)
	// An unset read_only is read-write, so writing it as false must not
	// replace the database.
	explicitReadWrite := strings.Replace(config, `remove_files_on_delete = false`, "remove_files_on_delete = false\n    read_only              = false", 1)
	replaced := strings.Replace(altered, `warehouse              = "analytics"`, `warehouse              = "other"`, 1)
	unmanaged := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_database" "test" {
  name = "contract_database"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.mu.Lock()
			defer client.mu.Unlock()
			if client.exists || client.creates != 1 {
				return fmt.Errorf("unexpected lifecycle: exists=%t creates=%d", client.exists, client.creates)
			}
			if len(client.alters) != 1 || client.alters[0] != icebergContractAlter {
				return fmt.Errorf("alters = %q, want [%q]", client.alters, icebergContractAlter)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_database.test", "database_type", "iceberg"),
					resource.TestCheckResourceAttr("motherduck_database.test", "iceberg.secret", "catalog_secret"),
					resource.TestCheckNoResourceAttr("motherduck_database.test", "snapshot_retention_days"),
				),
			},
			{
				Config: explicitReadWrite,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: altered,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "iceberg.default_schema", "analytics"),
			},
			{
				Config:             replaced,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
			{
				Config:             unmanaged,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate),
				}},
			},
		},
	})
}

func TestContractIcebergDatabaseImportAdoptsOptionsWithoutReplacement(t *testing.T) {
	client := &databaseIcebergSQL{contractSQL: newContractSQL(), exists: true}
	config := contractProviderConfig("http://127.0.0.1") + icebergContractConfig
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			client.mu.Lock()
			defer client.mu.Unlock()
			if client.exists || client.creates != 0 {
				return fmt.Errorf("import must not recreate: exists=%t creates=%d", client.exists, client.creates)
			}
			if len(client.alters) != 1 || client.alters[0] != icebergContractAdopt {
				return fmt.Errorf("alters = %q, want [%q]", client.alters, icebergContractAdopt)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				ResourceName:       "motherduck_database.test",
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateId:      "contract_database",
				Config:             config,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].Attributes["database_type"] != "iceberg" {
						return fmt.Errorf("imported state = %#v, want database_type iceberg", states)
					}
					return nil
				},
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
