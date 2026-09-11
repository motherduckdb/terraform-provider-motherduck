package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
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
				MarkdownDescription: "Stable Terraform ID for this Flight run, derived from the Flight ID and run number.",
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
		},
	}
}

func (r *flightRunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
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
	if waitForFlightRun(plan.WaitForStatus) {
		if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_flight_runs", "motherduck_flight_run") {
			return
		}
	}
	args := map[string]string{"flight_id": sqlbuild.StringLiteral(plan.FlightID.ValueString()) + "::UUID"}
	if !plan.Config.IsNull() {
		config := map[string]string{}
		resp.Diagnostics.Append(plan.Config.ElementsAs(ctx, &config, false)...)
		args["config"] = sqlbuild.MapLiteral(config)
	}
	var runID, status, created string
	var runNumber, version int64
	query := "SELECT run_id::VARCHAR, status, run_number, flight_version, created_at::VARCHAR FROM MD_RUN_FLIGHT" + sqlbuild.NamedArgs(args)
	if err := client.QueryRow(ctx, query).Scan(&runID, &status, &runNumber, &version, &created); err != nil {
		resp.Diagnostics.AddError("Unable to run MotherDuck Flight", err.Error())
		return
	}
	plan.ID = types.StringValue(runID)
	plan.Status = types.StringValue(status)
	plan.RunNumber = types.Int64Value(runNumber)
	plan.FlightVersion = types.Int64Value(version)
	plan.CreatedAt = types.StringValue(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if waitForFlightRun(plan.WaitForStatus) {
		r.waitForFlightRun(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func waitForFlightRun(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != ""
}

func (r *flightRunResource) waitForFlightRun(ctx context.Context, model *flightRunModel, diags *diag.Diagnostics) {
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
		if found := r.readFlightRunStatus(ctx, model, diags); !found && !diags.HasError() {
			continue
		}
		if diags.HasError() {
			return
		}
	}
}

func (r *flightRunResource) readFlightRunStatus(ctx context.Context, model *flightRunModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var runID, status, created string
	var runNumber, version int64
	query := fmt.Sprintf(
		"SELECT run_id::VARCHAR, status, run_number, flight_version, created_at::VARCHAR FROM MD_LIST_FLIGHT_RUNS(flight_id := %s::UUID) WHERE run_number = %d",
		sqlbuild.StringLiteral(model.FlightID.ValueString()),
		model.RunNumber.ValueInt64(),
	)
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(&runID, &status, &runNumber, &version, &created)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Flight run", err.Error())
		return false
	}
	model.ID = types.StringValue(runID)
	model.Status = types.StringValue(status)
	model.RunNumber = types.Int64Value(runNumber)
	model.FlightVersion = types.Int64Value(version)
	model.CreatedAt = types.StringValue(created)
	return true
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
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state flightRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_flight_runs", "motherduck_flight_run") {
		return
	}
	if found := r.readFlightRunStatus(ctx, &state, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *flightRunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *flightRunResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
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
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) && !strings.Contains(strings.ToLower(err.Error()), "terminal") {
		resp.Diagnostics.AddError("Unable to cancel MotherDuck Flight run", err.Error())
	}
}

func (r *flightRunResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError("Import is not supported", "Flight runs are action-like resources and should be recreated instead of imported.")
}
