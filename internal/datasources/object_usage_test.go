package datasources

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAgentUsageQueries(t *testing.T) {
	spec := findSpec(t, "agent_usage")
	const order = " ORDER BY run_finished_ts, session_id, run_seq"
	cases := []struct {
		model rowsModel
		want  string
	}{
		{rowsModel{}, "SELECT * FROM MD_INFORMATION_SCHEMA.AGENT_USAGE" + order},
		{
			rowsModel{Username: types.StringValue("O'Brien")},
			"SELECT * FROM MD_INFORMATION_SCHEMA.AGENT_USAGE WHERE lower(user_name) = lower('O''Brien')" + order,
		},
		{
			// Bounds are sent in UTC, and the end bound is exclusive so
			// consecutive windows never count a run twice.
			rowsModel{StartTime: types.StringValue("2026-11-01T02:00:00+02:00"), EndTime: types.StringValue("2026-12-01T00:00:00Z"), Limit: types.Int64Value(10), Offset: types.Int64Value(20)},
			"SELECT * FROM MD_INFORMATION_SCHEMA.AGENT_USAGE WHERE run_finished_ts >= '2026-11-01T00:00:00Z'::TIMESTAMPTZ AND run_finished_ts < '2026-12-01T00:00:00Z'::TIMESTAMPTZ" + order + " LIMIT 10 OFFSET 20",
		},
	}
	for _, tc := range cases {
		query, err := spec.build(tc.model)
		if err != nil || query != tc.want {
			t.Fatalf("agent_usage build = %q, %v, want %q", query, err, tc.want)
		}
	}
	if spec.requiredFunction != "md_agent_usage" {
		t.Fatalf("agent_usage must check md_agent_usage before reading, got %q", spec.requiredFunction)
	}
	if err := spec.validate(rowsModel{StartTime: types.StringValue("2026-11-02T00:00:00Z"), EndTime: types.StringValue("2026-11-01T00:00:00Z")}); err == nil || !strings.Contains(err.Error(), "must be later than start_time") {
		t.Fatalf("an empty window must fail at plan time, got %v", err)
	}
	if summary, ok := spec.permissionError("Invalid Error: MDExternalException: Missing permission for query history. Ask an organization admin to grant you access."); !ok || summary != "MotherDuck query history access required" {
		t.Fatalf("permission error = %q, %t", summary, ok)
	}
}

// The time bounds of agent usage compare the run finish time, so they must not
// carry the compute usage wording about usage windows.
func TestAgentUsageDescribesItsOwnFilters(t *testing.T) {
	var resp datasource.SchemaResponse
	(&rowsDataSource{spec: findSpec(t, "agent_usage")}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	for name, want := range map[string]string{"start_time": "finished at or after", "end_time": "finished before", "username": "the user started"} {
		attr, ok := resp.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !strings.Contains(attr.MarkdownDescription, want) {
			t.Fatalf("%s description = %#v, want it to mention %q", name, resp.Schema.Attributes[name], want)
		}
		if !attr.Optional || len(attr.Validators) != len(rowAttribute(name, false).(schema.StringAttribute).Validators) {
			t.Fatalf("%s lost its optional flag or validators", name)
		}
	}
	(&rowsDataSource{spec: findSpec(t, "compute_usage")}).Schema(t.Context(), datasource.SchemaRequest{}, &resp)
	if got := resp.Schema.Attributes["start_time"].(schema.StringAttribute).MarkdownDescription; !strings.Contains(got, "usage windows") {
		t.Fatalf("compute_usage start_time description changed: %q", got)
	}
}

func TestObjectGranteesQueries(t *testing.T) {
	for name, tc := range map[string]struct {
		model rowsModel
		want  string
	}{
		"dive_grantees":   {rowsModel{DiveID: types.StringValue(testUUID)}, "SELECT grantee_name, grantee_type, privilege FROM MD_LIST_DIVE_GRANTEES('" + testUUID + "'::UUID) ORDER BY lower(grantee_type), lower(grantee_name), grantee_name, lower(privilege)"},
		"flight_grantees": {rowsModel{FlightID: types.StringValue(testUUID)}, "SELECT grantee_name, grantee_type, privilege FROM MD_LIST_FLIGHT_GRANTEES('" + testUUID + "'::UUID) ORDER BY lower(grantee_type), lower(grantee_name), grantee_name, lower(privilege)"},
	} {
		spec := findSpec(t, name)
		query, err := spec.build(tc.model)
		if err != nil || query != tc.want {
			t.Fatalf("%s build = %q, %v, want %q", name, query, err, tc.want)
		}
		// The ID of the other object kind must not satisfy the lookup.
		if _, err := spec.build(rowsModel{GuideID: types.StringValue(testUUID)}); err == nil {
			t.Fatalf("%s built a query without its object ID", name)
		}
	}
}
