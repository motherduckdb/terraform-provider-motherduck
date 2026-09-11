package resources

import (
	"context"
	stdsql "database/sql"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

var (
	_ resource.Resource                = &diveResource{}
	_ resource.ResourceWithConfigure   = &diveResource{}
	_ resource.ResourceWithImportState = &diveResource{}
	_ resource.Resource                = &flightResource{}
	_ resource.ResourceWithConfigure   = &flightResource{}
	_ resource.ResourceWithImportState = &flightResource{}
	_ resource.Resource                = &flightRunResource{}
	_ resource.ResourceWithConfigure   = &flightRunResource{}
	_ resource.ResourceWithImportState = &flightRunResource{}
)

func importUUIDID(ctx context.Context, id string, resp *resource.ImportStateResponse) {
	if !validateUUIDString(id, "Import ID", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func addOptionalStringUpdate(args map[string]string, name string, plan, state types.String, nullValue string) {
	if plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		if nullValue != "" {
			args[name] = nullValue
		}
		return
	}
	args[name] = sqlbuild.StringLiteral(plan.ValueString())
}

func optionalStringFromLive(current types.String, live stdsql.NullString) types.String {
	if !live.Valid {
		return types.StringNull()
	}
	if live.String == "" && current.IsNull() {
		return types.StringNull()
	}
	return types.StringValue(live.String)
}

func optionalConfigOwnedStringFromLive(current types.String, live stdsql.NullString) types.String {
	if current.IsNull() {
		return types.StringNull()
	}
	return optionalStringFromLive(current, live)
}

func optionalStringListFromJSON(ctx context.Context, current types.List, raw stdsql.NullString, field string, diags *diag.Diagnostics) types.List {
	values := []string{}
	if !decodeNullableJSON(raw, &values, "Unable to parse MotherDuck Flight "+field, diags) {
		return current
	}
	if len(values) == 0 && current.IsNull() {
		return types.ListNull(types.StringType)
	}
	value, valueDiags := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return current
	}
	return value
}

func optionalStringMapFromJSON(ctx context.Context, current types.Map, raw stdsql.NullString, field string, diags *diag.Diagnostics) types.Map {
	values := map[string]string{}
	if !decodeNullableJSON(raw, &values, "Unable to parse MotherDuck Flight "+field, diags) {
		return current
	}
	if len(values) == 0 && current.IsNull() {
		return types.MapNull(types.StringType)
	}
	value, valueDiags := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return current
	}
	return value
}
