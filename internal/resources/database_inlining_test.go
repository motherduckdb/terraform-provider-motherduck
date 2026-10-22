package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDatabaseDataInliningRowLimitPlanModifier(t *testing.T) {
	ctx := context.Background()
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	for name, tc := range map[string]struct {
		state, config types.Int64
		create        bool
		wantReplace   bool
		wantWarning   bool
	}{
		"create":                   {state: types.Int64Null(), config: types.Int64Value(25), create: true},
		"null to value records":    {state: types.Int64Null(), config: types.Int64Value(25), wantWarning: true},
		"null to unknown records":  {state: types.Int64Null(), config: types.Int64Unknown(), wantWarning: true},
		"value to different value": {state: types.Int64Value(25), config: types.Int64Value(50), wantReplace: true},
		"value to same value":      {state: types.Int64Value(25), config: types.Int64Value(25)},
		"value to null":            {state: types.Int64Value(25), config: types.Int64Null()},
		"value to unknown":         {state: types.Int64Value(25), config: types.Int64Unknown()},
		"null to null":             {state: types.Int64Null(), config: types.Int64Null()},
	} {
		t.Run(name, func(t *testing.T) {
			req := planmodifier.Int64Request{
				Path:        path.Root("data_inlining_row_limit"),
				State:       tfsdk.State{Raw: existing},
				Plan:        tfsdk.Plan{Raw: existing},
				StateValue:  tc.state,
				ConfigValue: tc.config,
				PlanValue:   tc.config,
			}
			if tc.create {
				req.State = tfsdk.State{Raw: tftypes.NewValue(tftypes.Object{}, nil)}
			}
			resp := &planmodifier.Int64Response{PlanValue: req.PlanValue}
			databaseDataInliningRowLimitPlanModifier{}.PlanModifyInt64(ctx, req, resp)
			if resp.RequiresReplace != tc.wantReplace {
				t.Fatalf("RequiresReplace = %t, want %t", resp.RequiresReplace, tc.wantReplace)
			}
			if got := resp.Diagnostics.WarningsCount() > 0; got != tc.wantWarning {
				t.Fatalf("warning = %t, want %t: %v", got, tc.wantWarning, resp.Diagnostics)
			}
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
		})
	}
}
