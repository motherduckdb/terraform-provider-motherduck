package resources

import (
	"errors"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

func TestIsDatabaseGone(t *testing.T) {
	cases := map[string]struct {
		err      error
		database string
		want     bool
	}{
		"binder missing catalog": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "tf_analytics" does not exist!`},
			database: "tf_analytics",
			want:     true,
		},
		"catalog missing database": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Catalog with name tf_analytics does not exist!"},
			database: "tf_analytics",
			want:     true,
		},
		"case-insensitive name": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "TF_Analytics" does not exist!`},
			database: "tf_analytics",
			want:     true,
		},
		"quoted name": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "my""db" does not exist!`},
			database: `my"db`,
			want:     true,
		},
		"different database": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "other" does not exist!`},
			database: "tf_analytics",
			want:     false,
		},
		"name prefix only": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "tf_analytics_old" does not exist!`},
			database: "tf_analytics",
			want:     false,
		},
		"permission error": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: `Permission Error: Catalog "tf_analytics" does not exist!`},
			database: "tf_analytics",
			want:     false,
		},
		"dependency error": {
			err:      &duckdb.Error{Type: duckdb.ErrorTypeDependency, Msg: "Dependency Error: Cannot drop entry because there are entries that depend on it"},
			database: "tf_analytics",
			want:     false,
		},
		"not a DuckDB error": {
			err:      errors.New(`Catalog "tf_analytics" does not exist!`),
			database: "tf_analytics",
			want:     false,
		},
		"nil": {database: "tf_analytics", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isDatabaseGone(tc.err, tc.database); got != tc.want {
				t.Fatalf("isDatabaseGone() = %t, want %t", got, tc.want)
			}
		})
	}
}

// Pulumi destroys a database and its children in parallel when they are not
// linked in state, so a child drop can bind after its database is gone.
func TestChildDeletesTolerateDroppedDatabase(t *testing.T) {
	ctx := t.Context()
	gone := &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "tf_gone" does not exist!`}
	other := &duckdb.Error{Type: duckdb.ErrorTypeBinder, Msg: `Binder Error: Catalog "other" does not exist!`}
	for _, tc := range []struct {
		name    string
		execErr error
		wantErr bool
	}{
		{"database gone", gone, false},
		{"other catalog", other, true},
	} {
		t.Run("schema "+tc.name, func(t *testing.T) {
			client := &scriptedSQLClient{execErr: tc.execErr}
			r := &schemaResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := emptyResourceState(ctx, t, r)
			model := schemaModel{ID: types.StringValue("tf_gone.app"), Database: types.StringValue("tf_gone"), Name: types.StringValue("app"), CascadeOnDelete: types.BoolNull()}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("delete diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
		})
		t.Run("table "+tc.name, func(t *testing.T) {
			client := &scriptedSQLClient{execErr: tc.execErr}
			r := &tableResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := emptyResourceState(ctx, t, r)
			model := tableModel{ID: types.StringValue("tf_gone.app.facts"), Database: types.StringValue("tf_gone"), Schema: types.StringValue("app"), Name: types.StringValue("facts"), Columns: types.MapNull(types.StringType)}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("delete diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}
