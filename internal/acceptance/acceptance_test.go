//go:build acceptance

package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/provider"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"motherduck": providerserver.NewProtocol6WithError(provider.New("test")()),
}

func TestSQLAcceptanceDatabaseLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}

	runScript(t, "scripts/test-live-database-drop-with-objects.sh")
}

var allowedAcceptanceScripts = map[string]struct{}{
	"scripts/test-live-database-drop-with-objects.sh": {},
	"scripts/test-live-rest-token-matrix.sh":          {},
}

func TestPluginTestingDatabaseLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}

	databaseName := fmt.Sprintf("tf_acc_database_%d", time.Now().UTC().UnixNano())
	config := fmt.Sprintf(`
resource "motherduck_database" "test" {
  name = %q
}
`, databaseName)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(state *terraform.State) error {
			return checkDatabaseDestroyed(databaseName)
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
				Check: resource.TestCheckResourceAttr("motherduck_database.test", "name", databaseName),
			},
			{
				ResourceName:      "motherduck_database.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestPluginTestingSQLObjectLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}

	databaseName := fmt.Sprintf("tf_acc_sql_%d", time.Now().UTC().UnixNano())
	schemaName := "app"
	tableName := "facts"
	viewName := "facts_v"
	viewQuery := fmt.Sprintf("SELECT id, label FROM %s.%s.%s", databaseName, schemaName, tableName)
	updatedViewQuery := viewQuery + " WHERE id > 1"
	probe, err := mdsql.New(t.Context(), mdsql.Config{Token: os.Getenv("MOTHERDUCK_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := probe.Close(); err != nil {
			t.Errorf("close SQL probe: %v", err)
		}
	})
	checkViewRows := func(want string) resource.TestCheckFunc {
		return func(state *terraform.State) error {
			if err := resource.TestCheckResourceAttr("data.motherduck_databases.current", "rows.0.name", databaseName)(state); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			got, err := probe.ScalarString(ctx, fmt.Sprintf("SELECT count(*)::VARCHAR FROM %s.%s.%s", databaseName, schemaName, viewName))
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("remote view row count = %s, want %s", got, want)
			}
			return nil
		}
	}
	config := fmt.Sprintf(`
resource "motherduck_database" "test" {
  name = %[1]q
}

data "motherduck_databases" "current" {
 name = motherduck_database.test.name
 limit = 1
 offset = 0
}

resource "motherduck_schema" "test" {
  database = motherduck_database.test.name
  name     = %[2]q
}

resource "motherduck_table" "test" {
  database = motherduck_database.test.name
  schema   = motherduck_schema.test.name
  name     = %[3]q

  columns = {
    id    = "INTEGER"
    label = "VARCHAR"
  }
}

resource "motherduck_view" "test" {
  database = motherduck_database.test.name
  schema   = motherduck_schema.test.name
  name     = %[4]q
  query    = %[5]q

  depends_on = [motherduck_table.test]
}
`, databaseName, schemaName, tableName, viewName, viewQuery)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(state *terraform.State) error {
			return checkDatabaseDestroyed(databaseName)
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_database.test", "name", databaseName),
					resource.TestCheckResourceAttr("motherduck_schema.test", "name", schemaName),
					resource.TestCheckResourceAttr("motherduck_table.test", "name", tableName),
					resource.TestCheckResourceAttr("motherduck_view.test", "name", viewName),
					func(*terraform.State) error {
						ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
						defer cancel()
						if err := probe.AttachDatabase(ctx, databaseName); err != nil {
							return err
						}
						return probe.Exec(ctx, fmt.Sprintf("INSERT INTO %s.%s.%s VALUES (1, 'first'), (2, 'second')", databaseName, schemaName, tableName))
					},
					checkViewRows("2"),
				),
			},
			{
				Config:            config,
				ResourceName:      "motherduck_database.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            config,
				ResourceName:      "motherduck_schema.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            config,
				ResourceName:      "motherduck_table.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:                  config,
				ResourceName:            "motherduck_view.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"query"},
				// The server canonicalizes SQL. Verify recovered SQL explicitly
				// instead of silently excluding it from the import contract.
				ImportStateCheck: checkImportedViewQuery(probe, "2"),
			},
			{
				Config: strings.Replace(config, fmt.Sprintf("%q", viewQuery), fmt.Sprintf("%q", updatedViewQuery), 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_view.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_view.test", "query", updatedViewQuery),
					checkViewRows("1"),
				),
			},
			{
				ResourceName: "motherduck_view.test", ImportState: true,
				ImportStateVerify: true, ImportStateVerifyIgnore: []string{"query"},
				ImportStateCheck: checkImportedViewQuery(probe, "1"),
			},
			{
				Config: config,
				PreConfig: func() {
					if err := probe.Exec(t.Context(), fmt.Sprintf("DROP DATABASE %s CASCADE", databaseName)); err != nil {
						t.Fatal(err)
					}
				},
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("motherduck_schema.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("motherduck_view.test", plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(state *terraform.State) error {
					if err := resource.TestCheckResourceAttr("data.motherduck_databases.current", "rows.0.name", databaseName)(state); err != nil {
						return err
					}
					// Use a new connection after database recreation to avoid
					// testing a stale attachment to the deleted database UUID.
					restored, err := mdsql.New(t.Context(), mdsql.Config{Token: os.Getenv("MOTHERDUCK_TOKEN")})
					if err != nil {
						return err
					}
					defer func() {
						if err := restored.Close(); err != nil {
							t.Errorf("close restored SQL probe: %v", err)
						}
					}()
					if err := restored.AttachDatabase(t.Context(), databaseName); err != nil {
						return err
					}
					before, err := restored.ScalarString(t.Context(), fmt.Sprintf("SELECT count(*)::VARCHAR FROM %s.%s.%s", databaseName, schemaName, viewName))
					if err != nil {
						return err
					}
					if before != "0" {
						return fmt.Errorf("recreated graph contains %s rows, expected empty structure", before)
					}
					if err := restored.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.%s.%s VALUES (3, 'reloaded')", databaseName, schemaName, tableName)); err != nil {
						return err
					}
					after, err := restored.ScalarString(t.Context(), fmt.Sprintf("SELECT label FROM %s.%s.%s", databaseName, schemaName, viewName))
					if err != nil {
						return err
					}
					if after != "reloaded" {
						return fmt.Errorf("recreated view returned %q", after)
					}
					return nil
				},
			},
		},
	})
}

func checkImportedViewQuery(probe *mdsql.Client, want string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 || states[0].Attributes["query"] == "" {
			return fmt.Errorf("import must recover one view with its SQL")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		got, err := probe.ScalarString(ctx, "SELECT count(*)::VARCHAR FROM ("+states[0].Attributes["query"]+") AS imported_view")
		if err != nil {
			return fmt.Errorf("execute imported view SQL: %w", err)
		}
		if got != want {
			return fmt.Errorf("imported SQL returns %s rows, want %s", got, want)
		}
		return nil
	}
}

func requireAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Fatal("TF_ACC=1 is required for acceptance tests")
	}
}

func runScript(t *testing.T, script string) {
	t.Helper()

	root := repoRoot(t)
	if _, ok := allowedAcceptanceScripts[script]; !ok {
		t.Fatalf("acceptance script %q is not allowlisted", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	runID := fmt.Sprintf("acceptance_%s_%d", time.Now().UTC().Format("20060102150405"), os.Getpid())
	// #nosec G204 -- script is selected from the static allowlist above.
	cmd := exec.CommandContext(ctx, filepath.Join(root, script))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "RUN_ID="+runID)

	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("%s timed out after 10m\n%s", script, output)
	}
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, output)
	}
	if testing.Verbose() {
		t.Logf("%s output:\n%s", script, strings.TrimSpace(string(output)))
	}
}

func checkDatabaseDestroyed(databaseName string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client, err := mdsql.New(ctx, mdsql.Config{
		Token:           os.Getenv("MOTHERDUCK_TOKEN"),
		CustomUserAgent: "terraform-provider-motherduck-acceptance-test",
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := client.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	exists, err := client.Exists(ctx, "SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?", databaseName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("database %q still exists", databaseName)
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve acceptance test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
