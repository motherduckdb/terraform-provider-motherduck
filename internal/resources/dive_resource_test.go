package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDiveRequiredResourceURLValidator(t *testing.T) {
	for name, tc := range map[string]struct {
		value   types.String
		wantErr bool
	}{
		"database":      {value: types.StringValue("md:analytics"), wantErr: false},
		"share":         {value: types.StringValue("md:_share/analytics/0f1e2d3c"), wantErr: false},
		"missing md":    {value: types.StringValue("analytics"), wantErr: true},
		"https":         {value: types.StringValue("https://app.motherduck.com/share"), wantErr: true},
		"uppercase md":  {value: types.StringValue("MD:analytics"), wantErr: true},
		"blank":         {value: types.StringValue("  "), wantErr: true},
		"unknown value": {value: types.StringUnknown(), wantErr: false},
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			diveRequiredResourceURLValidator{}.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("url"), ConfigValue: tc.value}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("error = %t, want %t: %v", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr && !tc.value.IsUnknown() && strings.TrimSpace(tc.value.ValueString()) != "" && strings.Contains(resp.Diagnostics[0].Detail(), tc.value.ValueString()) {
				t.Fatalf("diagnostic repeats the sensitive URL: %v", resp.Diagnostics)
			}
		})
	}
}

func diveTestModel(content string, resources types.List, versionDescription types.String) diveModel {
	return diveModel{
		ID: types.StringValue(testAppID), Title: types.StringValue("Dive"), Description: types.StringNull(),
		Content: types.StringValue(content), VersionDescription: versionDescription, APIVersion: types.Int64Value(1),
		RequiredResources: resources, Status: types.StringValue("draft"), StatusChangedAt: types.StringNull(),
		StatusSetBy: types.StringNull(), StatusVersion: types.Int64Null(), CurrentVersion: types.Int64Value(2),
		VersionCreatedAt: types.StringNull(), CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(),
		OwnerID: types.StringNull(), OwnerName: types.StringNull(),
	}
}

func diveTestResources(t *testing.T, resourceType types.String, urls ...string) types.List {
	t.Helper()
	items := make([]attr.Value, 0, len(urls))
	objectType := diveRequiredResourceObjectType()
	for i, url := range urls {
		items = append(items, types.ObjectValueMust(objectType.AttrTypes, map[string]attr.Value{
			"alias":         types.StringValue(strings.Repeat("a", i+1)),
			"url":           types.StringValue(url),
			"resource_type": resourceType,
		}))
	}
	return types.ListValueMust(objectType, items)
}

func TestDiveVersionDescriptionPlanModifier(t *testing.T) {
	ctx := t.Context()
	var schemaResp resource.SchemaResponse
	NewDiveResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	for name, tc := range map[string]struct {
		planContent string
		want        types.String
	}{
		"metadata change keeps the version description": {planContent: "content", want: types.StringValue("notes")},
		"content change writes a version without one":   {planContent: "new content", want: types.StringUnknown()},
	} {
		t.Run(name, func(t *testing.T) {
			state := tfsdk.State{Schema: s}
			if d := state.Set(ctx, diveTestModel("content", diveTestResources(t, types.StringValue("database"), "md:analytics"), types.StringValue("notes"))); d.HasError() {
				t.Fatal(d)
			}
			plan := tfsdk.Plan{Schema: s}
			// The planned mount type is unknown until its own modifier runs, which
			// must not count as a mount change.
			if d := plan.Set(ctx, diveTestModel(tc.planContent, diveTestResources(t, types.StringUnknown(), "md:analytics"), types.StringUnknown())); d.HasError() {
				t.Fatal(d)
			}
			configPlan := tfsdk.Plan{Schema: s}
			if d := configPlan.Set(ctx, diveTestModel(tc.planContent, diveTestResources(t, types.StringNull(), "md:analytics"), types.StringNull())); d.HasError() {
				t.Fatal(d)
			}
			config := tfsdk.Config{Schema: s, Raw: configPlan.Raw}
			req := planmodifier.StringRequest{
				Path: path.Root("version_description"), Plan: plan, State: state, Config: config,
				PlanValue: types.StringUnknown(), StateValue: types.StringValue("notes"), ConfigValue: types.StringNull(),
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			diveVersionDescriptionPlanModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Fatalf("planned version_description = %s, want %s", resp.PlanValue, tc.want)
			}
		})
	}
}

func TestDiveResourceTypePlanModifier(t *testing.T) {
	ctx := t.Context()
	var schemaResp resource.SchemaResponse
	NewDiveResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	for name, tc := range map[string]struct {
		stateURL string
		planURL  string
		want     types.String
	}{
		"same mount keeps its type":  {stateURL: "md:analytics", planURL: "md:Analytics", want: types.StringValue("database")},
		"new share is a share":       {stateURL: "md:analytics", planURL: "md:_share/sales/0f1e2d3c", want: types.StringValue("share")},
		"new database stays unknown": {stateURL: "md:analytics", planURL: "md:other", want: types.StringUnknown()},
	} {
		t.Run(name, func(t *testing.T) {
			state := tfsdk.State{Schema: s}
			if d := state.Set(ctx, diveTestModel("content", diveTestResources(t, types.StringValue("database"), tc.stateURL), types.StringNull())); d.HasError() {
				t.Fatal(d)
			}
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, diveTestModel("content", diveTestResources(t, types.StringUnknown(), tc.planURL), types.StringNull())); d.HasError() {
				t.Fatal(d)
			}
			attributePath := path.Root("required_resources").AtListIndex(0).AtName("resource_type")
			req := planmodifier.StringRequest{
				Path: attributePath, Plan: plan, State: state,
				PlanValue: types.StringUnknown(), StateValue: types.StringValue("database"), ConfigValue: types.StringNull(),
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			diveResourceTypePlanModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Fatalf("planned resource_type = %s, want %s", resp.PlanValue, tc.want)
			}
		})
	}
}

func TestDiveContentArgsVersionDescription(t *testing.T) {
	ctx := context.Background()
	resources := diveTestResources(t, types.StringValue("database"), "md:analytics")
	unknownTypes := diveTestResources(t, types.StringUnknown(), "md:analytics")
	state := diveTestModel("content", resources, types.StringValue("notes"))

	unchanged := diveTestModel("content", unknownTypes, types.StringValue("notes"))
	if args, ok := diveContentArgs(ctx, &unchanged, &state, &diag.Diagnostics{}); ok {
		t.Fatalf("an unknown resource_type alone must not write a version, got %#v", args)
	}
	unconfigured := diveTestModel("content", unknownTypes, types.StringUnknown())
	if args, ok := diveContentArgs(ctx, &unconfigured, &state, &diag.Diagnostics{}); ok {
		t.Fatalf("an unconfigured version_description must not write a version, got %#v", args)
	}

	changed := diveTestModel("content", resources, types.StringValue("new notes"))
	args, ok := diveContentArgs(ctx, &changed, &state, &diag.Diagnostics{})
	if !ok || args["description"] != "'new notes'" || args["content"] != "'content'" || args["required_resources"] != "[{alias: 'a', url: 'md:analytics'}]" {
		t.Fatalf("a version_description change must rewrite the same content with the description, got %#v, %t", args, ok)
	}
}
