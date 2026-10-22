package resources

import (
	"context"
	"strings"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func serviceAccountPlanPair(t *testing.T, prior, planned serviceAccountModel) (tfsdk.State, tfsdk.Plan) {
	t.Helper()
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	NewServiceAccountResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, prior); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(ctx, planned); diags.HasError() {
		t.Fatal(diags)
	}
	return state, plan
}

// TestUsernameRequiresReplaceIgnoresCase pins the replacement rule for every
// username attribute. MotherDuck matches usernames case-insensitively, so a
// case-only edit names the same account, and replacing a service account
// deletes its data.
func TestUsernameRequiresReplaceIgnoresCase(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prior, planned string
		wantReplace    bool
	}{
		"unchanged":       {prior: "svc_reader", planned: "svc_reader"},
		"case only":       {prior: "svc_reader", planned: "SVC_Reader"},
		"unicode case":    {prior: "équipe", planned: "ÉQUIPE"},
		"different name":  {prior: "svc_reader", planned: "svc_writer", wantReplace: true},
		"case and rename": {prior: "svc_reader", planned: "SVC_Readers", wantReplace: true},
	} {
		t.Run(name, func(t *testing.T) {
			state, plan := serviceAccountPlanPair(t,
				serviceAccountModel{ID: types.StringValue(tc.prior), Username: types.StringValue(tc.prior), Description: types.StringNull()},
				serviceAccountModel{ID: types.StringUnknown(), Username: types.StringValue(tc.planned), Description: types.StringNull()},
			)
			req := planmodifier.StringRequest{
				Path:        path.Root("username"),
				State:       state,
				Plan:        plan,
				StateValue:  types.StringValue(tc.prior),
				PlanValue:   types.StringValue(tc.planned),
				ConfigValue: types.StringValue(tc.planned),
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			for _, modifier := range usernameRequiresReplace() {
				modifier.PlanModifyString(ctx, req, resp)
			}
			if resp.RequiresReplace != tc.wantReplace {
				t.Fatalf("RequiresReplace = %t, want %t", resp.RequiresReplace, tc.wantReplace)
			}
			if !resp.PlanValue.Equal(types.StringValue(tc.planned)) {
				t.Fatalf("planned username = %s, want the configured %q", resp.PlanValue, tc.planned)
			}
		})
	}
}

func TestUsernameAttributesUseCaseInsensitiveReplacement(t *testing.T) {
	ctx := context.Background()
	for name, res := range map[string]resource.Resource{
		"service_account": NewServiceAccountResource(),
		"access_token":    NewAccessTokenResource(),
		"duckling_config": NewDucklingConfigResource(),
	} {
		t.Run(name, func(t *testing.T) {
			var resp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &resp)
			attr := resp.Schema.Attributes["username"]
			modifiers := attr.(interface{ StringPlanModifiers() []planmodifier.String }).StringPlanModifiers()
			if len(modifiers) != 1 || modifiers[0].Description(ctx) != usernameRequiresReplace()[0].Description(ctx) {
				t.Fatalf("username plan modifiers = %v, want the case-insensitive replacement", modifiers)
			}
		})
	}
}

func TestIDFollowsUsernameOnUpdate(t *testing.T) {
	ctx := context.Background()
	state, plan := serviceAccountPlanPair(t,
		serviceAccountModel{ID: types.StringValue("svc_reader"), Username: types.StringValue("svc_reader"), Description: types.StringNull()},
		serviceAccountModel{ID: types.StringUnknown(), Username: types.StringValue("SVC_Reader"), Description: types.StringNull()},
	)
	resp := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	idFollowsUsernameOnUpdate{}.PlanModifyString(ctx, planmodifier.StringRequest{Path: path.Root("id"), State: state, Plan: plan, PlanValue: types.StringUnknown()}, resp)
	if !resp.PlanValue.Equal(types.StringValue("SVC_Reader")) {
		t.Fatalf("planned id = %s, want the planned username", resp.PlanValue)
	}

	// A new resource keeps an unknown ID until MotherDuck reports the username.
	nullState := tfsdk.State{Schema: state.Schema, Raw: tftypes.NewValue(state.Raw.Type(), nil)}
	created := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	idFollowsUsernameOnUpdate{}.PlanModifyString(ctx, planmodifier.StringRequest{Path: path.Root("id"), State: nullState, Plan: plan, PlanValue: types.StringUnknown()}, created)
	if !created.PlanValue.IsUnknown() {
		t.Fatalf("planned id on create = %s, want unknown", created.PlanValue)
	}
}

func TestServiceAccountDescriptionCannotChangeAfterCreate(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prior, planned types.String
		username       string
		wantErr        bool
	}{
		"unchanged":         {prior: types.StringValue("loader"), planned: types.StringValue("loader")},
		"both unset":        {prior: types.StringNull(), planned: types.StringNull()},
		"changed":           {prior: types.StringValue("loader"), planned: types.StringValue("reader"), wantErr: true},
		"changed with case": {prior: types.StringValue("loader"), planned: types.StringValue("reader"), username: "SVC", wantErr: true},
		// Removal only clears the state, so it is not an error.
		"removed":           {prior: types.StringValue("loader"), planned: types.StringNull()},
		"added after apply": {prior: types.StringNull(), planned: types.StringValue("loader"), wantErr: true},
		"unknown":           {prior: types.StringValue("loader"), planned: types.StringUnknown()},
		// A rename replaces the account, which then gets the new description.
		"changed with rename": {prior: types.StringValue("loader"), planned: types.StringValue("reader"), username: "svc_new"},
	} {
		t.Run(name, func(t *testing.T) {
			username := "svc"
			if tc.username != "" {
				username = tc.username
			}
			state, plan := serviceAccountPlanPair(t,
				serviceAccountModel{ID: types.StringValue("svc"), Username: types.StringValue("svc"), Description: tc.prior},
				serviceAccountModel{ID: types.StringValue("svc"), Username: types.StringValue(username), Description: tc.planned},
			)
			resp := resource.ModifyPlanResponse{Plan: plan}
			(&serviceAccountResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr && !diagnosticsContain(resp.Diagnostics, "cannot change") {
				t.Fatalf("diagnostics = %v, want a create-only explanation", resp.Diagnostics)
			}
			if tc.wantErr && !diagnosticsContain(resp.Diagnostics, "`terraform state rm`") {
				t.Fatalf("diagnostics = %v, want the state rm and import path", resp.Diagnostics)
			}
		})
	}
}

func TestServiceAccountAdoptionDetailNamesOnlyImportedAccounts(t *testing.T) {
	if got := serviceAccountAdoptionDetail([]byte(serviceAccountDescriptionImported)); !strings.Contains(got, "for this imported account") {
		t.Fatalf("imported detail = %q, want it to name the imported account", got)
	}
	for _, marker := range [][]byte{nil, []byte(serviceAccountDescriptionRemoved)} {
		if got := serviceAccountAdoptionDetail(marker); strings.Contains(got, "imported") {
			t.Fatalf("detail for marker %q = %q, want no mention of import", marker, got)
		}
	}
}

func TestServiceAccountDescriptionSchemaIsCreateOnly(t *testing.T) {
	var resp resource.SchemaResponse
	NewServiceAccountResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attr := resp.Schema.Attributes["description"]
	if !attr.IsOptional() || attr.IsComputed() || attr.IsRequired() {
		t.Fatalf("description = %#v, want optional and not computed", attr)
	}
	// A replace modifier would delete the account and its data.
	if modifiers := attr.(interface{ StringPlanModifiers() []planmodifier.String }).StringPlanModifiers(); len(modifiers) != 0 {
		t.Fatalf("description plan modifiers = %v, want none", modifiers)
	}
}

func TestAccessTokenImportRequiresUUIDTokenID(t *testing.T) {
	ctx := context.Background()
	for id, wantErr := range map[string]bool{
		"svc_reader/123e4567-e89b-42d3-a456-426614174000": false,
		"SVC_Reader/123E4567-E89B-42D3-A456-426614174000": false,
		// The delete endpoint would treat a non-UUID as a token name.
		"svc_reader/ci-loader": true,
		"svc_reader/token-id":  true,
		"svc_reader/123e4567":  true,
	} {
		t.Run(id, func(t *testing.T) {
			var schemaResp resource.SchemaResponse
			NewAccessTokenResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			resp := resource.ImportStateResponse{State: tfsdk.State{
				Schema: schemaResp.Schema,
				Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
			}}
			NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
			if resp.Diagnostics.HasError() != wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, wantErr)
			}
			if wantErr && !diagnosticsContain(resp.Diagnostics, "must be a UUID") {
				t.Fatalf("diagnostics = %v, want a UUID explanation", resp.Diagnostics)
			}
		})
	}
}

func TestAccessTokenNameCountsUTF16Units(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		value   string
		wantErr bool
	}{
		"ascii at limit":       {value: strings.Repeat("n", 255)},
		"ascii over limit":     {value: strings.Repeat("n", 256), wantErr: true},
		"bmp letters at limit": {value: strings.Repeat("é", 255)},
		// 128 emoji are 128 runes but 256 UTF-16 code units.
		"emoji over limit": {value: strings.Repeat("🦆", 128), wantErr: true},
		"emoji at limit":   {value: "n" + strings.Repeat("🦆", 127)},
	} {
		t.Run(name, func(t *testing.T) {
			var errs int
			for _, v := range accessTokenNameValidators() {
				var resp validator.StringResponse
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root("name"), ConfigValue: types.StringValue(tc.value)}, &resp)
				if resp.Diagnostics.HasError() {
					errs++
				}
			}
			if (errs > 0) != tc.wantErr {
				t.Fatalf("errors = %d, want error %t", errs, tc.wantErr)
			}
		})
	}
}

func TestDucklingConfigErrorDetailExplainsTierLimits(t *testing.T) {
	tierError := func(tier string) error {
		return &mdrest.APIError{StatusCode: 400, Code: "BAD_REQUEST", Message: "Invalid config for tier " + tier + ": [object Object]"}
	}
	for name, tc := range map[string]struct {
		err  error
		want []string
		deny []string
	}{
		"business": {
			err:  tierError("business"),
			want: []string{"plan (`business`)", "Business plan allows read-write up to `giga`, read-scaling up to `jumbo`, and a flock size of at most 16", "different limits for an organization or a user", "[object Object]"},
			deny: []string{"Default limits by plan"},
		},
		"lite": {
			err:  tierError("lite"),
			want: []string{"Lite plan allows read-write `pulse`, read-scaling `pulse`, and a flock size of 0"},
		},
		"unknown tier lists every plan": {
			err:  tierError("enterprise"),
			want: []string{"plan (`enterprise`)", "Default limits by plan:", "- Business:", "- Free:"},
		},
		"other bad request is unchanged": {
			err:  &mdrest.APIError{StatusCode: 400, Message: "cooldown_seconds cannot be set for Pulse instances"},
			want: []string{"cooldown_seconds cannot be set"},
			deny: []string{"plan"},
		},
		"tier text on another status is unchanged": {
			err:  &mdrest.APIError{StatusCode: 500, Message: "Invalid config for tier business: [object Object]"},
			deny: []string{"By default"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := ducklingConfigErrorDetail(tc.err)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("detail = %q, want it to contain %q", got, want)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("detail = %q, must not contain %q", got, deny)
				}
			}
		})
	}
}
