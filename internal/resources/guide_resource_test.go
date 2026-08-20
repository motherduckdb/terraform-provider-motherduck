package resources

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestGuideResourceSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewGuideResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	access, ok := resp.Schema.Attributes["access"].(schema.StringAttribute)
	if !ok || !access.Optional || !access.Computed || len(access.Validators) == 0 || len(access.PlanModifiers) == 0 {
		t.Fatalf("access schema does not preserve and validate the live Guide access value: %#v", access)
	}
	roleNames, ok := resp.Schema.Attributes["role_names"].(schema.SetAttribute)
	if !ok || !roleNames.Optional || !roleNames.Computed || roleNames.ElementType != types.StringType {
		t.Fatalf("role_names schema = %#v, want optional computed string set", resp.Schema.Attributes["role_names"])
	}
	references, ok := resp.Schema.Attributes["references"].(schema.ListNestedAttribute)
	if !ok || !references.Optional {
		t.Fatalf("references attribute = %#v, want optional nested list", resp.Schema.Attributes["references"])
	}
	if _, ok := references.NestedObject.Attributes["uuid"]; !ok {
		t.Fatal("references schema must expose UUID targets")
	}
}

func TestGuideRoleAccessValidation(t *testing.T) {
	validRoles := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("finance_readers"),
		types.StringValue("data_team"),
	})
	invalidRoles := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("invalid.role"),
	})
	tests := map[string]struct {
		access    types.String
		roleNames types.Set
		wantError bool
	}{
		"role with audience": {
			access:    types.StringValue("role"),
			roleNames: validRoles,
		},
		"role with planned audience": {
			access: types.StringValue("role"),
			roleNames: types.SetValueMust(types.StringType, []attr.Value{
				types.StringUnknown(),
			}),
		},
		"role without audience": {
			access:    types.StringValue("role"),
			roleNames: types.SetNull(types.StringType),
			wantError: true,
		},
		"user with audience": {
			access:    types.StringValue("user"),
			roleNames: validRoles,
			wantError: true,
		},
		"invalid role name": {
			access:    types.StringValue("role"),
			roleNames: invalidRoles,
			wantError: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateGuideRoleAccess(t.Context(), tc.access, tc.roleNames, &diags)
			if diags.HasError() != tc.wantError {
				t.Fatalf("diagnostics = %v, want error %v", diags, tc.wantError)
			}
		})
	}
}

func TestGuideTopicValidator(t *testing.T) {
	tests := map[string]struct {
		value   string
		wantErr bool
	}{
		"empty clears":   {value: ""},
		"nested":         {value: "metrics/revenue-v2"},
		"leading slash":  {value: "/metrics", wantErr: true},
		"trailing slash": {value: "metrics/", wantErr: true},
		"empty segment":  {value: "metrics//revenue", wantErr: true},
		"dot segment":    {value: "metrics/.", wantErr: true},
		"spaces":         {value: "metrics and revenue", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			guideTopicValidator{}.ValidateString(t.Context(), validator.StringRequest{
				Path:        path.Root("topic"),
				ConfigValue: types.StringValue(tc.value),
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestGuideContentValidatorUsesUTF8Bytes(t *testing.T) {
	tests := map[string]struct {
		value   string
		wantErr bool
	}{
		"content":      {value: "# Guide"},
		"empty":        {value: "", wantErr: true},
		"one over":     {value: strings.Repeat("a", maxGuideContentBytes+1), wantErr: true},
		"unicode over": {value: strings.Repeat("é", maxGuideContentBytes/2+1), wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			guideContentValidator{}.ValidateString(t.Context(), validator.StringRequest{
				Path:        path.Root("content"),
				ConfigValue: types.StringValue(tc.value),
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestGuideReferencesArgBuildsTypedReferenceList(t *testing.T) {
	ctx := context.Background()
	objectType := types.ObjectType{AttrTypes: guideReferenceAttrTypes()}
	reference, referenceDiags := types.ObjectValue(objectType.AttrTypes, map[string]attr.Value{
		"type":        types.StringValue("catalog"),
		"url":         types.StringValue("md:analytics"),
		"schema":      types.StringValue("main"),
		"table":       types.StringValue("invoices"),
		"column":      types.StringNull(),
		"view":        types.StringNull(),
		"macro":       types.StringNull(),
		"uuid":        types.StringNull(),
		"description": types.StringValue("authoritative source"),
	})
	if referenceDiags.HasError() {
		t.Fatalf("object diagnostics: %v", referenceDiags)
	}
	list, listDiags := types.ListValue(objectType, []attr.Value{reference})
	if listDiags.HasError() {
		t.Fatalf("list diagnostics: %v", listDiags)
	}
	var diags diag.Diagnostics
	got, ok := guideReferencesArg(ctx, list, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("guideReferencesArg diagnostics: %v", diags)
	}
	for _, fragment := range []string{"'type': 'catalog'", "'url': 'md:analytics'", "'table': 'invoices'", "'uuid': NULL::UUID"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("guide reference SQL %q does not contain %q", got, fragment)
		}
	}
}

func TestGuideReferencesArgRejectsMissingUUID(t *testing.T) {
	ctx := context.Background()
	reference := guideReferenceModel{
		Type:        types.StringValue("guide"),
		URL:         types.StringNull(),
		Schema:      types.StringNull(),
		Table:       types.StringNull(),
		Column:      types.StringNull(),
		View:        types.StringNull(),
		Macro:       types.StringNull(),
		UUID:        types.StringNull(),
		Description: types.StringNull(),
	}
	list, listDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: guideReferenceAttrTypes()}, []guideReferenceModel{reference})
	if listDiags.HasError() {
		t.Fatalf("list diagnostics: %v", listDiags)
	}
	var diags diag.Diagnostics
	if _, ok := guideReferencesArg(ctx, list, &diags); ok || !diags.HasError() {
		t.Fatalf("missing UUID should fail, diagnostics: %v", diags)
	}
}

func TestGuideReferencesFromJSONMapsResolvedIDs(t *testing.T) {
	raw := sql.NullString{
		Valid:  true,
		String: `[{"type":"guide","guide_id":"123e4567-e89b-42d3-a456-426614174000","description":"dependency"}]`,
	}
	var diags diag.Diagnostics
	value := guideReferencesFromJSON(context.Background(), types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()}), raw, &diags)
	if diags.HasError() {
		t.Fatalf("guideReferencesFromJSON diagnostics: %v", diags)
	}
	var references []guideReferenceModel
	diags.Append(value.ElementsAs(context.Background(), &references, false)...)
	if diags.HasError() {
		t.Fatalf("decoding reference value: %v", diags)
	}
	if len(references) != 1 || references[0].UUID.ValueString() != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatalf("resolved references = %#v", references)
	}
}
