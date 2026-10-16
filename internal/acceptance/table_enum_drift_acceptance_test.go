//go:build acceptance

package acceptance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// A plan may propose a destructive replacement for type drift, but observing
// that plan must preserve the remote rows until the operator chooses to apply.
func TestPluginTestingTableEnumCaseDrift(t *testing.T) {
	requireAcceptance(t)
	token := os.Getenv("MOTHERDUCK_TOKEN")
	if token == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required")
	}
	database := fmt.Sprintf("tf_acc_enum_%d", time.Now().UnixNano())
	probe, err := mdsql.New(t.Context(), mdsql.Config{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := probe.Exec(ctx, "DROP DATABASE IF EXISTS "+sqlbuild.QuoteIdentifier(database)+" CASCADE"); err != nil {
			t.Errorf("owned database cleanup: %v", err)
		}
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
		if err := checkDatabaseDestroyed(database); err != nil {
			t.Error(err)
		}
	})
	table := sqlbuild.QuoteQualifiedIdentifier(database, "main", "facts")
	config := fmt.Sprintf(`
resource "motherduck_database" "test" { name = %q }
resource "motherduck_table" "test" {
 database = motherduck_database.test.name
 schema = "main"
 name = "facts"
 columns = { id = "INT", label = "ENUM('open', 'closed')" }
}
`, database)
	checkRemote := func(label, labelType string) error {
		if err := probe.AttachDatabase(t.Context(), database); err != nil {
			return err
		}
		got, err := probe.ScalarString(t.Context(), "SELECT data_type FROM information_schema.columns WHERE table_catalog = ? AND table_schema = 'main' AND table_name = 'facts' AND column_name = 'label'", database)
		if err != nil || got != labelType {
			return fmt.Errorf("remote type = %q, error = %v, want %q", got, err, labelType)
		}
		got, err = probe.ScalarString(t.Context(), "SELECT string_agg(id::VARCHAR || ':' || label::VARCHAR, ',' ORDER BY id) FROM "+table)
		if err != nil || got != "1:"+label {
			return fmt.Errorf("remote rows = %q, error = %v, want 1:%s", got, err, label)
		}
		return nil
	}
	importCheck := func(want string) resource.ImportStateCheckFunc {
		return func(states []*terraform.InstanceState) error {
			if len(states) != 1 || states[0].Attributes["columns.label"] != want || states[0].Attributes["columns.id"] != "INTEGER" {
				return fmt.Errorf("unexpected imported types: %#v", states)
			}
			return nil
		}
	}
	alterLabel := func(label, expression string) func() {
		return func() {
			if err := probe.Exec(t.Context(), "ALTER TABLE "+table+" ALTER label TYPE ENUM('"+label+"', 'closed') USING "+expression+"(label::VARCHAR)::ENUM('"+label+"', 'closed')"); err != nil {
				t.Fatal(err)
			}
			if err := checkRemote(label, "ENUM('"+label+"', 'closed')"); err != nil {
				t.Fatal(err)
			}
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return checkDatabaseDestroyed(database) },
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: func(*terraform.State) error {
				if err := probe.AttachDatabase(t.Context(), database); err != nil {
					return err
				}
				if err := probe.Exec(t.Context(), "INSERT INTO "+table+" VALUES (1, 'open')"); err != nil {
					return err
				}
				return checkRemote("open", "ENUM('open', 'closed')")
			}},
			{Config: config, ResourceName: "motherduck_table.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"columns"}, ImportStateCheck: importCheck("ENUM('open', 'closed')")},
			{PreConfig: alterLabel("OPEN", "upper"), Config: config, PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionReplace)}}},
			{Config: config, ResourceName: "motherduck_table.test", ImportState: true, ImportStateCheck: func(states []*terraform.InstanceState) error {
				if err := importCheck("ENUM('OPEN', 'closed')")(states); err != nil {
					return err
				}
				return checkRemote("OPEN", "ENUM('OPEN', 'closed')")
			}},
			{PreConfig: alterLabel("open", "lower"), Config: strings.Replace(config, `id = "INT"`, `id = "INTEGER"`, 1), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionUpdate)}, PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: func(*terraform.State) error { return checkRemote("open", "ENUM('open', 'closed')") }},
			{Config: strings.Replace(config, `id = "INT"`, `id = "INTEGER"`, 1), ResourceName: "motherduck_table.test", ImportState: true, ImportStateVerify: true, ImportStateCheck: importCheck("ENUM('open', 'closed')")},
		},
	})
}
