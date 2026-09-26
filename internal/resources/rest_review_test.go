package resources

import (
	"context"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNormalizedTokenType(t *testing.T) {
	for _, tc := range []struct {
		current  types.String
		reported string
		want     string
	}{
		{types.StringValue("read_write"), "READ_WRITE", "read_write"},
		{types.StringValue("read_scaling"), " read_scaling ", "read_scaling"},
		{types.StringValue("read_scaling"), "", "read_scaling"},
		{types.StringNull(), "", "read_write"},
		{types.StringUnknown(), "", "read_write"},
	} {
		if got := normalizedTokenType(tc.current, tc.reported); got.ValueString() != tc.want {
			t.Fatalf("normalizedTokenType(%s, %q) = %s, want %s", tc.current, tc.reported, got, tc.want)
		}
	}
}

func TestSetDucklingModelFromRESTRecordsLiveCooldowns(t *testing.T) {
	serverDefault := int64(300)
	cfg := &mdrest.DucklingConfig{
		ReadWrite:   mdrest.DucklingReadWriteConfig{InstanceSize: "standard", CooldownSeconds: &serverDefault},
		ReadScaling: mdrest.DucklingReadScalingConfig{InstanceSize: "pulse", FlockSize: 2},
	}
	model := ducklingConfigModel{
		Username:                   types.StringValue("svc"),
		ReadWriteCooldownSeconds:   types.Int64Null(),
		ReadScalingCooldownSeconds: types.Int64Value(600),
	}
	setDucklingModelFromREST(&model, cfg)
	if model.ReadWriteCooldownSeconds.ValueInt64() != 300 {
		t.Fatalf("read_write_cooldown_seconds = %s, want the live value 300", model.ReadWriteCooldownSeconds)
	}
	if !model.ReadScalingCooldownSeconds.IsNull() {
		t.Fatalf("read_scaling_cooldown_seconds = %s, want null when MotherDuck reports none", model.ReadScalingCooldownSeconds)
	}
}

func TestPlannedCooldownOrLive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		planned types.Int64
		live    types.Int64
		want    types.Int64
	}{
		{"response omits planned value", types.Int64Value(600), types.Int64Null(), types.Int64Value(600)},
		{"response reports value", types.Int64Value(600), types.Int64Value(600), types.Int64Value(600)},
		{"unknown plan takes live default", types.Int64Unknown(), types.Int64Value(300), types.Int64Value(300)},
		{"unknown plan and no live value", types.Int64Unknown(), types.Int64Null(), types.Int64Null()},
	} {
		if got := plannedCooldownOrLive(tc.planned, tc.live); !got.Equal(tc.want) {
			t.Fatalf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestRESTImportsRejectDotSegments(t *testing.T) {
	for _, id := range []string{"./token", "../token", "svc/.", "svc/.."} {
		var diags diag.Diagnostics
		parts := strings.SplitN(id, "/", 2)
		ok := validateRESTUsernameImportID(parts[0], "`<username>/<token_id>`", &diags) &&
			validateRESTImportIDPart(parts[1], "`<username>/<token_id>`", "access token ID", &diags)
		if ok || !diags.HasError() {
			t.Fatalf("import ID %q accepted, want a dot-segment error", id)
		}
	}
	var diags diag.Diagnostics
	if !validateRESTUsernameImportID("svc.reader", "`<username>`", &diags) {
		t.Fatalf("dotted username rejected: %v", diags)
	}
}

func TestRESTUsernameValidatorsRejectDotSegments(t *testing.T) {
	for _, value := range []string{".", ".."} {
		var resp validator.StringResponse
		for _, v := range restUsernameValidators() {
			v.ValidateString(t.Context(), validator.StringRequest{Path: path.Root("username"), ConfigValue: types.StringValue(value)}, &resp)
		}
		if !resp.Diagnostics.HasError() {
			t.Fatalf("username %q accepted, want an error", value)
		}
	}
}

type roleListingErrorClient struct {
	scriptedAppSQL
	err error
}

func (c *roleListingErrorClient) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return "", c.err
}

func TestRoleReadKeepsStateOnUnrelatedCatalogError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		wantRemoved bool
	}{
		{"unrelated catalog", &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Table with name roles does not exist"}, false},
		{"named role", &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: "Catalog Error: Role analysts does not exist"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &roleListingErrorClient{err: tc.err}
			res := &roleResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			model := roleModel{Name: types.StringValue("analysts")}
			var diags diag.Diagnostics
			if res.readRole(t.Context(), &model, &diags) {
				t.Fatal("readRole reported a role from a failed listing")
			}
			if removed := !diags.HasError(); removed != tc.wantRemoved {
				t.Fatalf("role treated as removed = %t, want %t (diagnostics: %v)", removed, tc.wantRemoved, diags)
			}
		})
	}
}
