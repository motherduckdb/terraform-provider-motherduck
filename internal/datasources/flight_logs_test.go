package datasources

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFlightLogsQueryPagesOnServer(t *testing.T) {
	const prefix = "SELECT * FROM MD_GET_FLIGHT_LOGS("
	const ids = "flight_id := '11111111-1111-4111-8111-111111111111'::UUID, run_number := 3)"
	base := func() rowsModel {
		return rowsModel{
			FlightID:  types.StringValue("11111111-1111-4111-8111-111111111111"),
			RunNumber: types.Int64Value(3),
			Limit:     types.Int64Null(),
			Offset:    types.Int64Null(),
			Order:     types.StringNull(),
		}
	}
	cases := []struct {
		name   string
		mutate func(*rowsModel)
		want   string
	}{
		{"default window", func(*rowsModel) {}, prefix + ids},
		{"limit", func(m *rowsModel) { m.Limit = types.Int64Value(100) }, prefix + `"limit" := 100, ` + ids},
		{"limit and offset", func(m *rowsModel) { m.Limit = types.Int64Value(100); m.Offset = types.Int64Value(200) }, prefix + `"limit" := 100, "offset" := 200, ` + ids},
		{"tail", func(m *rowsModel) { m.Limit = types.Int64Value(20); m.Order = types.StringValue("desc") }, prefix + `"limit" := 20, "order" := 'desc', ` + ids},
		// MotherDuck rejects limit 0, so read one line and return none.
		{"zero limit", func(m *rowsModel) { m.Limit = types.Int64Value(0) }, prefix + `"limit" := 1, ` + ids + " LIMIT 0"},
		// MotherDuck rejects offset without limit, so skip lines in SQL.
		{"offset only", func(m *rowsModel) { m.Offset = types.Int64Value(5) }, prefix + ids + " ORDER BY line_number OFFSET 5"},
		{"unknown limit", func(m *rowsModel) { m.Limit = types.Int64Unknown() }, prefix + ids},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := base()
			tc.mutate(&model)
			got, err := findSpec(t, "flight_logs").build(model)
			if err != nil || got != tc.want {
				t.Fatalf("build = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestFlightLogOrderValidator(t *testing.T) {
	for _, tc := range []struct {
		value   types.String
		wantErr bool
	}{
		{types.StringValue("asc"), false},
		{types.StringValue("desc"), false},
		{types.StringValue("DESC"), true},
		{types.StringValue("tail"), true},
		{types.StringNull(), false},
		{types.StringUnknown(), false},
	} {
		resp := validator.StringResponse{}
		flightLogOrderValidator{}.ValidateString(t.Context(), validator.StringRequest{Path: path.Root("order"), ConfigValue: tc.value}, &resp)
		if resp.Diagnostics.HasError() != tc.wantErr {
			t.Fatalf("order %s diagnostics = %v, want error %t", tc.value, resp.Diagnostics, tc.wantErr)
		}
	}
}

func TestFlightListingsDocumentServerDefaults(t *testing.T) {
	for _, name := range []string{"flights", "flight_versions", "flight_runs"} {
		if description := findSpec(t, name).description; !strings.Contains(description, "50") {
			t.Fatalf("%s description must document the 50-row default page: %s", name, description)
		}
	}
	if description := findSpec(t, "flight_logs").description; !strings.Contains(description, "1,000 lines") {
		t.Fatalf("flight_logs description must document the default log window: %s", description)
	}
	for _, attr := range findSpec(t, "role_members").typedRows {
		if attr.name == "email" && !strings.Contains(attr.description, "service accounts") {
			t.Fatalf("role_members email must document null emails for service accounts: %s", attr.description)
		}
	}
}
