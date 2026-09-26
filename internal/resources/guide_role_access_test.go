package resources

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// guideRoleSQL answers function probes like production and parameter probes
// according to roleNames, which models whether the session has the planned
// role_names parameter on MD_SET_GUIDE_ACCESS.
type guideRoleSQL struct {
	*scriptedAppSQL
	roleNames       bool
	parameterProbes []string
}

func (c *guideRoleSQL) Exists(ctx context.Context, query string, args ...any) (bool, error) {
	if strings.Contains(query, "parameters") {
		c.parameterProbes = append(c.parameterProbes, fmt.Sprintf("%v", args))
		return c.roleNames, nil
	}
	return c.scriptedAppSQL.Exists(ctx, query, args...)
}

func guideRow(access string) scannedRow {
	return scannedRow{values: []any{nil, "Guide", nil, "content", nil, nil, access, "owner", "owner name", int64(1), "c", "u", "v", nil}}
}

func TestGuideCreateRejectsRoleAccessWithoutRoleNamesParameter(t *testing.T) {
	ctx := t.Context()
	client := &guideRoleSQL{scriptedAppSQL: &scriptedAppSQL{}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	s := resourceSchema(t, res)
	plan := tfsdk.Plan{Schema: s}
	roles, _ := types.SetValueFrom(ctx, types.StringType, []string{"analysts"})
	model := guideModel{
		ID:               types.StringUnknown(),
		Topic:            types.StringNull(),
		Title:            types.StringValue("Guide"),
		Description:      types.StringNull(),
		Content:          types.StringValue("content"),
		ChangeComment:    types.StringNull(),
		ExternalID:       types.StringNull(),
		References:       types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()}),
		Access:           types.StringValue("role"),
		RoleNames:        roles,
		OwnerID:          types.StringUnknown(),
		OwnerName:        types.StringUnknown(),
		CurrentVersion:   types.Int64Unknown(),
		CreatedAt:        types.StringUnknown(),
		UpdatedAt:        types.StringUnknown(),
		VersionCreatedAt: types.StringUnknown(),
	}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	res.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !hasErrorSummary(resp.Diagnostics, "MotherDuck Guide role access unavailable") {
		t.Fatalf("expected role access unavailable diagnostic, got %v", resp.Diagnostics)
	}
	if got := client.count("MD_CREATE_GUIDE"); got != 0 {
		t.Fatalf("MD_CREATE_GUIDE calls = %d, want 0 so no Guide is left behind", got)
	}
	if len(client.parameterProbes) != 1 || client.parameterProbes[0] != "[md_set_guide_access role_names]" {
		t.Fatalf("parameter probes = %v", client.parameterProbes)
	}
	for _, probe := range client.probes {
		if probe == "md_list_guide_grantees" {
			t.Fatal("role access must not depend on MD_LIST_GUIDE_GRANTEES")
		}
	}
}

func TestReadGuideSkipsRoleProbeForProductionAccessModes(t *testing.T) {
	for _, access := range []string{"user", "organization"} {
		t.Run(access, func(t *testing.T) {
			client := &guideRoleSQL{scriptedAppSQL: &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
				if strings.Contains(query, "access_role_names") {
					return scannedRow{err: fmt.Errorf("production has no access_role_names column")}
				}
				return guideRow(access)
			}}}
			res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			model := guideModel{ID: types.StringValue(testAppID), References: types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()})}
			var diags diag.Diagnostics
			if !res.readGuide(t.Context(), &model, &diags) || diags.HasError() {
				t.Fatalf("readGuide diagnostics: %v", diags)
			}
			if !model.RoleNames.IsNull() || model.Access.ValueString() != access {
				t.Fatalf("access = %s, role_names = %s", model.Access, model.RoleNames)
			}
			if len(client.parameterProbes) != 0 || client.count("access_role_names") != 0 {
				t.Fatalf("production access modes must not probe role support: probes=%v queries=%v", client.parameterProbes, client.queries)
			}
		})
	}
}

func TestReadGuideReadsRoleAudienceFromAccessRoleNames(t *testing.T) {
	client := &guideRoleSQL{roleNames: true, scriptedAppSQL: &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		if strings.Contains(query, "access_role_names") {
			return scannedRow{values: []any{`["finance","analysts"]`}}
		}
		return guideRow("ROLE")
	}}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := guideModel{ID: types.StringValue(testAppID), References: types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()})}
	var diags diag.Diagnostics
	if !res.readGuide(t.Context(), &model, &diags) || diags.HasError() {
		t.Fatalf("readGuide diagnostics: %v", diags)
	}
	want, _ := types.SetValueFrom(t.Context(), types.StringType, []string{"analysts", "finance"})
	if model.Access.ValueString() != "role" || !model.RoleNames.Equal(want) {
		t.Fatalf("access = %s, role_names = %s", model.Access, model.RoleNames)
	}
	if client.count("MD_LIST_GUIDE_GRANTEES") != 0 {
		t.Fatal("role audience must come from MD_GET_GUIDE access_role_names")
	}
}

func TestReadGuideReportsRoleAccessWithoutRoleSupport(t *testing.T) {
	client := &guideRoleSQL{scriptedAppSQL: &scriptedAppSQL{queryRow: func(string) mdsql.RowScanner { return guideRow("role") }}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := guideModel{ID: types.StringValue(testAppID), References: types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()})}
	var diags diag.Diagnostics
	if res.readGuide(t.Context(), &model, &diags) || !hasErrorSummary(diags, "MotherDuck Guide role access unavailable") {
		t.Fatalf("expected role access unavailable diagnostic, got %v", diags)
	}
}

func TestSetGuideAccessSendsRoleNames(t *testing.T) {
	client := &guideRoleSQL{roleNames: true, scriptedAppSQL: &scriptedAppSQL{queryJSON: func(string) (string, error) { return "[]", nil }}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	roles, _ := types.SetValueFrom(t.Context(), types.StringType, []string{"finance"})
	var diags diag.Diagnostics
	if !res.setGuideAccess(t.Context(), client, types.StringValue(testAppID), types.StringValue("role"), roles, &diags) {
		t.Fatalf("setGuideAccess diagnostics: %v", diags)
	}
	if client.count("MD_SET_GUIDE_ACCESS") != 1 || client.count("role_names := ['finance']") != 1 {
		t.Fatalf("queries = %v", client.queries)
	}
}

func hasErrorSummary(diags diag.Diagnostics, summary string) bool {
	for _, d := range diags.Errors() {
		if d.Summary() == summary {
			return true
		}
	}
	return false
}
