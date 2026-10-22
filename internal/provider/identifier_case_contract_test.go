//go:build contract

package provider

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
)

// caseCatalogSQL is an in-memory MotherDuck catalog that resolves identifiers
// the way MotherDuck and DuckDB do. The entity service resolves database names
// with a Unicode case-insensitive collation. DuckDB folds only ASCII letters
// for attached database aliases, schemas, and relations, so DDL rejects names
// that differ only in ASCII case while `Äpfel` and `äpfel` coexist. Catalog
// views report the attached alias and the spelling an object was created
// with, and compare it exactly unless the query uses lower(), which folds
// Unicode letters.
type caseCatalogSQL struct {
	mu          sync.Mutex
	calls       []string
	databases   map[string]string // lower(name) -> spelling
	aliases     map[string]string // asciiLower(alias) -> attached alias
	schemas     map[string]string // lower(db)/asciiLower(schema) -> spelling
	relations   map[string]*caseRelation
	shares      map[string]*caseShare
	snapshots   []*caseSnapshot
	shareGrants map[string]bool // lower(share)/type/lower(principal)
	roleGrants  map[string]bool // role/type/lower(principal)
}

type caseRelation struct {
	database, schema, name, kind, query string
	columns                             [][2]string
}

type caseShare struct{ name, source string }

type caseSnapshot struct{ id, database, name string }

func newCaseCatalogSQL(databases ...string) *caseCatalogSQL {
	c := &caseCatalogSQL{
		databases:   map[string]string{},
		aliases:     map[string]string{},
		schemas:     map[string]string{},
		relations:   map[string]*caseRelation{},
		shares:      map[string]*caseShare{},
		shareGrants: map[string]bool{},
		roleGrants:  map[string]bool{},
	}
	for _, database := range databases {
		c.databases[strings.ToLower(database)] = database
		c.aliases[caseASCIILower(database)] = database
		c.schemas[caseCatalogKey(database, "main")] = "main"
	}
	return c
}

// caseASCIILower folds only ASCII letters, as DuckDB catalogs do.
func caseASCIILower(value string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

// caseCatalogKey keys a schema or relation by its database, which MotherDuck
// resolves with Unicode case folding, and by names that DuckDB resolves with
// ASCII case folding.
func caseCatalogKey(database string, names ...string) string {
	return strings.ToLower(database) + "/" + caseASCIILower(strings.Join(names, "/"))
}

// caseAliasesFor returns the attached aliases of a database. Every alias that
// MotherDuck resolves to the same database reports the same objects.
func (c *caseCatalogSQL) caseAliasesFor(database string) []string {
	var aliases []string
	for _, alias := range c.aliases {
		if strings.EqualFold(alias, database) {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

var caseQuotedIdentifier = regexp.MustCompile(`"((?:[^"]|"")*)"`)

func caseIdentifiers(query string) []string {
	matches := caseQuotedIdentifier.FindAllStringSubmatch(query, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, strings.ReplaceAll(match[1], `""`, `"`))
	}
	return names
}

func caseKey(parts ...string) string {
	return strings.ToLower(strings.Join(parts, "/"))
}

// caseMatch compares a catalog column the way the query does. Only an
// explicit lower(column) = lower(?) filter ignores letter case, and DuckDB's
// lower() folds Unicode letters.
func caseMatch(query, column, stored string, arg any) bool {
	value, _ := arg.(string)
	if strings.Contains(query, "lower("+column+") = lower(?)") {
		return strings.ToLower(stored) == strings.ToLower(value)
	}
	return stored == value
}

func caseCatalogError(format string, args ...any) error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: " + fmt.Sprintf(format, args...)}
}

func (c *caseCatalogSQL) Available() bool { return true }
func (c *caseCatalogSQL) Close() error    { return nil }

func (c *caseCatalogSQL) record(method, query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, method+" "+query)
}

func (c *caseCatalogSQL) countPrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

func (c *caseCatalogSQL) AttachDatabase(_ context.Context, database string) error {
	c.record("attach", database)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.databases[strings.ToLower(database)]; !ok {
		return caseCatalogError("Database %s does not exist", database)
	}
	// ATTACH IF NOT EXISTS 'md:<name>' reuses an alias equal under ASCII
	// folding and otherwise attaches the database under the given name.
	if _, ok := c.aliases[caseASCIILower(database)]; !ok {
		c.aliases[caseASCIILower(database)] = database
	}
	return nil
}

func (c *caseCatalogSQL) WithDatabaseUse(ctx context.Context, database string, fn func(func(string, ...any) error) error) error {
	if err := c.AttachDatabase(ctx, database); err != nil {
		return err
	}
	return fn(func(query string, args ...any) error { return c.Exec(ctx, query, args...) })
}

func (c *caseCatalogSQL) Exec(_ context.Context, query string, args ...any) error {
	c.record("exec", query)
	if len(args) != 0 {
		return fmt.Errorf("unexpected exec args %#v", args)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := caseIdentifiers(query)
	needDatabase := func(name string) (string, error) {
		spelling, ok := c.databases[strings.ToLower(name)]
		if !ok {
			return "", caseCatalogError("Database %s does not exist", name)
		}
		return spelling, nil
	}
	needSchema := func(database, schema string) (string, string, error) {
		db, err := needDatabase(database)
		if err != nil {
			return "", "", err
		}
		spelling, ok := c.schemas[caseCatalogKey(database, schema)]
		if !ok {
			return "", "", caseCatalogError("Schema with name %s does not exist", schema)
		}
		return db, spelling, nil
	}
	switch {
	case query == "USE memory":
	case strings.HasPrefix(query, "CREATE DATABASE "):
		if _, ok := c.databases[strings.ToLower(ids[0])]; ok {
			return errors.New(`Binder Error: Failed to create database: database with name "` + ids[0] + `" already exists`)
		}
		c.databases[strings.ToLower(ids[0])] = ids[0]
		c.aliases[caseASCIILower(ids[0])] = ids[0]
		c.schemas[caseCatalogKey(ids[0], "main")] = "main"
	case strings.HasPrefix(query, "DROP DATABASE IF EXISTS "):
		delete(c.databases, strings.ToLower(ids[0]))
	case strings.HasPrefix(query, "CREATE SCHEMA "):
		if _, err := needDatabase(ids[0]); err != nil {
			return err
		}
		if _, ok := c.schemas[caseCatalogKey(ids[0], ids[1])]; ok {
			return caseCatalogError("Schema with name %q already exists!", ids[1])
		}
		c.schemas[caseCatalogKey(ids[0], ids[1])] = ids[1]
	case strings.HasPrefix(query, "DROP SCHEMA IF EXISTS "):
		delete(c.schemas, caseCatalogKey(ids[0], ids[1]))
	case strings.HasPrefix(query, "CREATE TABLE "):
		db, schema, err := needSchema(ids[0], ids[1])
		if err != nil {
			return err
		}
		key := caseCatalogKey(ids[0], ids[1], ids[2])
		if _, ok := c.relations[key]; ok {
			return caseCatalogError("Table with name %q already exists!", ids[2])
		}
		relation := &caseRelation{database: db, schema: schema, name: ids[2], kind: "BASE TABLE"}
		columnSQL := query[strings.Index(query, " (")+2 : len(query)-1]
		for _, column := range strings.Split(columnSQL, ", ") {
			name := caseIdentifiers(column)[0]
			relation.columns = append(relation.columns, [2]string{name, strings.TrimSpace(column[strings.LastIndex(column, `"`)+1:])})
		}
		c.relations[key] = relation
	case strings.HasPrefix(query, "CREATE VIEW "), strings.HasPrefix(query, "CREATE OR REPLACE VIEW "):
		db, schema, err := needSchema(ids[0], ids[1])
		if err != nil {
			return err
		}
		key := caseCatalogKey(ids[0], ids[1], ids[2])
		if _, ok := c.relations[key]; ok && strings.HasPrefix(query, "CREATE VIEW ") {
			return caseCatalogError("View with name %q already exists!", ids[2])
		}
		// CREATE OR REPLACE adopts the new spelling, as DuckDB does.
		c.relations[key] = &caseRelation{database: db, schema: schema, name: ids[2], kind: "VIEW", query: query[strings.Index(query, " AS ")+4:]}
	case strings.HasPrefix(query, "DROP TABLE IF EXISTS "), strings.HasPrefix(query, "DROP VIEW IF EXISTS "):
		delete(c.relations, caseCatalogKey(ids[0], ids[1], ids[2]))
	case strings.HasPrefix(query, "CREATE SHARE "):
		source, err := needDatabase(ids[1])
		if err != nil {
			return err
		}
		if _, ok := c.shares[strings.ToLower(ids[0])]; ok {
			return errors.New("Catalog Error: Failed to create share: share name is already taken!")
		}
		c.shares[strings.ToLower(ids[0])] = &caseShare{name: ids[0], source: source}
	case strings.HasPrefix(query, "DROP SHARE IF EXISTS "):
		delete(c.shares, strings.ToLower(ids[0]))
	case strings.HasPrefix(query, "CREATE SNAPSHOT "):
		db, err := needDatabase(ids[1])
		if err != nil {
			return err
		}
		c.snapshots = append(c.snapshots, &caseSnapshot{id: fmt.Sprintf("snapshot-%d", len(c.snapshots)+1), database: db, name: ids[0]})
	case strings.HasPrefix(query, "ALTER SNAPSHOT '"):
		id := strings.TrimPrefix(query, "ALTER SNAPSHOT '")
		id = id[:strings.Index(id, "'")]
		for _, snapshot := range c.snapshots {
			if snapshot.id == id && strings.HasSuffix(query, "SET snapshot_name = ''") {
				snapshot.name = ""
				return nil
			}
		}
		return errors.New("Invalid Input Error: snapshot not found")
	case strings.HasPrefix(query, "GRANT READ ON SHARE "), strings.HasPrefix(query, "REVOKE READ ON SHARE "):
		if _, ok := c.shares[strings.ToLower(ids[0])]; !ok {
			return caseCatalogError("Share %s does not exist", ids[0])
		}
		key := caseKey(ids[0], caseGranteeType(query), ids[1])
		c.shareGrants[key] = strings.HasPrefix(query, "GRANT")
	case strings.HasPrefix(query, "GRANT ROLE "), strings.HasPrefix(query, "REVOKE ROLE "):
		key := ids[0] + "/" + caseGranteeType(query) + "/" + strings.ToLower(ids[1])
		c.roleGrants[key] = strings.HasPrefix(query, "GRANT")
	default:
		return fmt.Errorf("unexpected SQL exec %q", query)
	}
	return nil
}

var caseGranteeTypePattern = regexp.MustCompile(` (?:TO|FROM) (USER|ROLE) "`)

func caseGranteeType(query string) string {
	match := caseGranteeTypePattern.FindStringSubmatch(query)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

func (c *caseCatalogSQL) Exists(_ context.Context, query string, args ...any) (bool, error) {
	c.record("exists", query)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(query, "duckdb_functions()"):
		return true, nil
	case strings.Contains(query, "MD_LIST_SHARE_GRANTEES"):
		share, _ := args[0].(string)
		if _, ok := c.shares[strings.ToLower(share)]; !ok {
			return false, caseCatalogError("Share %s does not exist", share)
		}
		principal, _ := args[1].(string)
		granteeType, _ := args[2].(string)
		return c.shareGrants[caseKey(share, granteeType, principal)], nil
	}
	return false, fmt.Errorf("unexpected SQL exists query %q", query)
}

type caseSnapshotRow struct {
	id, created string
	matches     int
}

func (r caseSnapshotRow) Scan(dest ...any) error {
	if len(dest) != 3 {
		return fmt.Errorf("snapshot scan destinations = %d, want 3", len(dest))
	}
	if err := (contractRow{values: []any{r.id, r.created}}).Scan(dest[:2]...); err != nil {
		return err
	}
	*dest[2].(*int) = r.matches
	return nil
}

func (c *caseCatalogSQL) QueryRow(_ context.Context, query string, args ...any) mdsql.RowScanner {
	c.record("query-row", query)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.Contains(query, "MD_INFORMATION_SCHEMA.DATABASES WHERE") && strings.Contains(query, "name"):
		for _, database := range c.databases {
			if caseMatch(query, "name", database, args[0]) {
				return contractRow{values: []any{"00000000-0000-0000-0000-000000000001", "2026-10-01T00:00:00Z", false, nil, "DEFAULT"}}
			}
		}
		return contractRow{err: stdsql.ErrNoRows}
	case strings.Contains(query, "MD_INFORMATION_SCHEMA.OWNED_SHARES"):
		for _, share := range c.shares {
			if caseMatch(query, "name", share.name, args[0]) {
				return contractRow{values: []any{"md:_share/" + strings.ToLower(share.name), share.source, "ORGANIZATION", "DISCOVERABLE", "AUTOMATIC", nil, "2026-10-01T00:00:00Z", "00000000-0000-0000-0000-0000000000a1"}}
			}
		}
		return contractRow{err: stdsql.ErrNoRows}
	case strings.Contains(query, "DATABASE_SNAPSHOTS") && strings.Contains(query, "snapshot_id::VARCHAR = ?"):
		for _, snapshot := range c.snapshots {
			if caseMatch(query, "database_name", snapshot.database, args[0]) && snapshot.id == args[1] {
				return contractRow{values: []any{snapshot.name, "2026-10-01T00:00:00Z"}}
			}
		}
		return contractRow{err: stdsql.ErrNoRows}
	case strings.Contains(query, "DATABASE_SNAPSHOTS") && strings.Contains(query, "snapshot_name = ?"):
		var found []*caseSnapshot
		for _, snapshot := range c.snapshots {
			if caseMatch(query, "database_name", snapshot.database, args[0]) && snapshot.name == args[1] {
				found = append(found, snapshot)
			}
		}
		if len(found) == 0 {
			return contractRow{err: stdsql.ErrNoRows}
		}
		return caseSnapshotRow{id: found[0].id, created: "2026-10-01T00:00:00Z", matches: len(found)}
	}
	return contractRow{err: fmt.Errorf("unexpected SQL row query %q", query)}
}

func (c *caseCatalogSQL) QueryRowsJSON(_ context.Context, query string, args ...any) (string, error) {
	c.record("query-json", query)
	c.mu.Lock()
	defer c.mu.Unlock()
	if rows, ok := c.catalogRows(query, args); ok {
		encoded, err := json.Marshal(rows)
		return string(encoded), err
	}
	rows := []map[string]any{}
	switch {
	case strings.HasPrefix(query, "SHOW ROLES TO "):
		granteeType := strings.ToLower(strings.Fields(query)[3])
		principal := strings.ToLower(caseIdentifiers(query)[0])
		keys := make([]string, 0, len(c.roleGrants))
		for key, granted := range c.roleGrants {
			if granted && strings.HasSuffix(key, "/"+granteeType+"/"+principal) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, map[string]any{"role_name": key[:strings.Index(key, "/")], "is_direct": true, "granted_at": "2026-10-01T00:00:00Z"})
		}
	default:
		return "", fmt.Errorf("unexpected SQL JSON query %q", query)
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

// catalogRows answers an information_schema query, or reports false for any
// other query. The caller holds c.mu.
func (c *caseCatalogSQL) catalogRows(query string, args []any) ([]map[string]any, bool) {
	rows := []map[string]any{}
	switch {
	case strings.Contains(query, "information_schema.schemata"):
		keys := make([]string, 0, len(c.schemas))
		for key := range c.schemas {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			schema := c.schemas[key]
			for _, alias := range c.caseAliasesFor(c.databases[key[:strings.Index(key, "/")]]) {
				if caseMatch(query, "catalog_name", alias, args[0]) && caseMatch(query, "schema_name", schema, args[1]) {
					rows = append(rows, map[string]any{"catalog_name": alias, "schema_name": schema})
				}
			}
		}
	case strings.Contains(query, "information_schema.tables"), strings.Contains(query, "information_schema.views"), strings.Contains(query, "information_schema.columns"):
		for _, relation := range c.sortedRelations() {
			for _, alias := range c.caseAliasesFor(relation.database) {
				if !caseMatch(query, "table_catalog", alias, args[0]) || !caseMatch(query, "table_schema", relation.schema, args[1]) || !caseMatch(query, "table_name", relation.name, args[2]) {
					continue
				}
				row := map[string]any{"table_catalog": alias, "table_schema": relation.schema, "table_name": relation.name}
				switch {
				case strings.Contains(query, "information_schema.tables"):
					if relation.kind == args[3] {
						rows = append(rows, row)
					}
				case strings.Contains(query, "information_schema.views"):
					if relation.kind == "VIEW" {
						row["view_definition"] = "CREATE VIEW " + relation.schema + "." + relation.name + " AS " + relation.query + ";"
						rows = append(rows, row)
					}
				default:
					for _, column := range relation.columns {
						columnRow := map[string]any{"column_name": column[0], "data_type": column[1]}
						for key, value := range row {
							columnRow[key] = value
						}
						rows = append(rows, columnRow)
					}
				}
			}
		}
	default:
		return nil, false
	}
	return rows, true
}

func (c *caseCatalogSQL) sortedRelations() []*caseRelation {
	keys := make([]string, 0, len(c.relations))
	for key := range c.relations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	relations := make([]*caseRelation, 0, len(keys))
	for _, key := range keys {
		relations = append(relations, c.relations[key])
	}
	return relations
}

func (c *caseCatalogSQL) ScalarString(_ context.Context, query string, _ ...any) (string, error) {
	c.record("scalar", query)
	switch query {
	case "SELECT current_database()":
		return "memory", nil
	case "SELECT typeof(CAST(NULL AS INT))", "SELECT typeof(CAST(NULL AS INTEGER))":
		return "INTEGER", nil
	}
	return "", fmt.Errorf("unexpected SQL scalar query %q", query)
}

// CREATE SNAPSHOT resolves the database case-insensitively, but
// DATABASE_SNAPSHOTS reports the spelling the database was created with. The
// readback must still find the snapshot, and state keeps the configured
// spelling. A later case-only edit updates state without a new snapshot.
func TestContractSnapshotDatabaseCaseInsensitive(t *testing.T) {
	client := newCaseCatalogSQL("Analytics")
	config := func(database string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_snapshot" "test" {
  database = %q
  name     = "contract_snapshot"
}
`, database)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.countPrefix("exec CREATE SNAPSHOT"); got != 1 {
				return fmt.Errorf("snapshot creates = %d, want 1", got)
			}
			if got := client.countPrefix("exec ALTER SNAPSHOT 'snapshot-1' SET snapshot_name = ''"); got != 1 {
				return fmt.Errorf("snapshot unnames = %d, want 1", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("analytics"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_snapshot.test", "database", "analytics"),
					resource.TestCheckResourceAttr("motherduck_snapshot.test", "id", "snapshot-1"),
				),
			},
			{
				Config: config("ANALYTICS"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_snapshot.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_snapshot.test", "database", "ANALYTICS"),
					resource.TestCheckResourceAttr("motherduck_snapshot.test", "id", "snapshot-1"),
				),
			},
		},
	})
}

// OWNED_SHARES reports the source database with its created spelling. The
// share keeps the configured spelling, and case-only edits of the share name
// or source database update state instead of replacing the share and
// changing its URL.
func TestContractShareIdentifierCaseInsensitive(t *testing.T) {
	client := newCaseCatalogSQL("Analytics")
	config := func(name, source string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_share" "test" {
  name            = %q
  source_database = %q
}
`, name, source)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.countPrefix("exec CREATE SHARE"); got != 1 {
				return fmt.Errorf("share creates = %d, want 1", got)
			}
			if len(client.shares) != 0 {
				return errors.New("share still exists after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("contract_share", "analytics"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_share.test", "source_database", "analytics"),
			},
			{
				Config: config("Contract_Share", "ANALYTICS"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_share.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_share.test", "name", "Contract_Share"),
					resource.TestCheckResourceAttr("motherduck_share.test", "id", "Contract_Share"),
					resource.TestCheckResourceAttr("motherduck_share.test", "source_database", "ANALYTICS"),
					resource.TestCheckResourceAttr("motherduck_share.test", "url", "md:_share/contract_share"),
				),
			},
		},
	})
}

// information_schema reports the created spelling of the database, schema,
// and relation. Schemas, tables, and views whose configuration spells a
// parent differently must stay in state, and case-only edits of any
// identifier update state without dropping the object.
func TestContractRelationIdentifierCaseInsensitive(t *testing.T) {
	client := newCaseCatalogSQL("Analytics")
	config := func(database, schema, table, view string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_schema" "test" {
  database = %[1]q
  name     = %[2]q
}

resource "motherduck_table" "test" {
  database = upper(%[1]q)
  schema   = lower(%[2]q)
  name     = %[3]q
  columns  = { id = "INTEGER" }

  depends_on = [motherduck_schema.test]
}

resource "motherduck_view" "test" {
  database = lower(%[1]q)
  schema   = upper(%[2]q)
  name     = %[4]q
  query    = "SELECT 1 AS x"

  depends_on = [motherduck_schema.test]
}
`, database, schema, table, view)
	}
	resources := []string{"motherduck_schema.test", "motherduck_table.test", "motherduck_view.test"}
	updates := make([]plancheck.PlanCheck, 0, len(resources))
	for _, name := range resources {
		updates = append(updates, plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate))
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			for prefix, want := range map[string]int{
				"exec CREATE SCHEMA": 1, "exec CREATE TABLE": 1, "exec CREATE VIEW": 1, "exec CREATE OR REPLACE VIEW": 0,
				"exec DROP SCHEMA": 1, "exec DROP TABLE": 1, "exec DROP VIEW": 1,
			} {
				if got := client.countPrefix(prefix); got != want {
					return fmt.Errorf("%q calls = %d, want %d", prefix, got, want)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("analytics", "App", "Facts", "Recent"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_schema.test", "id", "analytics.App"),
					resource.TestCheckResourceAttr("motherduck_table.test", "id", "ANALYTICS.app.Facts"),
					resource.TestCheckResourceAttr("motherduck_view.test", "id", "analytics.APP.Recent"),
					resource.TestCheckResourceAttr("motherduck_view.test", "query", "SELECT 1 AS x"),
				),
			},
			{
				Config: config("Analytics", "app", "facts", "RECENT"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             updates,
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_schema.test", "id", "Analytics.app"),
					resource.TestCheckResourceAttr("motherduck_table.test", "id", "ANALYTICS.app.facts"),
					resource.TestCheckResourceAttr("motherduck_view.test", "id", "analytics.APP.RECENT"),
				),
			},
		},
	})
}

// seedRelation adds an object that Terraform does not manage.
func (c *caseCatalogSQL) seedRelation(relation *caseRelation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.relations[caseCatalogKey(relation.database, relation.schema, relation.name)] = relation
}

func (c *caseCatalogSQL) relationNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.relations))
	for _, relation := range c.sortedRelations() {
		names = append(names, relation.schema+"."+relation.name)
	}
	sort.Strings(names)
	return names
}

func (c *caseCatalogSQL) schemaNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.schemas))
	for _, schema := range c.schemas {
		names = append(names, schema)
	}
	sort.Strings(names)
	return names
}

// DuckDB folds only ASCII letters in schema, table, and view names, so
// `Äpfel` and `äpfel` are different tables. A rename that changes only the
// case of a non-ASCII letter must replace the object. Updating state in place
// would leave the old object behind and drop a different one on destroy.
func TestContractRelationNonASCIICaseRenameReplaces(t *testing.T) {
	client := newCaseCatalogSQL("Analytics")
	config := func(schema, table, view string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_schema" "test" {
  database = "Analytics"
  name     = %[1]q
}

resource "motherduck_table" "test" {
  database = "Analytics"
  schema   = "main"
  name     = %[2]q
  columns  = { id = "INTEGER" }
}

resource "motherduck_view" "test" {
  database = "Analytics"
  schema   = "main"
  name     = %[3]q
  query    = "SELECT 1 AS x"
}
`, schema, table, view)
	}
	replaces := []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("motherduck_schema.test", plancheck.ResourceActionReplace),
		plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionReplace),
		plancheck.ExpectResourceAction("motherduck_view.test", plancheck.ResourceActionReplace),
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			for prefix, want := range map[string]int{
				"exec CREATE SCHEMA": 2, "exec CREATE TABLE": 2, "exec CREATE VIEW": 2,
				"exec DROP SCHEMA": 2, "exec DROP TABLE": 2, "exec DROP VIEW": 2,
			} {
				if got := client.countPrefix(prefix); got != want {
					return fmt.Errorf("%q calls = %d, want %d", prefix, got, want)
				}
			}
			if names := client.relationNames(); len(names) != 0 {
				return fmt.Errorf("relations left after destroy: %v", names)
			}
			if names := client.schemaNames(); len(names) != 1 || names[0] != "main" {
				return fmt.Errorf("schemas left after destroy: %v", names)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("Ärger", "Äpfel", "Öl"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: config("ärger", "äpfel", "öl"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             replaces,
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					if got, want := strings.Join(client.relationNames(), ","), "main.äpfel,main.öl"; got != want {
						return fmt.Errorf("relations = %s, want %s", got, want)
					}
					if got, want := strings.Join(client.schemaNames(), ","), "main,ärger"; got != want {
						return fmt.Errorf("schemas = %s, want %s", got, want)
					}
					return nil
				},
			},
		},
	})
}

// Catalog lookups fetch candidates with lower(), which folds Unicode letters,
// so they must not mistake an unmanaged `Äpfel` for the managed `äpfel`.
// Refresh reads the managed table's own columns, and when the managed objects
// are dropped outside Terraform, refresh notices and plans to create them.
func TestContractRelationLookupIgnoresNonASCIICaseVariant(t *testing.T) {
	client := newCaseCatalogSQL("Analytics")
	client.seedRelation(&caseRelation{database: "Analytics", schema: "main", name: "Äpfel", kind: "BASE TABLE", columns: [][2]string{{"label", "VARCHAR"}}})
	client.seedRelation(&caseRelation{database: "Analytics", schema: "main", name: "Öl", kind: "VIEW", query: "SELECT 2 AS y"})
	config := contractProviderConfig("http://127.0.0.1") + `
resource "motherduck_table" "test" {
  database = "Analytics"
  schema   = "main"
  name     = "äpfel"
  columns  = { id = "INTEGER" }
}

resource "motherduck_view" "test" {
  database = "Analytics"
  schema   = "main"
  name     = "öl"
  query    = "SELECT 1 AS x"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got, want := strings.Join(client.relationNames(), ","), "main.Äpfel,main.Öl"; got != want {
				return fmt.Errorf("relations after destroy = %s, want only the unmanaged %s", got, want)
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
					resource.TestCheckResourceAttr("motherduck_table.test", "columns.%", "1"),
					resource.TestCheckResourceAttr("motherduck_table.test", "columns.id", "INTEGER"),
					resource.TestCheckResourceAttr("motherduck_view.test", "query", "SELECT 1 AS x"),
				),
			},
			{
				PreConfig: func() {
					client.mu.Lock()
					defer client.mu.Unlock()
					delete(client.relations, caseCatalogKey("Analytics", "main", "äpfel"))
					delete(client.relations, caseCatalogKey("Analytics", "main", "öl"))
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction("motherduck_view.test", plancheck.ResourceActionCreate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// MotherDuck resolves database names with a Unicode case-insensitive
// collation, so `Bäck` and `BÄCK` name the same database. A change of the
// parent database that differs only in letter case, including non-ASCII
// letters, updates state in place instead of dropping the table.
func TestContractRelationDatabaseNonASCIICaseChangeUpdatesInPlace(t *testing.T) {
	client := newCaseCatalogSQL("Bäck")
	config := func(database string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_table" "test" {
  database = %q
  schema   = "main"
  name     = "facts"
  columns  = { id = "INTEGER" }
}
`, database)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.countPrefix("exec CREATE TABLE"); got != 1 {
				return fmt.Errorf("table creates = %d, want 1", got)
			}
			if got := client.countPrefix("exec DROP TABLE"); got != 1 {
				return fmt.Errorf("table drops = %d, want 1 on destroy", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("Bäck")},
			{
				Config: config("BÄCK"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_table.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("motherduck_table.test", "id", "BÄCK.main.facts"),
			},
		},
	})
}

// A case-only rename of a database names the same database, so it must not
// plan DROP DATABASE.
func TestContractDatabaseNameCaseOnlyChangeUpdatesInPlace(t *testing.T) {
	client := newCaseCatalogSQL()
	config := func(name string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_database" "test" {
  name = %q
}
`, name)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			if got := client.countPrefix("exec CREATE DATABASE"); got != 1 {
				return fmt.Errorf("database creates = %d, want 1", got)
			}
			if got := client.countPrefix("exec DROP DATABASE"); got != 1 {
				return fmt.Errorf("database drops = %d, want 1 on destroy", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("contract_db")},
			{
				Config: config("Contract_DB"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction("motherduck_database.test", plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_database.test", "name", "Contract_DB"),
					resource.TestCheckResourceAttr("motherduck_database.test", "id", "Contract_DB"),
				),
			},
		},
	})
}

// MotherDuck resolves share and role grant principals case-insensitively. A
// case-only edit must not replace the grant, because create_before_destroy
// would grant and then revoke the same principal.
func TestContractGrantPrincipalCaseOnlyChangeUpdatesInPlace(t *testing.T) {
	client := newCaseCatalogSQL("analytics")
	config := func(share, principal string) string {
		return contractProviderConfig("http://127.0.0.1") + fmt.Sprintf(`
resource "motherduck_share" "test" {
  name            = "contract_share"
  source_database = "analytics"
}

resource "motherduck_share_grant" "test" {
  share    = %[1]q
  username = %[2]q

  depends_on = [motherduck_share.test]

  lifecycle {
    create_before_destroy = true
  }
}

resource "motherduck_role_grant" "test" {
  role_name    = "contract_readers"
  grantee_name = %[2]q

  lifecycle {
    create_before_destroy = true
  }
}
`, share, principal)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: contractProviderFactories(client),
		CheckDestroy: func(*terraform.State) error {
			for prefix, want := range map[string]int{
				"exec GRANT READ ON SHARE": 1, "exec REVOKE READ ON SHARE": 1,
				"exec GRANT ROLE": 1, "exec REVOKE ROLE": 1,
			} {
				if got := client.countPrefix(prefix); got != want {
					return fmt.Errorf("%q calls = %d, want %d", prefix, got, want)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("contract_share", "Alice@Example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: config("CONTRACT_SHARE", "alice@example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("motherduck_share_grant.test", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("motherduck_role_grant.test", plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("motherduck_share_grant.test", "id", "CONTRACT_SHARE/user/alice@example.com"),
					resource.TestCheckResourceAttr("motherduck_role_grant.test", "id", "contract_readers/user/alice@example.com"),
				),
			},
		},
	})
}
