package resources

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

const largeFlightRunMetadataValue int64 = 9223372036854770000

func TestFlightRunReadUpdatesAllMetadataAndPreservesConfiguration(t *testing.T) {
	client := &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		if strings.Contains(query, "MD_GET_FLIGHT_RUN(") {
			return scannedRow{values: []any{
				"run-with-empty-created-at",
				"RUN_STATUS_SUCCEEDED",
				largeFlightRunMetadataValue,
				largeFlightRunMetadataValue - 1,
				"",
			}}
		}
		return scannedRow{err: errors.New("unexpected query")}
	}}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		ID:                  types.StringValue("old-run"),
		FlightID:            types.StringValue(testFlightID),
		Config:              types.MapNull(types.StringType),
		RunNumber:           types.Int64Value(7),
		Status:              types.StringUnknown(),
		FlightVersion:       types.Int64Null(),
		CancelOnDestroy:     types.BoolValue(true),
		WaitForStatus:       types.StringUnknown(),
		PollIntervalSeconds: types.Int64Null(),
		TimeoutSeconds:      types.Int64Value(17),
		CreatedAt:           types.StringNull(),
	}
	state := resourceStateWithFlightRunModel(t, res, &model)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	var got flightRunModel
	if d := resp.State.Get(t.Context(), &got); d.HasError() {
		t.Fatal(d)
	}
	assertFlightRunMetadata(t, got, "run-with-empty-created-at", "RUN_STATUS_SUCCEEDED", largeFlightRunMetadataValue, largeFlightRunMetadataValue-1, "")
	if !got.Config.IsNull() || !got.WaitForStatus.IsUnknown() || !got.PollIntervalSeconds.IsNull() || got.TimeoutSeconds.ValueInt64() != 17 || !got.CancelOnDestroy.ValueBool() {
		t.Fatalf("configured state changed during read: %#v", got)
	}
}

func TestFlightRunReadPaginatedMetadataUsesZeroForMalformedVersion(t *testing.T) {
	client := &scriptedAppSQL{
		functionOK: func(name string) bool { return name != "md_get_flight_run" },
		queryRow:   func(string) mdsql.RowScanner { return scannedRow{err: sql.ErrNoRows} },
		queryJSON: func(query string) (string, error) {
			if strings.Contains(query, `"OFFSET" := 0)`) {
				return runPage(57, 8), nil
			}
			if strings.Contains(query, `"OFFSET" := 50)`) {
				return `[{"run_id":"run-7-empty-created-at","status":"RUN_STATUS_RUNNING","run_number":7,"flight_version":"malformed","created_at":""}]`, nil
			}
			return "", errors.New("unexpected listing query")
		},
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		ID:                  types.StringValue("old-run"),
		FlightID:            types.StringValue(testFlightID),
		Config:              types.MapNull(types.StringType),
		RunNumber:           types.Int64Value(7),
		Status:              types.StringValue("RUNNING"),
		FlightVersion:       types.Int64Value(1),
		CancelOnDestroy:     types.BoolValue(false),
		WaitForStatus:       types.StringValue("succeeded"),
		PollIntervalSeconds: types.Int64Value(3),
		TimeoutSeconds:      types.Int64Value(17),
		CreatedAt:           types.StringValue("old-created-at"),
	}
	state := resourceStateWithFlightRunModel(t, res, &model)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	var got flightRunModel
	if d := resp.State.Get(t.Context(), &got); d.HasError() {
		t.Fatal(d)
	}
	assertFlightRunMetadata(t, got, "run-7-empty-created-at", "RUN_STATUS_RUNNING", 7, 0, "")
	if !got.Config.IsNull() || got.WaitForStatus.ValueString() != "succeeded" || got.PollIntervalSeconds.ValueInt64() != 3 || got.TimeoutSeconds.ValueInt64() != 17 || got.CancelOnDestroy.ValueBool() {
		t.Fatalf("configured state changed during paginated read: %#v", got)
	}
}

func TestFlightRunReadScanFailureRetainsModelAndDiagnostic(t *testing.T) {
	client := &scriptedAppSQL{queryRow: func(string) mdsql.RowScanner { return mutatingFlightRunRow{} }}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		ID:                  types.StringValue("original-run"),
		FlightID:            types.StringValue(testFlightID),
		Config:              types.MapNull(types.StringType),
		RunNumber:           types.Int64Value(7),
		Status:              types.StringUnknown(),
		FlightVersion:       types.Int64Null(),
		CancelOnDestroy:     types.BoolValue(true),
		WaitForStatus:       types.StringUnknown(),
		PollIntervalSeconds: types.Int64Null(),
		TimeoutSeconds:      types.Int64Value(17),
		CreatedAt:           types.StringNull(),
	}
	original := model
	var diags diag.Diagnostics
	if found := res.readFlightRunStatus(t.Context(), &model, flightRunLookup{direct: true}, &diags); found {
		t.Fatal("scan failure reported a found run")
	}
	if len(diags) != 1 || diags[0].Summary() != "Unable to read MotherDuck Flight run" {
		t.Fatalf("diagnostics = %v, want one read error", diags)
	}
	if !model.ID.Equal(original.ID) || !model.FlightID.Equal(original.FlightID) || !model.Config.Equal(original.Config) || !model.RunNumber.Equal(original.RunNumber) || !model.Status.Equal(original.Status) || !model.FlightVersion.Equal(original.FlightVersion) || !model.CancelOnDestroy.Equal(original.CancelOnDestroy) || !model.WaitForStatus.Equal(original.WaitForStatus) || !model.PollIntervalSeconds.Equal(original.PollIntervalSeconds) || !model.TimeoutSeconds.Equal(original.TimeoutSeconds) || !model.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("model changed after partial scan failure: %#v", model)
	}
}

type mutatingFlightRunRow struct{}

func (mutatingFlightRunRow) Scan(dest ...any) error {
	*dest[0].(*string) = "mutated-run"
	*dest[1].(*string) = "MUTATED"
	*dest[2].(*int64) = 99
	*dest[3].(*int64) = 98
	*dest[4].(*string) = "mutated-created-at"
	return errors.New("scan failed after mutating destinations")
}

func resourceStateWithFlightRunModel(t *testing.T, res *flightRunResource, model *flightRunModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema(t, res)}
	if d := state.Set(t.Context(), model); d.HasError() {
		t.Fatal(d)
	}
	return state
}

func assertFlightRunMetadata(t *testing.T, model flightRunModel, id, status string, runNumber, version int64, created string) {
	t.Helper()
	if model.ID.IsNull() || model.ID.IsUnknown() || model.ID.ValueString() != id || model.Status.IsNull() || model.Status.IsUnknown() || model.Status.ValueString() != status || model.RunNumber.IsNull() || model.RunNumber.IsUnknown() || model.RunNumber.ValueInt64() != runNumber || model.FlightVersion.IsNull() || model.FlightVersion.IsUnknown() || model.FlightVersion.ValueInt64() != version || model.CreatedAt.IsNull() || model.CreatedAt.IsUnknown() || model.CreatedAt.ValueString() != created {
		t.Fatalf("metadata = id %q status %q run_number %d flight_version %d created_at %q", model.ID.ValueString(), model.Status.ValueString(), model.RunNumber.ValueInt64(), model.FlightVersion.ValueInt64(), model.CreatedAt.ValueString())
	}
}
