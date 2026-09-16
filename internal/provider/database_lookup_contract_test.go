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
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

func TestContractDatabaseLookup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		row       []any
		err       error
		wantError string
		check     resource.TestCheckFunc
	}{
		{name: "metadata", row: []any{"database-uuid", "2026-09-16T00:00:00Z", true, "7 days", "MOTHERDUCK"}, check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.motherduck_database.test", "uuid", "database-uuid"),
			resource.TestCheckResourceAttr("data.motherduck_database.test", "created_ts", "2026-09-16T00:00:00Z"),
			resource.TestCheckResourceAttr("data.motherduck_database.test", "transient", "true"),
			resource.TestCheckResourceAttr("data.motherduck_database.test", "historical_snapshot_retention", "7 days"),
			resource.TestCheckResourceAttr("data.motherduck_database.test", "database_type", "MOTHERDUCK"),
		)},
		{name: "nullable", row: []any{nil, nil, nil, nil, nil}, check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckNoResourceAttr("data.motherduck_database.test", "uuid"),
			resource.TestCheckNoResourceAttr("data.motherduck_database.test", "created_ts"),
			resource.TestCheckNoResourceAttr("data.motherduck_database.test", "transient"),
			resource.TestCheckNoResourceAttr("data.motherduck_database.test", "historical_snapshot_retention"),
			resource.TestCheckNoResourceAttr("data.motherduck_database.test", "database_type"),
		)},
		{name: "false", row: []any{"database-uuid", nil, false, nil, "DUCKLAKE"}, check: resource.TestCheckResourceAttr("data.motherduck_database.test", "transient", "false")},
		{name: "missing", err: stdsql.ErrNoRows, wantError: "MotherDuck database not found"},
		{name: "permission", err: errors.New("catalog access denied"), wantError: "catalog access denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &databaseLookupSQL{contractSQL: newContractSQL(), row: tc.row, err: tc.err}
			step := resource.TestStep{
				Config: contractProviderConfig("http://127.0.0.1") + `
data "motherduck_database" "test" {
 name = "warehouse's data"
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

type databaseLookupSQL struct {
	*contractSQL
	row []any
	err error
}

func (c *databaseLookupSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	const want = `SELECT uuid::VARCHAR, created_ts::VARCHAR, transient, historical_snapshot_retention::VARCHAR, type FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?`
	if query != want || len(args) != 1 || args[0] != "warehouse's data" {
		return contractRow{err: fmt.Errorf("unexpected exact-name lookup: %q %#v", query, args)}
	}
	return contractRow{values: c.row, err: c.err}
}

func (c *databaseLookupSQL) Exec(context.Context, string, ...any) error {
	return errors.New("database lookup must not execute mutations")
}
