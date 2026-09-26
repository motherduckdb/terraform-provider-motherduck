package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func ducklingCooldownModifierPlan(t *testing.T, stateSize, planSize string, stateCooldown types.Int64) types.Int64 {
	t.Helper()
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&ducklingConfigResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	model := func(size string, cooldown types.Int64) ducklingConfigModel {
		return ducklingConfigModel{
			ID:                         types.StringValue("svc"),
			Username:                   types.StringValue("svc"),
			ReadWriteInstanceSize:      types.StringValue(size),
			ReadWriteCooldownSeconds:   cooldown,
			ReadScalingInstanceSize:    types.StringValue("standard"),
			ReadScalingFlockSize:       types.Float64Value(1),
			ReadScalingCooldownSeconds: types.Int64Value(300),
		}
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if d := state.Set(ctx, model(stateSize, stateCooldown)); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, model(planSize, types.Int64Unknown())); d.HasError() {
		t.Fatal(d)
	}
	req := planmodifier.Int64Request{
		Path:        path.Root("read_write_cooldown_seconds"),
		ConfigValue: types.Int64Null(),
		StateValue:  stateCooldown,
		PlanValue:   types.Int64Unknown(),
		State:       state,
		Plan:        plan,
	}
	resp := planmodifier.Int64Response{PlanValue: req.PlanValue}
	ducklingCooldownPlanModifier{sizeAttribute: "read_write_instance_size"}.PlanModifyInt64(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp.PlanValue
}

func TestDucklingCooldownPlanModifier(t *testing.T) {
	for name, tc := range map[string]struct {
		stateSize, planSize string
		state               types.Int64
		want                types.Int64
	}{
		"same size keeps live value":      {"standard", "standard", types.Int64Value(300), types.Int64Value(300)},
		"new size lets MotherDuck decide": {"standard", "jumbo", types.Int64Value(300), types.Int64Unknown()},
		"old null state stays unknown":    {"standard", "standard", types.Int64Null(), types.Int64Unknown()},
		"pulse keeps no cooldown":         {"pulse", "pulse", types.Int64Null(), types.Int64Null()},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ducklingCooldownModifierPlan(t, tc.stateSize, tc.planSize, tc.state); !got.Equal(tc.want) {
				t.Fatalf("planned cooldown = %s, want %s", got, tc.want)
			}
		})
	}
}
