package resources

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

type secretParserClient struct {
	providerctx.SQLClient
	db  *sql.DB
	err error
}

func (c *secretParserClient) Available() bool { return true }
func (c *secretParserClient) Exec(ctx context.Context, query string, args ...any) error {
	_, c.err = c.db.ExecContext(ctx, query, args...)
	return c.err
}

type secretDiagnosticState struct {
	model  secretModel
	writes int
}

func (s *secretDiagnosticState) Get(_ context.Context, dest any) diag.Diagnostics {
	*dest.(*secretModel) = s.model
	return nil
}

func (s *secretDiagnosticState) Set(context.Context, any) diag.Diagnostics {
	s.writes++
	return nil
}

func TestSecretWriteDiagnosticsDoNotExposeSQLValues(t *testing.T) {
	const marker = "AUDIT_NOT_A_REAL_SECRET"
	for _, replace := range []bool{false, true} {
		name := "create"
		if replace {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			db, err := sql.Open("duckdb", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			client := &secretParserClient{db: db}
			r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := &secretDiagnosticState{model: secretModel{
				Name:           types.StringValue("audit"),
				Type:           types.StringValue("s3"),
				SecretProvider: types.StringNull(),
				Params:         types.MapNull(types.StringType),
				SecretSQL:      types.StringValue("SECRET '" + marker + "' INVALID_OPTION 'bad'"),
			}}
			var diags diag.Diagnostics
			r.createSecret(t.Context(), state, state, replace, &diags)
			if client.err == nil || !strings.Contains(client.err.Error(), marker) {
				t.Fatal("expected the real DuckDB parser to echo the fake sensitive value")
			}
			if !diags.HasError() || state.writes != 0 {
				t.Fatal("failed secret write must return an error without committing state")
			}
			for _, d := range diags {
				if strings.Contains(d.Detail(), marker) || strings.Contains(d.Summary(), marker) {
					t.Fatal("secret write diagnostic exposed a sensitive SQL value")
				}
				if !strings.Contains(d.Detail(), "SQL syntax") {
					t.Fatal("secret write diagnostic lost the actionable parser error category")
				}
			}
		})
	}
}
