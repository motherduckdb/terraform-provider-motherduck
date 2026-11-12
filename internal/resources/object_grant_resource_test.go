package resources

import (
	"errors"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

const testGrantObjectID = "22222222-2222-4222-8222-222222222222"

func grantResource(kind objectGrantKind, client *scriptedAppSQL) *objectGrantResource {
	return &objectGrantResource{kind: kind, baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
}

func grantModel(granteeType string, granteeName types.String) objectGrantModel {
	return objectGrantModel{
		ObjectID:    types.StringValue(testGrantObjectID),
		GranteeType: types.StringValue(granteeType),
		GranteeName: granteeName,
	}
}

// grantState is a state or plan value for a grant, as Terraform would send it.
func grantState(t *testing.T, res *objectGrantResource, model objectGrantModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, res)}
	state.Raw = tftypes.NewValue(state.Schema.Type().TerraformType(t.Context()), nil)
	var diags diag.Diagnostics
	res.store(t.Context(), &state, model, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func grantGoneError(label string) error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Could not find " + label + " with id '" + testGrantObjectID + "'"}
}

func TestObjectGrantStatements(t *testing.T) {
	tests := map[string]struct {
		kind  objectGrantKind
		model objectGrantModel
		grant string
	}{
		"dive user":   {diveGrantKind, grantModel("user", types.StringValue("alice")), `GRANT READ ON DIVE '` + testGrantObjectID + `' TO USER "alice"`},
		"dive role":   {diveGrantKind, grantModel("role", types.StringValue("finance")), `GRANT READ ON DIVE '` + testGrantObjectID + `' TO ROLE "finance"`},
		"dive org":    {diveGrantKind, grantModel("organization", types.StringNull()), `GRANT READ ON DIVE '` + testGrantObjectID + `' TO ORGANIZATION`},
		"flight user": {flightGrantKind, grantModel("user", types.StringValue(`a"b`)), `GRANT RUN ON FLIGHT '` + testGrantObjectID + `' TO USER "a""b"`},
		"flight org":  {flightGrantKind, grantModel("organization", types.StringNull()), `GRANT RUN ON FLIGHT '` + testGrantObjectID + `' TO ORGANIZATION`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := grantResource(tc.kind, &scriptedAppSQL{})
			if got := res.statement("GRANT", "TO", tc.model); got != tc.grant {
				t.Fatalf("grant = %s, want %s", got, tc.grant)
			}
			wantRevoke := strings.Replace(strings.Replace(tc.grant, "GRANT", "REVOKE", 1), " TO ", " FROM ", 1)
			if got := res.statement("REVOKE", "FROM", tc.model); got != wantRevoke {
				t.Fatalf("revoke = %s, want %s", got, wantRevoke)
			}
		})
	}
}

func TestObjectGrantID(t *testing.T) {
	if got := objectGrantID(grantModel("role", types.StringValue("finance"))).ValueString(); got != testGrantObjectID+"/role/finance" {
		t.Fatalf("role grant ID = %s", got)
	}
	if got := objectGrantID(grantModel("organization", types.StringNull())).ValueString(); got != testGrantObjectID+"/organization" {
		t.Fatalf("organization grant ID = %s", got)
	}
}

func TestObjectGrantListed(t *testing.T) {
	rows := []map[string]any{
		{"grantee_name": "Acme Inc", "grantee_type": "organization", "privilege": "read"},
		{"grantee_name": "finance", "grantee_type": "role", "privilege": "read"},
		{"grantee_name": "alice", "grantee_type": "user", "privilege": "write"},
	}
	tests := map[string]struct {
		model objectGrantModel
		want  bool
	}{
		"organization matches on type alone": {grantModel("organization", types.StringNull()), true},
		"role matches ignoring case":         {grantModel("role", types.StringValue("Finance")), true},
		"same name with another type":        {grantModel("user", types.StringValue("finance")), false},
		"another privilege is not the grant": {grantModel("user", types.StringValue("alice")), false},
		"unlisted principal":                 {grantModel("user", types.StringValue("bob")), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := objectGrantListed(rows, tc.model, "read"); got != tc.want {
				t.Fatalf("listed = %t, want %t", got, tc.want)
			}
		})
	}
}

// The messages are the ones production MotherDuck returns for a deleted Dive
// or Flight from the grant statements and the grantee listings.
func TestObjectGrantGone(t *testing.T) {
	catalog := func(msg string) error { return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: msg} }
	tests := map[string]struct {
		kind objectGrantKind
		err  error
		want bool
	}{
		"dive listing":            {diveGrantKind, grantGoneError("Dive"), true},
		"dive statement":          {diveGrantKind, catalog("Catalog Error: Could not find Dive"), true},
		"flight listing":          {flightGrantKind, grantGoneError("Flight"), true},
		"flight statement":        {flightGrantKind, catalog("Catalog Error: Could not find Flight"), true},
		"flight error on a dive":  {diveGrantKind, catalog("Catalog Error: Could not find Flight"), false},
		"missing role":            {diveGrantKind, catalog("Catalog Error: Unable to find role finance"), false},
		"missing function":        {flightGrantKind, catalog("Catalog Error: Table Function with name md_list_flight_grantees does not exist!"), false},
		"untyped error":           {diveGrantKind, errors.New("Could not find Dive"), false},
		"permission error":        {flightGrantKind, &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: Only the owner of this Flight can add grants"}, false},
		"nil is not a gone error": {diveGrantKind, nil, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tc.kind.gone(tc.err); got != tc.want {
				t.Fatalf("gone = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestObjectGranteeProblem(t *testing.T) {
	if got := objectGranteeProblem("organization", types.StringNull()); got != "" {
		t.Fatalf("organization without a name: %q", got)
	}
	if got := objectGranteeProblem("organization", types.StringValue("acme")); !strings.Contains(got, "Omit `grantee_name`") {
		t.Fatalf("organization with a name: %q", got)
	}
	if got := objectGranteeProblem("role", types.StringNull()); !strings.Contains(got, "Set `grantee_name` to the role") {
		t.Fatalf("role without a name: %q", got)
	}
	if got := objectGranteeProblem("user", types.StringValue("alice")); got != "" {
		t.Fatalf("user with a name: %q", got)
	}
}

func TestObjectGrantCreateGrantsAndStoresID(t *testing.T) {
	client := &scriptedAppSQL{exec: func(string) error { return nil }}
	res := grantResource(flightGrantKind, client)
	planState := grantState(t, res, grantModel("role", types.StringValue("operators")))
	resp := resource.CreateResponse{State: tfsdk.State{Schema: planState.Schema, Raw: planState.Raw}}
	res.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(planState)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	want := `GRANT RUN ON FLIGHT '` + testGrantObjectID + `' TO ROLE "operators"`
	if len(client.queries) != 1 || client.queries[0] != want {
		t.Fatalf("queries = %q, want only %q", client.queries, want)
	}
	var id types.String
	resp.State.GetAttribute(t.Context(), path.Root("id"), &id)
	if id.ValueString() != testGrantObjectID+"/role/operators" {
		t.Fatalf("id = %s", id.ValueString())
	}
}

// Values that are unknown during planning skip the plan-time validators, so
// apply must reject them before they reach SQL.
func TestObjectGrantCreateRevalidatesAtApply(t *testing.T) {
	tests := map[string]objectGrantModel{
		"object ID that is not a UUID": {ObjectID: types.StringValue("x' OR '1'='1"), GranteeType: types.StringValue("user"), GranteeName: types.StringValue("alice")},
		"organization with a name":     grantModel("organization", types.StringValue("acme")),
		"user without a name":          grantModel("user", types.StringNull()),
		"blank principal":              grantModel("role", types.StringValue(" ")),
	}
	for name, model := range tests {
		t.Run(name, func(t *testing.T) {
			client := &scriptedAppSQL{exec: func(string) error { return nil }}
			res := grantResource(diveGrantKind, client)
			planState := grantState(t, res, model)
			resp := resource.CreateResponse{State: tfsdk.State{Schema: planState.Schema, Raw: planState.Raw}}
			res.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(planState)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected a diagnostic")
			}
			if len(client.queries) != 0 {
				t.Fatalf("SQL ran before validation: %q", client.queries)
			}
		})
	}
}

func TestObjectGrantRead(t *testing.T) {
	listed := `[{"grantee_name":"Acme","grantee_type":"organization","privilege":"read"},{"grantee_name":"finance","grantee_type":"role","privilege":"read"}]`
	tests := map[string]struct {
		model      objectGrantModel
		rows       string
		err        error
		wantRemove bool
		wantError  bool
	}{
		"grant is listed":                 {model: grantModel("role", types.StringValue("Finance")), rows: listed},
		"organization grant is listed":    {model: grantModel("organization", types.StringNull()), rows: listed},
		"grant revoked out of band":       {model: grantModel("user", types.StringValue("alice")), rows: listed, wantRemove: true},
		"no grants left":                  {model: grantModel("organization", types.StringNull()), rows: `[]`, wantRemove: true},
		"dive deleted out of band":        {model: grantModel("role", types.StringValue("finance")), err: grantGoneError("Dive"), wantRemove: true},
		"other errors stay a diagnostic":  {model: grantModel("role", types.StringValue("finance")), err: errors.New("network unreachable"), wantError: true},
		"a missing role is not a removal": {model: grantModel("role", types.StringValue("finance")), err: &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Unable to find role finance"}, wantError: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			client := &scriptedAppSQL{queryJSON: func(string) (string, error) { return tc.rows, tc.err }}
			res := grantResource(diveGrantKind, client)
			state := grantState(t, res, tc.model)
			resp := resource.ReadResponse{State: state}
			res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantError)
			}
			if !tc.wantError && resp.State.Raw.IsNull() != tc.wantRemove {
				t.Fatalf("removed = %t, want %t", resp.State.Raw.IsNull(), tc.wantRemove)
			}
			want := "SELECT grantee_name, grantee_type, privilege FROM MD_LIST_DIVE_GRANTEES('" + testGrantObjectID + "'::UUID)"
			if client.count(want) != 1 {
				t.Fatalf("queries = %q, want %q once", client.queries, want)
			}
		})
	}
}

func TestObjectGrantReadNeedsTheListingFunction(t *testing.T) {
	client := &scriptedAppSQL{functionOK: func(string) bool { return false }}
	res := grantResource(flightGrantKind, client)
	state := grantState(t, res, grantModel("organization", types.StringNull()))
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "md_list_flight_grantees is not exposed") || !strings.Contains(resp.Diagnostics[0].Detail(), "motherduck_flight_grant resource") {
		t.Fatalf("diagnostics = %v", resp.Diagnostics)
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("a session without the listing function must not remove the grant from state")
	}
}

func TestObjectGrantDeleteIgnoresAGoneObject(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		wantError bool
	}{
		"revoked":       {},
		"flight gone":   {err: &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Could not find Flight"}},
		"other failure": {err: &duckdb.Error{Type: duckdb.ErrorTypePermission, Msg: "Permission Error: denied"}, wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			client := &scriptedAppSQL{exec: func(string) error { return tc.err }}
			res := grantResource(flightGrantKind, client)
			state := grantState(t, res, grantModel("organization", types.StringNull()))
			var resp resource.DeleteResponse
			res.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantError)
			}
			if want := `REVOKE RUN ON FLIGHT '` + testGrantObjectID + `' FROM ORGANIZATION`; client.count(want) == 0 {
				t.Fatalf("queries = %q, want %q", client.queries, want)
			}
		})
	}
}

func TestObjectGrantImport(t *testing.T) {
	tests := map[string]struct {
		id       string
		wantType string
		wantName types.String
		wantID   string
		wantErr  bool
	}{
		"role":                     {id: testGrantObjectID + "/role/finance", wantType: "role", wantName: types.StringValue("finance"), wantID: testGrantObjectID + "/role/finance"},
		"grantee type case":        {id: testGrantObjectID + "/USER/alice", wantType: "user", wantName: types.StringValue("alice"), wantID: testGrantObjectID + "/user/alice"},
		"organization":             {id: testGrantObjectID + "/organization", wantType: "organization", wantName: types.StringNull(), wantID: testGrantObjectID + "/organization"},
		"principal with a slash":   {id: testGrantObjectID + "/user/team/alice", wantType: "user", wantName: types.StringValue("team/alice"), wantID: testGrantObjectID + "/user/team/alice"},
		"organization with a name": {id: testGrantObjectID + "/organization/acme", wantErr: true},
		"missing principal":        {id: testGrantObjectID + "/role", wantErr: true},
		"blank principal":          {id: testGrantObjectID + "/role/ ", wantErr: true},
		"unknown grantee type":     {id: testGrantObjectID + "/group/finance", wantErr: true},
		"object ID is not a UUID":  {id: "revenue/role/finance", wantErr: true},
		"object ID only":           {id: testGrantObjectID, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := grantResource(diveGrantKind, &scriptedAppSQL{})
			s := resourceSchema(t, res)
			resp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(t.Context()), nil)}}
			res.ImportState(t.Context(), resource.ImportStateRequest{ID: tc.id}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			var id, diveID, granteeType, granteeName types.String
			resp.State.GetAttribute(t.Context(), path.Root("id"), &id)
			resp.State.GetAttribute(t.Context(), path.Root("dive_id"), &diveID)
			resp.State.GetAttribute(t.Context(), path.Root("grantee_type"), &granteeType)
			resp.State.GetAttribute(t.Context(), path.Root("grantee_name"), &granteeName)
			if id.ValueString() != tc.wantID || diveID.ValueString() != testGrantObjectID || granteeType.ValueString() != tc.wantType || !granteeName.Equal(tc.wantName) {
				t.Fatalf("imported id=%s dive_id=%s grantee_type=%s grantee_name=%s", id, diveID, granteeType, granteeName)
			}
		})
	}
}

func TestObjectGrantErrorDetail(t *testing.T) {
	dive := grantResource(diveGrantKind, &scriptedAppSQL{})
	flight := grantResource(flightGrantKind, &scriptedAppSQL{})
	tests := map[string]struct {
		res  *objectGrantResource
		err  error
		want string
	}{
		"unknown user":      {dive, errors.New("Catalog Error: Unable to find user alice"), "as `motherduck_users` lists it"},
		"unknown role":      {dive, errors.New("Catalog Error: Unable to find role finance"), "Reference the `motherduck_role` resource"},
		"dive not visible":  {dive, grantGoneError("Dive"), "The owner of a Dive and organization admins can grant access to it"},
		"flight not owned":  {flight, errors.New("Permission Error: Only the owner of this Flight can add grants"), "Only the owner of a Flight can grant access to it"},
		"old client":        {flight, errors.New(`Parser Error: syntax error at or near "FLIGHT"`), "does not support GRANT ON FLIGHT"},
		"other errors pass": {flight, errors.New("network unreachable"), "network unreachable"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			detail := tc.res.grantErrorDetail(tc.err)
			if !strings.Contains(detail, tc.want) || !strings.Contains(detail, tc.err.Error()) {
				t.Fatalf("detail = %q, want the original error and %q", detail, tc.want)
			}
		})
	}
	if got := flight.grantErrorDetail(errors.New("network unreachable")); got != "network unreachable" {
		t.Fatalf("an error without a hint was rewritten: %q", got)
	}
}
