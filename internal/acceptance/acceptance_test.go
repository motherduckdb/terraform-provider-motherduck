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
		t.Skip("set MOTHERDUCK_TOKEN to run MotherDuck SQL acceptance tests")
	}

	runScript(t, "scripts/test-live-database-drop-with-objects.sh")
}

func TestRESTAcceptanceAdminLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_ADMIN_TOKEN") == "" {
		t.Skip("set MOTHERDUCK_ADMIN_TOKEN to run MotherDuck REST acceptance tests")
	}

	runScript(t, "scripts/test-live-rest-token-matrix.sh")
}

var allowedAcceptanceScripts = map[string]struct{}{
	"scripts/test-live-database-drop-with-objects.sh": {},
	"scripts/test-live-rest-token-matrix.sh":          {},
}

func TestPluginTestingDatabaseLifecycle(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Skip("set MOTHERDUCK_TOKEN to run MotherDuck SQL acceptance tests")
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
		t.Skip("set MOTHERDUCK_TOKEN to run MotherDuck SQL acceptance tests")
	}

	databaseName := fmt.Sprintf("tf_acc_sql_%d", time.Now().UTC().UnixNano())
	schemaName := "app"
	tableName := "facts"
	viewName := "facts_v"
	viewQuery := fmt.Sprintf("SELECT id, label FROM %s.%s.%s", databaseName, schemaName, tableName)
	config := fmt.Sprintf(`
resource "motherduck_database" "test" {
  name = %[1]q
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
			},
		},
	})
}

func requireAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run acceptance tests")
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
