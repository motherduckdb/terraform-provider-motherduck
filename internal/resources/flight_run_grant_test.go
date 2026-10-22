package resources

import (
	"context"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// flightRunTestRow returns one finished on-demand run in flightRunColumns order.
func flightRunTestRow(runID, status string, runNumber int64) []any {
	return []any{runID, status, runNumber, int64(1), "2026-09-01T00:00:00Z", false, "2026-09-01T00:00:05Z", "2026-09-01T00:01:00Z", nil, nil, int64(0)}
}

// flightNotVisibleError is how MotherDuck answers a principal that can run a
// Flight through a run grant but cannot see it, and also a deleted Flight.
func flightNotVisibleError() error {
	return &duckdb.Error{Type: duckdb.ErrorTypeCatalog, Msg: `Catalog Error: Flight "` + testFlightID + `" not found`}
}

func flightNotVisibleSQL() *scriptedAppSQL {
	return &scriptedAppSQL{
		queryRow: func(string) mdsql.RowScanner { return scannedRow{err: flightNotVisibleError()} },
		queryJSON: func(string) (string, error) {
			return "", flightNotVisibleError()
		},
	}
}

func TestFlightRunReadKeepsRunWhenParentFlightIsNotVisible(t *testing.T) {
	client := flightNotVisibleSQL()
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("a run started by this state was removed, so the next apply would start another run")
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("want one warning, got %v", resp.Diagnostics)
	}
	detail := resp.Diagnostics.Warnings()[0].Detail()
	for _, want := range []string{testFlightID, "run 7", "run grant", "remove this resource"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("warning detail %q does not mention %q", detail, want)
		}
	}
	if got := client.count("MD_GET_FLIGHT("); got != 1 {
		t.Fatalf("parent Flight lookups = %d, want 1", got)
	}
}

func TestFlightRunReadRemovesRunWithoutRunNumberWhenParentFlightIsGone(t *testing.T) {
	client := flightNotVisibleSQL()
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		ID:            types.StringValue("run-unknown"),
		FlightID:      types.StringValue(testFlightID),
		Config:        types.MapNull(types.StringType),
		RunNumber:     types.Int64Null(),
		Status:        types.StringNull(),
		FlightVersion: types.Int64Null(),
		CreatedAt:     types.StringNull(),
	}
	state := resourceStateWithFlightRunModel(t, res, &model)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("a run without a run number has nothing to keep and should leave state when its Flight is gone")
	}
}

func TestFlightRunDeleteWarnsWhenCancelCannotSeeFlight(t *testing.T) {
	client := &scriptedAppSQL{exec: func(query string) error {
		if strings.HasPrefix(query, "CALL MD_CANCEL_FLIGHT_RUN(") {
			return flightNotVisibleError()
		}
		return nil
	}}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		ID:              types.StringValue("run-7"),
		FlightID:        types.StringValue(testFlightID),
		Config:          types.MapNull(types.StringType),
		RunNumber:       types.Int64Value(7),
		CancelOnDestroy: types.BoolValue(true),
	}
	state := resourceStateWithFlightRunModel(t, res, &model)
	resp := resource.DeleteResponse{State: tfsdk.State{Schema: state.Schema, Raw: state.Raw}}
	res.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() != 1 || resp.Diagnostics.Warnings()[0].Summary() != "MotherDuck Flight run not cancelled" {
		t.Fatalf("want one not-cancelled warning, got %v", resp.Diagnostics)
	}
}

func TestFlightRunListRowConvertsOptionalRunTiming(t *testing.T) {
	row := flightRunListRow{
		RunID:         "run-3",
		Status:        "FAILED",
		FlightVersion: []byte(`2`),
		IsScheduled:   []byte(`false`),
		ExitCode:      []byte(`1`),
	}
	ended := "2026-09-01 00:01:00+00"
	row.EndedAt = &ended
	got := row.flightRunRow(3)
	if got.RunNumber != 3 || got.FlightVersion != 2 || !got.IsScheduled.Valid || got.IsScheduled.Bool || got.StartedAt.Valid || got.EndedAt.String != ended || !got.ExitCode.Valid || got.ExitCode.Int64 != 1 {
		t.Fatalf("converted row = %#v", got)
	}
	missing := flightRunListRow{RunID: "run-4"}.flightRunRow(4)
	if missing.IsScheduled.Valid || missing.ExitCode.Valid || missing.CancelledAt.Valid {
		t.Fatalf("missing optional values should read as null: %#v", missing)
	}
}
