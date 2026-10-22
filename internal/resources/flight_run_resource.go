package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlfunc"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"
)

type flightRunResource struct{ baseResource }

type flightRunModel struct {
	ID                  types.String `tfsdk:"id"`
	FlightID            types.String `tfsdk:"flight_id"`
	Config              types.Map    `tfsdk:"config"`
	RunNumber           types.Int64  `tfsdk:"run_number"`
	Status              types.String `tfsdk:"status"`
	FlightVersion       types.Int64  `tfsdk:"flight_version"`
	CancelOnDestroy     types.Bool   `tfsdk:"cancel_on_destroy"`
	WaitForStatus       types.String `tfsdk:"wait_for_status"`
	PollIntervalSeconds types.Int64  `tfsdk:"poll_interval_seconds"`
	TimeoutSeconds      types.Int64  `tfsdk:"timeout_seconds"`
	CreatedAt           types.String `tfsdk:"created_at"`
	IsScheduled         types.Bool   `tfsdk:"is_scheduled"`
	StartedAt           types.String `tfsdk:"started_at"`
	EndedAt             types.String `tfsdk:"ended_at"`
	ScheduledAt         types.String `tfsdk:"scheduled_at"`
	CancelledAt         types.String `tfsdk:"cancelled_at"`
	ExitCode            types.Int64  `tfsdk:"exit_code"`
}

const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))

func NewFlightRunResource() resource.Resource { return &flightRunResource{} }

func (r *flightRunResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_flight_run"
}

func (r *flightRunResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Action-like resource that triggers an on-demand MotherDuck Flight run.",
		DeprecationMessage:  "Trigger Flight runs through your deployment pipeline or the MotherDuck CLI/SQL interface. Keep Flight definitions and schedules in Terraform only when Terraform owns their lifecycle.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "MotherDuck run ID for this Flight run.",
			},
			"flight_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Flight ID. Must be a UUID with no leading or trailing whitespace.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          uuidValidators(),
			},
			"config": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional string configuration for this on-demand Flight run. Keys must already exist on the Flight definition and must be valid Flight config names.",
				PlanModifiers:       mapRequiresReplace(),
				Validators:          flightConfigMapValidators(),
			},
			"run_number": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "MotherDuck run number assigned to this Flight run.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Latest Flight run status observed by the provider.",
			},
			"flight_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Flight version used by this run.",
			},
			"cancel_on_destroy": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, provider destroy attempts to cancel this Flight run.",
			},
			"wait_for_status": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional terminal status to wait for after triggering the run. The only supported value is `succeeded`. If the run reaches a failure status, the provider fails the apply without copying potentially sensitive Flight logs into diagnostics. Inspect logs separately with `motherduck_flight_logs`.",
				Validators:          flightRunWaitStatusValidators(),
			},
			"poll_interval_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Polling interval in seconds when `wait_for_status` is set. Defaults to 10 seconds.",
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck Flight run poll interval", 1, maxDurationSeconds)},
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum number of seconds to wait when `wait_for_status` is set. Defaults to 600 seconds.",
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck Flight run timeout", 1, maxDurationSeconds)},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Flight run creation timestamp reported by MotherDuck.",
			},
			"is_scheduled": schema.BoolAttribute{
				Computed:            true,
				PlanModifiers:       boolUseStateForUnknown(),
				MarkdownDescription: "Whether MotherDuck started the run from the Flight schedule. Runs started by this resource are on-demand, so this is `false`.",
			},
			"started_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp when the run started executing, or null while it is queued.",
			},
			"ended_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp when the run finished, or null until it finishes.",
			},
			"scheduled_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp the run was scheduled for, or null for an on-demand run.",
			},
			"cancelled_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp when the run was cancelled, or null when it was not cancelled.",
			},
			"exit_code": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Process exit code of the run, or null until the run finishes.",
			},
		},
	}
}

func (r *flightRunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = sqlfunc.WithCache(ctx)
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_run_flight", "motherduck_flight_run") {
		return
	}
	var plan flightRunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var lookup flightRunLookup
	if waitForFlightRun(plan.WaitForStatus) {
		var ok bool
		if lookup, ok = r.flightRunLookup(ctx, client, &resp.Diagnostics); !ok {
			return
		}
	}
	args := map[string]string{"flight_id": sqlbuild.StringLiteral(plan.FlightID.ValueString()) + "::UUID"}
	if !plan.Config.IsNull() {
		config := map[string]string{}
		resp.Diagnostics.Append(plan.Config.ElementsAs(ctx, &config, false)...)
		args["config"] = sqlbuild.MapLiteral(config)
	}
	var row flightRunRow
	query := "SELECT " + flightRunColumns + " FROM MD_RUN_FLIGHT" + sqlbuild.NamedArgs(args)
	if err := client.QueryRow(ctx, query).Scan(row.scanTargets()...); err != nil {
		resp.Diagnostics.AddError("Unable to run MotherDuck Flight", err.Error())
		return
	}
	plan.setMetadata(row)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if waitForFlightRun(plan.WaitForStatus) {
		r.waitForFlightRun(ctx, &plan, lookup, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func waitForFlightRun(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != ""
}

func (r *flightRunResource) waitForFlightRun(ctx context.Context, model *flightRunModel, lookup flightRunLookup, diags *diag.Diagnostics) {
	wantStatus := normalizeFlightRunStatus(model.WaitForStatus.ValueString())
	pollInterval := time.Duration(int64ValueOrDefault(model.PollIntervalSeconds, 10)) * time.Second
	timeout := time.Duration(int64ValueOrDefault(model.TimeoutSeconds, 600)) * time.Second
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		status := normalizeFlightRunStatus(model.Status.ValueString())
		if status == wantStatus {
			return
		}
		if flightRunFailed(status) {
			detail := fmt.Sprintf("Flight run %d reached status %q while waiting for %q. Inspect logs with the sensitive motherduck_flight_logs data source. Logs are not copied into diagnostics because they can contain credentials or other sensitive output.", model.RunNumber.ValueInt64(), model.Status.ValueString(), wantStatus)
			diags.AddError("MotherDuck Flight run failed", detail)
			return
		}
		if !time.Now().Before(deadline) {
			detail := fmt.Sprintf("Timed out after %d seconds waiting for Flight run %d to reach %q. Last status was %q. Inspect logs with the sensitive motherduck_flight_logs data source. Logs are not copied into diagnostics because they can contain credentials or other sensitive output.", int64ValueOrDefault(model.TimeoutSeconds, 600), model.RunNumber.ValueInt64(), wantStatus, model.Status.ValueString())
			diags.AddError("Timed out waiting for MotherDuck Flight run", detail)
			return
		}

		sleepFor := pollInterval
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor <= 0 {
			continue
		}
		err := retry.Sleep(ctx, sleepFor)
		// Timer and context completion can race. Once our deadline has passed,
		// report the configured timeout above instead of querying with an
		// expired context or describing the timeout as an interruption.
		if !time.Now().Before(deadline) {
			continue
		}
		if err != nil {
			diags.AddError("Interrupted while waiting for MotherDuck Flight run", err.Error())
			return
		}
		if found := r.readFlightRunStatus(ctx, model, lookup, diags); !found && !diags.HasError() {
			continue
		}
		if diags.HasError() {
			return
		}
	}
}

// flightRunLookup records which MotherDuck functions can read a Flight run.
type flightRunLookup struct {
	// direct is true when MD_GET_FLIGHT_RUN reads one run by number.
	direct bool
	// listing is true when MD_LIST_FLIGHT_RUNS lists runs newest first.
	listing bool
}

// flightRunLookup probes the run read functions. It prefers MD_GET_FLIGHT_RUN
// and keeps MD_LIST_FLIGHT_RUNS as the fallback for sessions without it.
func (r *flightRunResource) flightRunLookup(ctx context.Context, client sqlfunc.Exister, diags *diag.Diagnostics) (flightRunLookup, bool) {
	direct, err := sqlFunctionExists(ctx, client, "md_get_flight_run")
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return flightRunLookup{}, false
	}
	listing, err := sqlFunctionExists(ctx, client, "md_list_flight_runs")
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return flightRunLookup{}, false
	}
	if !direct && !listing {
		diags.AddError(
			"MotherDuck SQL function unavailable",
			"Neither md_get_flight_run nor md_list_flight_runs is exposed by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_flight_run resource.",
		)
		return flightRunLookup{}, false
	}
	return flightRunLookup{direct: direct, listing: listing}, true
}

const flightRunColumns = "run_id::VARCHAR, status, run_number, flight_version, created_at::VARCHAR, is_scheduled, started_at::VARCHAR, ended_at::VARCHAR, scheduled_at::VARCHAR, cancelled_at::VARCHAR, exit_code"

// flightRunRow is one run as MD_RUN_FLIGHT, MD_GET_FLIGHT_RUN, and
// MD_LIST_FLIGHT_RUNS report it, in flightRunColumns order.
type flightRunRow struct {
	RunID         string
	Status        string
	RunNumber     int64
	FlightVersion int64
	CreatedAt     string
	IsScheduled   stdsql.NullBool
	StartedAt     stdsql.NullString
	EndedAt       stdsql.NullString
	ScheduledAt   stdsql.NullString
	CancelledAt   stdsql.NullString
	ExitCode      stdsql.NullInt64
}

func (r *flightRunRow) scanTargets() []any {
	return []any{&r.RunID, &r.Status, &r.RunNumber, &r.FlightVersion, &r.CreatedAt, &r.IsScheduled, &r.StartedAt, &r.EndedAt, &r.ScheduledAt, &r.CancelledAt, &r.ExitCode}
}

// setMetadata applies a successful remote result without changing the run's
// configured inputs or normalizing the server's status and timestamp strings.
func (m *flightRunModel) setMetadata(row flightRunRow) {
	m.ID = types.StringValue(row.RunID)
	m.Status = types.StringValue(row.Status)
	m.RunNumber = types.Int64Value(row.RunNumber)
	m.FlightVersion = types.Int64Value(row.FlightVersion)
	m.CreatedAt = types.StringValue(row.CreatedAt)
	if row.IsScheduled.Valid {
		m.IsScheduled = types.BoolValue(row.IsScheduled.Bool)
	} else {
		m.IsScheduled = types.BoolNull()
	}
	m.StartedAt = nullString(row.StartedAt)
	m.EndedAt = nullString(row.EndedAt)
	m.ScheduledAt = nullString(row.ScheduledAt)
	m.CancelledAt = nullString(row.CancelledAt)
	if row.ExitCode.Valid {
		m.ExitCode = types.Int64Value(row.ExitCode.Int64)
	} else {
		m.ExitCode = types.Int64Null()
	}
}

func (r *flightRunResource) readFlightRunStatus(ctx context.Context, model *flightRunModel, lookup flightRunLookup, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var row flightRunRow
	query := fmt.Sprintf(
		"SELECT "+flightRunColumns+" FROM MD_LIST_FLIGHT_RUNS(flight_id := %s::UUID) WHERE run_number = %d",
		sqlbuild.StringLiteral(model.FlightID.ValueString()),
		model.RunNumber.ValueInt64(),
	)
	if lookup.direct {
		query = fmt.Sprintf(
			"SELECT "+flightRunColumns+" FROM MD_GET_FLIGHT_RUN(flight_id := %s::UUID, run_number := %d)",
			sqlbuild.StringLiteral(model.FlightID.ValueString()),
			model.RunNumber.ValueInt64(),
		)
	}
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(row.scanTargets()...)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) || (lookup.direct && flightRunMissing(err)) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Flight run", err.Error())
		return false
	}
	model.setMetadata(row)
	return true
}

// flightRunMissing reports whether MD_GET_FLIGHT_RUN failed because the run
// or its Flight does not exist. MotherDuck reports that as an error rather
// than an empty result, and the error is not always a catalog error.
func flightRunMissing(err error) bool {
	if err == nil {
		return false
	}
	var duckErr *duckdb.Error
	if !errors.As(err, &duckErr) {
		return false
	}
	msg := strings.ToLower(duckErr.Error())
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "not found")
}

func flightRunFailed(status string) bool {
	switch normalizeFlightRunStatus(status) {
	case "failed", "error", "errored", "canceled", "cancelled", "timed_out", "timeout":
		return true
	default:
		return false
	}
}

func normalizeFlightRunStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	return strings.TrimPrefix(status, "run_status_")
}

func int64ValueOrDefault(value types.Int64, defaultValue int64) int64 {
	if value.IsNull() || value.IsUnknown() {
		return defaultValue
	}
	return value.ValueInt64()
}

func (r *flightRunResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	ctx = sqlfunc.WithCache(ctx)
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state flightRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lookup, ok := r.flightRunLookup(ctx, client, &resp.Diagnostics)
	if !ok {
		return
	}
	found := r.readFlightRunStatus(ctx, &state, lookup, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() && lookup.listing {
		// MD_LIST_FLIGHT_RUNS returns one page of the newest runs by default.
		// A scheduled Flight can push an older run out of that page, so walk
		// the listing before treating the run as gone.
		found = r.findFlightRunInListing(ctx, client, &state, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		// Removing the run from state makes the next apply start a new
		// production run. Only do that when the parent Flight is gone too.
		exists, known := r.flightExists(ctx, client, state.FlightID.ValueString(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if !known || exists {
			resp.Diagnostics.AddWarning(
				"MotherDuck Flight run not listed",
				fmt.Sprintf("Flight run %d was not found for Flight %s. Terraform kept the last known run state instead of starting a new run.", state.RunNumber.ValueInt64(), state.FlightID.ValueString()),
			)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
		// MotherDuck reports a deleted Flight and a Flight this principal
		// cannot see with the same not found error. A Flight run grant lets
		// a principal start a Flight it cannot read, so a run this state
		// started can sit under a Flight that only looks deleted. Removing
		// it would start a new production run on the next apply, so keep a
		// run that has a run number and say why it was not refreshed.
		if knownInt64(state.RunNumber) {
			resp.Diagnostics.AddWarning(
				"MotherDuck Flight run cannot be refreshed",
				fmt.Sprintf("Flight %s was not found for the current principal, so Terraform could not refresh Flight run %d and kept its last known state instead of starting a new run. MotherDuck reports a deleted Flight and a Flight the principal cannot see in the same way. A Flight run grant lets a principal start a Flight it cannot read. To refresh the run, give the principal access to the Flight or run Terraform as the Flight owner. If the Flight was deleted, remove this resource from the configuration or from state.", state.FlightID.ValueString(), state.RunNumber.ValueInt64()),
			)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

const (
	flightRunListPageSize = 50
	// Twenty pages cover the latest 1,000 runs and bound refresh cost when a
	// purged run is absent while its Flight still exists.
	flightRunListMaxPages = 20
)

type flightRunListRow struct {
	RunID         string          `json:"run_id"`
	Status        string          `json:"status"`
	RunNumber     json.RawMessage `json:"run_number"`
	FlightVersion json.RawMessage `json:"flight_version"`
	CreatedAt     string          `json:"created_at"`
	IsScheduled   json.RawMessage `json:"is_scheduled"`
	StartedAt     *string         `json:"started_at"`
	EndedAt       *string         `json:"ended_at"`
	ScheduledAt   *string         `json:"scheduled_at"`
	CancelledAt   *string         `json:"cancelled_at"`
	ExitCode      json.RawMessage `json:"exit_code"`
}

// flightRunRow converts a listing row. A missing or malformed optional value
// reads as null, and a malformed flight_version as zero.
func (l flightRunListRow) flightRunRow(runNumber int64) flightRunRow {
	version, _ := jsonInt64(l.FlightVersion)
	row := flightRunRow{RunID: l.RunID, Status: l.Status, RunNumber: runNumber, FlightVersion: version, CreatedAt: l.CreatedAt}
	switch strings.Trim(strings.TrimSpace(string(l.IsScheduled)), `"`) {
	case "true":
		row.IsScheduled = stdsql.NullBool{Bool: true, Valid: true}
	case "false":
		row.IsScheduled = stdsql.NullBool{Valid: true}
	}
	row.StartedAt = jsonNullString(l.StartedAt)
	row.EndedAt = jsonNullString(l.EndedAt)
	row.ScheduledAt = jsonNullString(l.ScheduledAt)
	row.CancelledAt = jsonNullString(l.CancelledAt)
	if exitCode, ok := jsonInt64(l.ExitCode); ok {
		row.ExitCode = stdsql.NullInt64{Int64: exitCode, Valid: true}
	}
	return row
}

func jsonNullString(value *string) stdsql.NullString {
	if value == nil {
		return stdsql.NullString{}
	}
	return stdsql.NullString{String: *value, Valid: true}
}

// findFlightRunInListing pages through MD_LIST_FLIGHT_RUNS, which lists runs
// newest first, until it finds the run, passes older run numbers, or reaches
// the end of the listing.
func (r *flightRunResource) findFlightRunInListing(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, model *flightRunModel, diags *diag.Diagnostics) bool {
	target := model.RunNumber.ValueInt64()
	previousFirstRunID := ""
	for page := 0; page < flightRunListMaxPages; page++ {
		query := fmt.Sprintf(
			`SELECT run_id::VARCHAR AS run_id, status, run_number, flight_version, created_at::VARCHAR AS created_at, is_scheduled, started_at::VARCHAR AS started_at, ended_at::VARCHAR AS ended_at, scheduled_at::VARCHAR AS scheduled_at, cancelled_at::VARCHAR AS cancelled_at, exit_code FROM MD_LIST_FLIGHT_RUNS(flight_id := %s::UUID, "LIMIT" := %d, "OFFSET" := %d)`,
			sqlbuild.StringLiteral(model.FlightID.ValueString()),
			flightRunListPageSize,
			page*flightRunListPageSize,
		)
		var raw string
		err := retry.SQL(ctx, func() error {
			var queryErr error
			raw, queryErr = client.QueryRowsJSON(ctx, query)
			return queryErr
		})
		if isNotFound(err) {
			return false
		}
		if err != nil {
			diags.AddError("Unable to list MotherDuck Flight runs", err.Error())
			return false
		}
		var rows []flightRunListRow
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			diags.AddError("Unable to parse MotherDuck Flight runs", err.Error())
			return false
		}
		if len(rows) == 0 || rows[0].RunID == previousFirstRunID {
			return false
		}
		previousFirstRunID = rows[0].RunID
		olderSeen := false
		for _, row := range rows {
			runNumber, ok := jsonInt64(row.RunNumber)
			if !ok {
				diags.AddError("Unable to parse MotherDuck Flight runs", "MD_LIST_FLIGHT_RUNS returned a run without an integer run_number.")
				return false
			}
			if runNumber == target {
				model.setMetadata(row.flightRunRow(runNumber))
				return true
			}
			if runNumber < target {
				olderSeen = true
			}
		}
		if olderSeen || len(rows) < flightRunListPageSize {
			return false
		}
	}
	return false
}

// flightExists reports whether the parent Flight exists. known is false when
// the session cannot answer, so callers stay on the cautious path.
func (r *flightRunResource) flightExists(ctx context.Context, client providerctx.SQLClient, flightID string, diags *diag.Diagnostics) (exists bool, known bool) {
	available, err := sqlFunctionExists(ctx, client, "md_get_flight")
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return false, false
	}
	if !available {
		return false, false
	}
	var id string
	query := "SELECT flight_id::VARCHAR FROM MD_GET_FLIGHT(flight_id := " + sqlbuild.StringLiteral(flightID) + "::UUID)"
	err = retry.SQL(ctx, func() error { return client.QueryRow(ctx, query).Scan(&id) })
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false, true
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Flight", err.Error())
		return false, false
	}
	return true, true
}

func jsonInt64(raw json.RawMessage) (int64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	value, err := strconv.ParseInt(text, 10, 64)
	return value, err == nil
}

func (r *flightRunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	ctx = sqlfunc.WithCache(ctx)
	var plan, state flightRunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.RunNumber = state.RunNumber
	plan.Status = state.Status
	plan.FlightVersion = state.FlightVersion
	plan.CreatedAt = state.CreatedAt
	plan.IsScheduled = state.IsScheduled
	plan.StartedAt = state.StartedAt
	plan.EndedAt = state.EndedAt
	plan.ScheduledAt = state.ScheduledAt
	plan.CancelledAt = state.CancelledAt
	plan.ExitCode = state.ExitCode
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *flightRunResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	ctx = sqlfunc.WithCache(ctx)
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state flightRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.CancelOnDestroy.IsNull() || !state.CancelOnDestroy.ValueBool() {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_cancel_flight_run", "motherduck_flight_run") {
		return
	}
	query := fmt.Sprintf("CALL MD_CANCEL_FLIGHT_RUN(flight_id := %s::UUID, run_number := %d)", sqlbuild.StringLiteral(state.FlightID.ValueString()), state.RunNumber.ValueInt64())
	err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) })
	switch {
	case err == nil || strings.Contains(strings.ToLower(err.Error()), "terminal"):
	case isNotFound(err):
		// A principal holding only a Flight run grant cannot see the Flight,
		// so MotherDuck reports it as not found and does not cancel the run.
		resp.Diagnostics.AddWarning(
			"MotherDuck Flight run not cancelled",
			fmt.Sprintf("Flight %s was not found for the current principal, so Terraform removed Flight run %d from state without cancelling it. If the Flight still exists, cancel the run as its owner.", state.FlightID.ValueString(), state.RunNumber.ValueInt64()),
		)
	default:
		resp.Diagnostics.AddError("Unable to cancel MotherDuck Flight run", err.Error())
	}
}

func (r *flightRunResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError("Import is not supported", "Flight runs are action-like resources and should be recreated instead of imported.")
}
