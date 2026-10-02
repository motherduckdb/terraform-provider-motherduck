//go:build acceptance

package acceptance

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// The SQL fixture owns the secret. Terraform owns only its metadata lookup.
func TestPluginTestingSecretLookup(t *testing.T) {
	requireAcceptance(t)
	if os.Getenv("MOTHERDUCK_TOKEN") == "" {
		t.Fatal("MOTHERDUCK_TOKEN is required for MotherDuck SQL acceptance tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	probe, err := mdsql.New(ctx, mdsql.Config{Token: os.Getenv("MOTHERDUCK_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := probe.Close(); err != nil {
			t.Errorf("close SQL probe: %v", err)
		}
	})
	name := fmt.Sprintf("tf_acc_secret_lookup_%d", time.Now().UTC().UnixNano())
	scope := "s3://terraform-provider-motherduck/secret-lookup/" + name + "/"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := probe.Exec(cleanupCtx, "DROP SECRET IF EXISTS "+sqlbuild.QuoteIdentifier(name)+" FROM motherduck"); err != nil {
			t.Errorf("drop owned lookup fixture %q: %v", name, err)
		}
		if exists, err := probe.Exists(cleanupCtx, "SELECT count(*) FROM duckdb_secrets() WHERE name = ? AND storage = 'motherduck'", name); err != nil {
			t.Errorf("audit lookup fixture cleanup: %v", err)
		} else if exists {
			t.Errorf("owned secret %q remains after cleanup", name)
		}
	})
	// These dummy values never access an external bucket.
	create := "CREATE SECRET " + sqlbuild.QuoteIdentifier(name) + " IN MOTHERDUCK (TYPE S3, KEY_ID 'fixture-key', SECRET 'fixture-value', REGION 'us-east-1', SCOPE " + sqlbuild.StringLiteral(scope) + ")"
	if err := probe.Exec(ctx, create); err != nil {
		t.Fatal(err)
	}
	config := func(lookupName string) string {
		return fmt.Sprintf(`
data "motherduck_secret" "test" {
  name = %q
}
`, lookupName)
	}
	checkOwnedSecret := func(*terraform.State) error {
		readCtx, readCancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer readCancel()
		var secretType string
		if err := probe.QueryRow(readCtx, "SELECT type FROM duckdb_secrets() WHERE name = ? AND storage = 'motherduck'", name).Scan(&secretType); err != nil {
			return err
		}
		if !strings.EqualFold(secretType, "s3") {
			return fmt.Errorf("independent secret type = %q, want s3", secretType)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy:             checkOwnedSecret,
		Steps: []resource.TestStep{
			{
				Config:           config(strings.ToUpper(name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.motherduck_secret.test", "name", strings.ToUpper(name)),
					resource.TestMatchResourceAttr("data.motherduck_secret.test", "type", regexp.MustCompile("(?i)^s3$")),
					resource.TestCheckResourceAttr("data.motherduck_secret.test", "persistent", "true"),
					resource.TestCheckResourceAttr("data.motherduck_secret.test", "secret_provider", "config"),
					resource.TestMatchResourceAttr("data.motherduck_secret.test", "scope", regexp.MustCompile(regexp.QuoteMeta(scope))),
					resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "params"),
					resource.TestCheckNoResourceAttr("data.motherduck_secret.test", "secret_sql"),
					checkOwnedSecret,
				),
			},
			{
				Config:      config(name + "_missing"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("MotherDuck secret not found"),
			},
			{
				Config:           config(strings.ToUpper(name)),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            checkOwnedSecret,
			},
		},
	})
}
