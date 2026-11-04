package datasources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestComputeUsageQueries(t *testing.T) {
	spec := findSpec(t, "compute_usage")
	const order = " ORDER BY start_time, user_name, duckling_id, is_read_scaling"
	cases := []struct {
		model rowsModel
		want  string
	}{
		{rowsModel{}, "SELECT * FROM MD_INFORMATION_SCHEMA.COMPUTE_USAGE" + order},
		{
			rowsModel{Username: types.StringValue("O'Brien")},
			"SELECT * FROM MD_INFORMATION_SCHEMA.COMPUTE_USAGE WHERE lower(user_name) = lower('O''Brien')" + order,
		},
		{
			// Bounds are normalized to UTC, so the session time zone never
			// changes the window.
			rowsModel{StartTime: types.StringValue("2026-11-01T02:00:00+02:00"), EndTime: types.StringValue("2026-11-02T00:00:00Z"), Limit: types.Int64Value(10), Offset: types.Int64Value(20)},
			"SELECT * FROM MD_INFORMATION_SCHEMA.COMPUTE_USAGE WHERE start_time >= '2026-11-01T00:00:00Z'::TIMESTAMPTZ AND end_time <= '2026-11-02T00:00:00Z'::TIMESTAMPTZ" + order + " LIMIT 10 OFFSET 20",
		},
		{
			rowsModel{Username: types.StringValue("etl"), EndTime: types.StringValue("2026-11-02T00:00:00.5Z")},
			"SELECT * FROM MD_INFORMATION_SCHEMA.COMPUTE_USAGE WHERE lower(user_name) = lower('etl') AND end_time <= '2026-11-02T00:00:00.5Z'::TIMESTAMPTZ" + order,
		},
	}
	for _, tc := range cases {
		query, err := spec.build(tc.model)
		if err != nil || query != tc.want {
			t.Fatalf("compute_usage build = %q, %v, want %q", query, err, tc.want)
		}
	}
	if spec.requiredFunction != "md_compute_usage" {
		t.Fatalf("compute_usage must check md_compute_usage before reading, got %q", spec.requiredFunction)
	}
}

func TestComputeUsageWindowValidation(t *testing.T) {
	for _, tc := range []struct {
		start, end string
		wantErr    bool
	}{
		{"2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", false},
		{"2026-11-02T00:00:00Z", "2026-11-01T00:00:00Z", true},
		{"2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z", true},
		// The same instant in another offset is still an empty window.
		{"2026-11-01T02:00:00+02:00", "2026-11-01T00:00:00Z", true},
	} {
		err := validateComputeUsageWindow(rowsModel{StartTime: types.StringValue(tc.start), EndTime: types.StringValue(tc.end)})
		if (err != nil) != tc.wantErr {
			t.Fatalf("window %s to %s: err = %v, want error %t", tc.start, tc.end, err, tc.wantErr)
		}
		var attrErr attributeConfigError
		if err != nil && (!errors.As(err, &attrErr) || attrErr.attribute != "end_time") {
			t.Fatalf("window error must point at end_time: %#v", err)
		}
	}
	// One bound alone, or an unknown bound, is checked later or not at all.
	if err := validateComputeUsageWindow(rowsModel{StartTime: types.StringValue("2026-11-01T00:00:00Z"), EndTime: types.StringUnknown()}); err != nil {
		t.Fatalf("unknown end_time must not fail validation: %v", err)
	}
}

func TestRFC3339ValidatorRequiresAnOffset(t *testing.T) {
	for _, tc := range []struct {
		value   types.String
		wantErr bool
	}{
		{types.StringValue("2026-11-01T00:00:00Z"), false},
		{types.StringValue("2026-11-01T00:00:00.123+05:30"), false},
		{types.StringValue("2026-11-01 00:00:00"), true},
		{types.StringValue("2026-11-01"), true},
		{types.StringValue("yesterday"), true},
		{types.StringNull(), false},
		{types.StringUnknown(), false},
	} {
		resp := validator.StringResponse{}
		rfc3339Validator{}.ValidateString(t.Context(), validator.StringRequest{Path: path.Root("start_time"), ConfigValue: tc.value}, &resp)
		if resp.Diagnostics.HasError() != tc.wantErr {
			t.Fatalf("value %s diagnostics = %v, want error %t", tc.value, resp.Diagnostics, tc.wantErr)
		}
	}
}

func TestComputeUsageExplainsQueryHistoryErrors(t *testing.T) {
	ds := &rowsDataSource{spec: findSpec(t, "compute_usage")}
	for _, message := range []string{
		"Invalid Error: MDExternalException: Missing permission for query history. Ask an organization admin to grant you access.",
		"Invalid Error: MDExternalException: Query History is not available on your plan. Consider upgrading to the Business Plan.",
	} {
		var diags diag.Diagnostics
		if _, ok := ds.queryRows(context.Background(), fakeRowsClient{err: errors.New(message)}, "SELECT 1", types.StringNull(), &diags); ok || !diags.HasError() {
			t.Fatalf("%q must fail the read", message)
		}
		if diags[0].Summary() != "MotherDuck query history access required" || !strings.Contains(diags[0].Detail(), "Business plan") || !strings.Contains(diags[0].Detail(), message) {
			t.Fatalf("unexpected diagnostic: %s: %s", diags[0].Summary(), diags[0].Detail())
		}
	}
	// The organization admin explanation stays specific to storage_info.
	var diags diag.Diagnostics
	ds.queryRows(context.Background(), fakeRowsClient{err: errors.New("admin only function")}, "SELECT 1", types.StringNull(), &diags)
	if !diags.HasError() || diags[0].Summary() != "Unable to read motherduck_compute_usage" {
		t.Fatalf("unexpected diagnostic for an unrelated error: %v", diags)
	}
}
