package resources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDiveAPIVersionPlanModifier(t *testing.T) {
	ctx := t.Context()
	var schemaResp resource.SchemaResponse
	NewDiveResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	objectType := diveRequiredResourceObjectType()
	model := func(content string, apiVersion types.Int64) diveModel {
		return diveModel{
			ID: types.StringValue(testAppID), Title: types.StringValue("Dive"), Description: types.StringNull(),
			Content: types.StringValue(content), APIVersion: apiVersion, RequiredResources: types.ListNull(objectType),
			Status: types.StringValue("draft"), StatusChangedAt: types.StringNull(), StatusSetBy: types.StringNull(),
			StatusVersion: types.Int64Null(), CurrentVersion: types.Int64Value(1), CreatedAt: types.StringNull(),
			UpdatedAt: types.StringNull(), OwnerName: types.StringNull(),
		}
	}
	for name, tc := range map[string]struct {
		planContent string
		config      types.Int64
		want        types.Int64
	}{
		"metadata change keeps state":  {planContent: "content", config: types.Int64Null(), want: types.Int64Value(1)},
		"content change stays unknown": {planContent: "new content", config: types.Int64Null(), want: types.Int64Unknown()},
		"configured value is not kept": {planContent: "content", config: types.Int64Value(2), want: types.Int64Unknown()},
	} {
		t.Run(name, func(t *testing.T) {
			state := tfsdk.State{Schema: s}
			if d := state.Set(ctx, model("content", types.Int64Value(1))); d.HasError() {
				t.Fatal(d)
			}
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, model(tc.planContent, types.Int64Unknown())); d.HasError() {
				t.Fatal(d)
			}
			req := planmodifier.Int64Request{
				Path: path.Root("api_version"), Plan: plan, State: state,
				PlanValue: types.Int64Unknown(), StateValue: types.Int64Value(1), ConfigValue: tc.config,
			}
			resp := &planmodifier.Int64Response{PlanValue: req.PlanValue}
			diveAPIVersionPlanModifier{}.PlanModifyInt64(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Fatalf("planned api_version = %s, want %s", resp.PlanValue, tc.want)
			}
		})
	}
}
