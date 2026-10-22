package resources

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestIsAlreadyExists(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		// Messages below are the ones MotherDuck returns for each object type.
		"database":      {&duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Failed to create database: database with name "tf_db" already exists`}, true},
		"schema":        {&duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Schema with name "s1" already exists!`}, true},
		"table":         {&duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Table with name "t1" already exists!`}, true},
		"share":         {&duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Failed to create share: share name is already taken!"}, true},
		"secret":        {&duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Failed to create secret: another secret with the same name already exists! DROP SECRET or CREATE OR REPLACE SECRET."}, true},
		"rest conflict": {&mdrest.APIError{StatusCode: http.StatusConflict, Code: "CONFLICT", Message: "conflict"}, true},
		"rest message":  {&mdrest.APIError{StatusCode: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "User svc already exists"}, true},
		"not found":     {&duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table with name t1 does not exist!"}, false},
		"rest other":    {&mdrest.APIError{StatusCode: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "invalid username"}, false},
		"plain error":   {errors.New("database already exists"), false},
		"nil":           {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isAlreadyExists(tc.err); got != tc.want {
				t.Fatalf("isAlreadyExists() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCreateErrorDetailSuggestsImport(t *testing.T) {
	err := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Table with name "t1" already exists!`}
	got := createErrorDetail(err, err.Error(), "table", "analytics.main.t1")
	if !strings.HasPrefix(got, err.Error()) {
		t.Fatalf("detail should keep the MotherDuck error first, got %q", got)
	}
	if !strings.Contains(got, `import it with the ID "analytics.main.t1"`) {
		t.Fatalf("detail should name the import ID, got %q", got)
	}

	other := &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: denied"}
	if got := createErrorDetail(other, other.Error(), "table", "analytics.main.t1"); got != other.Error() {
		t.Fatalf("detail for other errors should be unchanged, got %q", got)
	}
}

func TestSecretWriteDiagnosticAlreadyExists(t *testing.T) {
	err := &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Failed to create secret: another secret with the same name already exists! DROP SECRET or CREATE OR REPLACE SECRET."}
	got := secretWriteDiagnostic(err)
	if !strings.HasPrefix(got, "A MotherDuck secret with this name already exists.") {
		t.Fatalf("unexpected secret diagnostic %q", got)
	}
	if strings.Contains(got, "DROP SECRET") {
		t.Fatalf("secret diagnostic must not copy the raw error, got %q", got)
	}
}

func TestDropErrorDetails(t *testing.T) {
	schemaErr := &duckdb.Error{Type: duckdb.ErrorTypeDependency, Msg: "Dependency Error: Cannot drop entry \"s1\" because there are entries that depend on it.\ntable \"t1\" depends on schema \"s1\".\nUse DROP...CASCADE to drop all dependents."}
	if got := schemaDropErrorDetail(schemaErr); !strings.Contains(got, "cascade_on_delete = true") {
		t.Fatalf("schema drop detail should point to cascade_on_delete, got %q", got)
	}
	other := &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: denied"}
	if got := schemaDropErrorDetail(other); got != other.Error() {
		t.Fatalf("schema drop detail for other errors should be unchanged, got %q", got)
	}

	databaseErr := &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Failed to drop the database because there are still database shares linked to it. Use DROP DATABASE <db_name> CASCADE to drop the database and all associated shares."}
	if got := databaseDropErrorDetail(databaseErr); !strings.Contains(got, "Destroy the shares created from this database first") {
		t.Fatalf("database drop detail should explain linked shares, got %q", got)
	}
	if got := databaseDropErrorDetail(other); got != other.Error() {
		t.Fatalf("database drop detail for other errors should be unchanged, got %q", got)
	}
}

func TestAttachErrorDetail(t *testing.T) {
	missing := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: no database/share named "analytics" found`}
	if got := attachErrorDetail(missing, "analytics"); !strings.Contains(got, `The database "analytics" does not exist`) {
		t.Fatalf("attach detail should explain the missing database, got %q", got)
	}
	if got := attachErrorDetail(missing, "other"); got != missing.Error() {
		t.Fatalf("attach detail for another database should be unchanged, got %q", got)
	}
	denied := &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: denied"}
	if got := attachErrorDetail(denied, "analytics"); got != denied.Error() {
		t.Fatalf("attach detail for other errors should be unchanged, got %q", got)
	}
}

func TestWithoutSQLContext(t *testing.T) {
	msg := "Catalog Error: Type with name INTEGR does not exist!\nDid you mean \"JSON\"?\n\nLINE 1: SELECT typeof(CAST(NULL AS INTEGR))\n                                   ^"
	want := "Catalog Error: Type with name INTEGR does not exist!\nDid you mean \"JSON\"?"
	if got := withoutSQLContext(msg); got != want {
		t.Fatalf("withoutSQLContext() = %q, want %q", got, want)
	}
	if got := withoutSQLContext("plain error"); got != "plain error" {
		t.Fatalf("withoutSQLContext() changed a message without context: %q", got)
	}
}

func TestSchemaCreateErrorDetail(t *testing.T) {
	missing := &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Schema with name staging does not exist!"}
	if got := schemaCreateErrorDetail(missing, missing.Error(), "staging"); !strings.Contains(got, `The schema "staging" does not exist`) {
		t.Fatalf("detail should explain the missing schema, got %q", got)
	}
	// A view query can reference a different missing schema, which is a
	// query error rather than an ordering problem.
	if got := schemaCreateErrorDetail(missing, missing.Error(), "main"); got != missing.Error() {
		t.Fatalf("detail for another schema should be unchanged, got %q", got)
	}
}
