package datasources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
)

func TestRESTInventoryEmptyValuesAndDiagnostics(t *testing.T) {
	cases := []struct {
		name   string
		source datasource.DataSource
		field  string
		values func(*diag.Diagnostics) types.List
	}{
		{"nil tokens", NewUserTokensDataSource(), "tokens", func(d *diag.Diagnostics) types.List { return userTokensListValue(nil, d) }},
		{"empty tokens", NewUserTokensDataSource(), "tokens", func(d *diag.Diagnostics) types.List { return userTokensListValue([]mdrest.Token{}, d) }},
		{"nil users", NewUsersDataSource(), "users", func(d *diag.Diagnostics) types.List { return usersListValue(nil, d) }},
		{"empty users", NewUsersDataSource(), "users", func(d *diag.Diagnostics) types.List { return usersListValue([]mdrest.User{}, d) }},
		{"nil accounts", NewActiveAccountsDataSource(), "accounts", func(d *diag.Diagnostics) types.List { return activeAccountsListValue(nil, d) }},
		{"empty accounts", NewActiveAccountsDataSource(), "accounts", func(d *diag.Diagnostics) types.List {
			return activeAccountsListValue([]mdrest.ActiveAccount{}, d)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var response datasource.SchemaResponse
			tc.source.Schema(context.Background(), datasource.SchemaRequest{}, &response)
			for _, withError := range []bool{false, true} {
				var d diag.Diagnostics
				d.AddWarning("existing warning", "keep warning detail")
				if withError {
					d.AddError("existing error", "keep error detail")
				}
				wantDiagnostics := append(diag.Diagnostics(nil), d...)
				got := tc.values(&d)
				if !reflect.DeepEqual(d, wantDiagnostics) {
					t.Fatalf("diagnostics changed: %v", d)
				}
				if !got.Type(context.Background()).Equal(response.Schema.Attributes[tc.field].GetType()) {
					t.Fatalf("value type %v differs from advertised schema", got.Type(context.Background()))
				}
				if got.IsNull() != withError || got.IsUnknown() || len(got.Elements()) != 0 {
					t.Fatalf("value with prior error %t = %v", withError, got)
				}
			}
		})
	}
}

func TestUserTokenValuesPreserveFieldSemanticsAndOrder(t *testing.T) {
	tokens := []mdrest.Token{
		{ID: "z", Token: "never expose this", Name: "", Description: "", ExpireAt: "", CreatedTS: "", TokenType: ""},
		{ID: "a", Name: "label", Description: "purpose", ExpireAt: "expiry", CreatedTS: "created", ReadOnly: true, TokenType: "read_only"},
	}
	original := append([]mdrest.Token(nil), tokens...)
	var d diag.Diagnostics
	got := userTokensListValue(tokens, &d)
	if d.HasError() {
		t.Fatal(d)
	}
	want := []map[string]attr.Value{
		{"id": types.StringValue("z"), "name": types.StringNull(), "description": types.StringNull(), "expire_at": types.StringNull(), "created_ts": types.StringValue(""), "read_only": types.BoolValue(false), "token_type": types.StringValue("")},
		{"id": types.StringValue("a"), "name": types.StringValue("label"), "description": types.StringValue("purpose"), "expire_at": types.StringValue("expiry"), "created_ts": types.StringValue("created"), "read_only": types.BoolValue(true), "token_type": types.StringValue("read_only")},
	}
	assertInventoryRows(t, got, want)
	if !reflect.DeepEqual(tokens, original) {
		t.Fatal("conversion mutated caller tokens")
	}
}

func TestUserValuesPreserveFieldSemanticsAndCallerOwnership(t *testing.T) {
	users := []mdrest.User{
		{ID: "z", Username: "same", Roles: []string{"z", "a", "a"}, IsServiceAccount: true},
		{ID: "a", Username: "same", FirstName: "Ada", LastName: "Lovelace", Email: "ada@example.com", Roles: nil, IsDeprovisioned: true},
		{ID: "b", Username: "before", Roles: []string{}},
	}
	original := append([]mdrest.User(nil), users...)
	original[0].Roles = append([]string(nil), users[0].Roles...)
	var d diag.Diagnostics
	got := usersListValue(users, &d)
	if d.HasError() {
		t.Fatal(d)
	}
	emptyRoles := types.ListValueMust(types.StringType, []attr.Value{})
	want := []map[string]attr.Value{
		{"id": types.StringValue("b"), "username": types.StringValue("before"), "email": types.StringValue(""), "first_name": types.StringNull(), "last_name": types.StringNull(), "is_service_account": types.BoolValue(false), "is_deprovisioned": types.BoolValue(false), "roles": emptyRoles},
		{"id": types.StringValue("a"), "username": types.StringValue("same"), "email": types.StringValue("ada@example.com"), "first_name": types.StringValue("Ada"), "last_name": types.StringValue("Lovelace"), "is_service_account": types.BoolValue(false), "is_deprovisioned": types.BoolValue(true), "roles": emptyRoles},
		{"id": types.StringValue("z"), "username": types.StringValue("same"), "email": types.StringValue(""), "first_name": types.StringNull(), "last_name": types.StringNull(), "is_service_account": types.BoolValue(true), "is_deprovisioned": types.BoolValue(false), "roles": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("a"), types.StringValue("a"), types.StringValue("z")})},
	}
	assertInventoryRows(t, got, want)
	if !reflect.DeepEqual(users, original) {
		t.Fatal("conversion mutated caller users or role slices")
	}
}

func TestActiveAccountValuesSortAndPreserveCallerOwnership(t *testing.T) {
	accounts := []mdrest.ActiveAccount{
		{Username: "svc_b", Ducklings: []mdrest.Duckling{{ID: "rw", Type: "read_write", Status: "active"}, {ID: "rs.1", Type: "read_scaling", Status: "cooldown"}}},
		{Username: "svc_a", Ducklings: nil},
	}
	original := []mdrest.ActiveAccount{accounts[0], accounts[1]}
	original[0].Ducklings = append([]mdrest.Duckling(nil), accounts[0].Ducklings...)
	var d diag.Diagnostics
	got := activeAccountsListValue(accounts, &d)
	if d.HasError() {
		t.Fatal(d)
	}
	ducklingType := activeAccountsAttribute().NestedObject.Type().(types.ObjectType).AttrTypes["ducklings"].(types.ListType).ElemType.(types.ObjectType)
	duckling := func(id, kind, status string) attr.Value {
		return types.ObjectValueMust(ducklingType.AttrTypes, map[string]attr.Value{"id": types.StringValue(id), "type": types.StringValue(kind), "status": types.StringValue(status)})
	}
	want := []map[string]attr.Value{
		{"username": types.StringValue("svc_a"), "ducklings": types.ListValueMust(ducklingType, []attr.Value{})},
		{"username": types.StringValue("svc_b"), "ducklings": types.ListValueMust(ducklingType, []attr.Value{
			duckling("rs.1", "read_scaling", "cooldown"),
			duckling("rw", "read_write", "active"),
		})},
	}
	assertInventoryRows(t, got, want)
	if !reflect.DeepEqual(accounts, original) {
		t.Fatal("conversion mutated caller accounts or duckling slices")
	}
}

func assertInventoryRows(t *testing.T, got types.List, want []map[string]attr.Value) {
	t.Helper()
	rows := got.Elements()
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %d", got, len(want))
	}
	for i, row := range rows {
		fields := row.(types.Object).Attributes()
		if len(fields) != len(want[i]) {
			t.Fatalf("row %d fields = %v", i, fields)
		}
		for name, expected := range want[i] {
			if fields[name] == nil || !fields[name].Equal(expected) {
				t.Errorf("row %d field %s = %v, want %v", i, name, fields[name], expected)
			}
		}
	}
}
