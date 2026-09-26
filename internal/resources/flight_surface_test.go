package resources

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

func TestInvalidFlightConfigKeyRejectsEveryReservedName(t *testing.T) {
	for _, key := range []string{"MOTHERDUCK_TOKEN", "MOTHERDUCK_FLIGHTS_RUN", "MOTHERDUCK_FLIGHT_ID", "MOTHERDUCK_FLIGHT_RUN_ID"} {
		if detail := invalidFlightConfigKey(key); !strings.Contains(detail, "reserved") {
			t.Fatalf("invalidFlightConfigKey(%q) = %q, want a reserved-name error", key, detail)
		}
	}
	// MotherDuck matches reserved names exactly, so other spellings are allowed.
	for _, key := range []string{"motherduck_flight_id", "MOTHERDUCK_FLIGHT_IDS", "WAREHOUSE"} {
		if detail := invalidFlightConfigKey(key); detail != "" {
			t.Fatalf("invalidFlightConfigKey(%q) = %q, want no error", key, detail)
		}
	}
}

func TestFlightContentBytesValidator(t *testing.T) {
	source := flightContentBytes("source", 1, flightMaxSourceCodeBytes)
	requirements := flightContentBytes("requirements", 0, flightMaxRequirementsBytes)
	cases := []struct {
		name    string
		v       validator.String
		value   types.String
		wantErr bool
	}{
		{"source at limit", source, types.StringValue(strings.Repeat("a", flightMaxSourceCodeBytes)), false},
		{"source over limit", source, types.StringValue(strings.Repeat("a", flightMaxSourceCodeBytes+1)), true},
		// Each "é" is two bytes, so the limit counts bytes rather than characters.
		{"source counts bytes", source, types.StringValue(strings.Repeat("é", flightMaxSourceCodeBytes/2+1)), true},
		{"source empty", source, types.StringValue(""), true},
		{"source whitespace", source, types.StringValue(" "), false},
		{"requirements at limit", requirements, types.StringValue(strings.Repeat("a", flightMaxRequirementsBytes)), false},
		{"requirements over limit", requirements, types.StringValue(strings.Repeat("a", flightMaxRequirementsBytes+1)), true},
		{"requirements empty", requirements, types.StringValue(""), false},
		{"unknown", source, types.StringUnknown(), false},
		{"null", requirements, types.StringNull(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := validator.StringResponse{}
			tc.v.ValidateString(t.Context(), validator.StringRequest{Path: path.Root("value"), ConfigValue: tc.value}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func TestFlightSchemaDocumentsPlanRuntimeRulesAndScheduleState(t *testing.T) {
	s := resourceSchema(t, NewFlightResource())
	runtime := s.Attributes["max_runtime_sec"].(schema.Int64Attribute).MarkdownDescription
	if strings.Contains(runtime, "disables the runtime limit") {
		t.Fatalf("max_runtime_sec must not claim 0 always disables the limit: %s", runtime)
	}
	for _, want := range []string{"3,600", "28,800", "Business", "`0` is rejected"} {
		if !strings.Contains(runtime, want) {
			t.Fatalf("max_runtime_sec description missing %q: %s", want, runtime)
		}
	}
	if schedule := s.Attributes["schedule_cron"].(schema.StringAttribute).MarkdownDescription; !strings.Contains(schedule, "Free plan") {
		t.Fatalf("schedule_cron must document the Free plan restriction: %s", schedule)
	}
	for _, name := range []string{"schedule_status", "owner_name"} {
		attr := s.Attributes[name].(schema.StringAttribute)
		if !attr.Computed || attr.Optional || attr.Required {
			t.Fatalf("%s must be computed only", name)
		}
		// MotherDuck can change both outside Terraform, and an update can
		// change the schedule status, so neither may copy prior state.
		if len(attr.PlanModifiers) != 0 {
			t.Fatalf("%s must not use plan modifiers", name)
		}
	}
	if len(s.Attributes["source_code"].(schema.StringAttribute).Validators) == 0 || len(s.Attributes["requirements_txt"].(schema.StringAttribute).Validators) == 0 {
		t.Fatal("source_code and requirements_txt must validate their byte size")
	}
}

func TestReadFlightReadsScheduleStatusAndOwner(t *testing.T) {
	client := &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		switch {
		case strings.Contains(query, "MD_GET_FLIGHT_VERSION"):
			return scannedRow{values: []any{"print(1)", nil, nil, nil, nil, int64(900)}}
		case strings.Contains(query, "MD_GET_FLIGHT("):
			if !strings.Contains(query, "schedule_status") || !strings.Contains(query, "owner_name") {
				return scannedRow{err: fmt.Errorf("query does not read schedule_status and owner_name: %q", query)}
			}
			return scannedRow{values: []any{"flight", "0 * * * *", "DISABLED", "ACTIVE", int64(1), "c", "u", "analyst"}}
		default:
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		}
	}}
	res := &flightResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightModel{ID: types.StringValue(testAppID), Config: types.MapNull(types.StringType), FlightSecretNames: types.ListNull(types.StringType)}
	var diags diag.Diagnostics
	if !res.readFlight(t.Context(), &model, &diags) || diags.HasError() {
		t.Fatalf("readFlight diagnostics: %v", diags)
	}
	if model.ScheduleStatus.ValueString() != "DISABLED" || model.OwnerName.ValueString() != "analyst" {
		t.Fatalf("schedule_status = %s, owner_name = %s", model.ScheduleStatus, model.OwnerName)
	}

	client.queryRow = func(query string) mdsql.RowScanner {
		if strings.Contains(query, "MD_GET_FLIGHT_VERSION") {
			return scannedRow{values: []any{"print(1)", nil, nil, nil, nil, int64(900)}}
		}
		return scannedRow{values: []any{"flight", nil, nil, "ACTIVE", int64(1), "c", "u", "analyst"}}
	}
	if !res.readFlight(t.Context(), &model, &diags) || diags.HasError() {
		t.Fatalf("readFlight diagnostics: %v", diags)
	}
	if !model.ScheduleStatus.IsNull() {
		t.Fatalf("schedule_status = %s, want null for a Flight without a schedule", model.ScheduleStatus)
	}
}

func TestFlightRunMissing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"run missing", &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: `Invalid Input Error: Run 7 does not exist for Flight "x"`}, true},
		{"flight missing", fmt.Errorf("wrapped: %w", &duckdb.Error{Type: duckdb.ErrorTypeIO, Msg: "IO Error: Flight not found"}), true},
		{"permission", &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: "Invalid Input Error: permission denied"}, false},
		{"not duckdb", errors.New("does not exist"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := flightRunMissing(tc.err); got != tc.want {
				t.Fatalf("flightRunMissing(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestFlightRunReadUsesGetFlightRun(t *testing.T) {
	client := &scriptedAppSQL{
		queryRow: func(query string) mdsql.RowScanner {
			if strings.Contains(query, "MD_GET_FLIGHT_RUN(flight_id := '"+testFlightID+"'::UUID, run_number := 7)") {
				return scannedRow{values: []any{"run-7", "SUCCEEDED", int64(7), int64(1), "2026-09-01T00:00:00Z"}}
			}
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		},
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	var status types.String
	if d := resp.State.GetAttribute(t.Context(), path.Root("status"), &status); d.HasError() {
		t.Fatal(d)
	}
	if status.ValueString() != "SUCCEEDED" {
		t.Fatalf("status = %s, want SUCCEEDED", status)
	}
	if got := client.count("MD_LIST_FLIGHT_RUNS"); got != 0 {
		t.Fatalf("listing queries = %d, want 0 when MD_GET_FLIGHT_RUN finds the run", got)
	}
}

func TestFlightRunReadFallsBackToListingWhenGetFlightRunReportsMissing(t *testing.T) {
	client := &scriptedAppSQL{
		queryRow: func(query string) mdsql.RowScanner {
			if strings.Contains(query, "MD_GET_FLIGHT_RUN(") {
				return scannedRow{err: &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: "Invalid Input Error: Run 7 does not exist"}}
			}
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		},
		queryJSON: func(query string) (string, error) { return runPage(9, 1), nil },
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
		t.Fatalf("read diagnostics: %v, removed: %t", resp.Diagnostics, resp.State.Raw.IsNull())
	}
	if got := client.count("MD_LIST_FLIGHT_RUNS"); got != 1 {
		t.Fatalf("listing queries = %d, want 1", got)
	}
}

func TestFlightRunReadReportsOtherGetFlightRunErrors(t *testing.T) {
	client := &scriptedAppSQL{
		queryRow: func(query string) mdsql.RowScanner {
			return scannedRow{err: &duckdb.Error{Type: duckdb.ErrorTypeInvalidInput, Msg: "Invalid Input Error: permission denied"}}
		},
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for a failure that is not a missing run")
	}
}

func TestFlightRunReadUsesListingWithoutGetFlightRun(t *testing.T) {
	client := &scriptedAppSQL{
		functionOK: func(name string) bool { return name != "md_get_flight_run" },
		queryRow: func(query string) mdsql.RowScanner {
			if strings.Contains(query, "FROM MD_LIST_FLIGHT_RUNS(") && strings.Contains(query, "WHERE run_number = 7") {
				return scannedRow{values: []any{"run-7", "SUCCEEDED", int64(7), int64(1), "2026-09-01T00:00:00Z"}}
			}
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		},
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if got := client.count("MD_GET_FLIGHT_RUN("); got != 0 {
		t.Fatalf("direct queries = %d, want 0 without md_get_flight_run", got)
	}
}

func TestFlightRunLookupRequiresAReadFunction(t *testing.T) {
	client := &scriptedAppSQL{functionOK: func(string) bool { return false }}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	var diags diag.Diagnostics
	if _, ok := res.flightRunLookup(t.Context(), client, &diags); ok || !diags.HasError() {
		t.Fatalf("lookup ok = %t, diagnostics = %v, want an unavailable-function error", ok, diags)
	}
	client.functionOK = func(name string) bool { return name == "md_list_flight_runs" }
	diags = nil
	lookup, ok := res.flightRunLookup(t.Context(), client, &diags)
	if !ok || diags.HasError() || lookup.direct || !lookup.listing {
		t.Fatalf("lookup = %#v, ok = %t, diagnostics = %v", lookup, ok, diags)
	}
}
