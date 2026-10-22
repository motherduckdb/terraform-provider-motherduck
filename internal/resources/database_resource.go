package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	resourceTimeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"
)

type databaseResource struct{ baseResource }

type databaseModel struct {
	ID                    types.String           `tfsdk:"id"`
	Name                  types.String           `tfsdk:"name"`
	Transient             types.Bool             `tfsdk:"transient"`
	SnapshotRetentionDays types.Int64            `tfsdk:"snapshot_retention_days"`
	DatabaseType          types.String           `tfsdk:"database_type"`
	DataPath              types.String           `tfsdk:"data_path"`
	Encrypted             types.Bool             `tfsdk:"encrypted"`
	DataInliningRowLimit  types.Int64            `tfsdk:"data_inlining_row_limit"`
	Iceberg               types.Object           `tfsdk:"iceberg"`
	UUID                  types.String           `tfsdk:"uuid"`
	CreatedTS             types.String           `tfsdk:"created_ts"`
	Timeouts              resourceTimeouts.Value `tfsdk:"timeouts"`
}

func NewDatabaseResource() resource.Resource { return &databaseResource{} }

func (r *databaseResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (r *databaseResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck database using public SQL.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Database resource ID. This is the database name.",
				PlanModifiers:       identifierID("name"),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database name. Must be a single MotherDuck SQL identifier. MotherDuck compares database names case-insensitively, so changing only the letter case updates state in place instead of replacing the database.",
				PlanModifiers:       identifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"transient": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Whether to create the database as transient. Omit for the MotherDuck default.",
				PlanModifiers:       boolRequiresReplaceIfConfigured(),
			},
			"snapshot_retention_days": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Historical snapshot retention in days. Must be nonnegative. MotherDuck enforces any account-specific upper bound.",
				PlanModifiers:       int64UseStateForUnknown(),
				Validators:          snapshotRetentionValidators(),
			},
			"database_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "`default`, `ducklake`, or `iceberg`. Use `transient = true` for transient databases. An `iceberg` database registers an existing Iceberg REST catalog and needs the `iceberg` block.",
				PlanModifiers:       stringRequiresReplaceIfConfigured(),
				Validators:          databaseTypeValidators(),
			},
			"data_path": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "DuckLake-only non-empty data path used when creating the database.",
				PlanModifiers:       stringRequiresReplace(),
			},
			"encrypted": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "DuckLake-only. When true, emits the `ENCRYPTED` database option at creation. When false, omits the option.",
				PlanModifiers:       boolRequiresReplace(),
			},
			"data_inlining_row_limit": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "DuckLake-only. Maximum number of rows an insert can keep inline in the DuckLake catalog instead of writing a data file. Emits the `DATA_INLINING_ROW_LIMIT` database option at creation. MotherDuck accepts this option only when creating a DuckLake database and does not report it back, so Terraform keeps the configured value. Changing a recorded value replaces the database. Adding it to a database whose state has no recorded value, such as an imported database or one created by an earlier provider release, records the value in place without SQL and does not verify it. Removing it keeps the database.",
				PlanModifiers:       []planmodifier.Int64{databaseDataInliningRowLimitPlanModifier{}},
				Validators:          []validator.Int64{tfvalidators.Int64Min("MotherDuck DuckLake data inlining row limit", 0)},
			},
			"iceberg": databaseIcebergAttribute(),
			"uuid": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Database UUID reported by MotherDuck.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       stringUseStateForUnknown(),
				MarkdownDescription: "Database creation timestamp reported by MotherDuck.",
			},
			"timeouts": resourceTimeoutsAttribute(ctx, resourceTimeouts.Opts{
				Create:            true,
				Read:              true,
				Update:            true,
				Delete:            true,
				CreateDescription: "Optional timeout for creating a MotherDuck database, including DuckLake database setup.",
				ReadDescription:   "Optional timeout for refreshing MotherDuck database metadata.",
				UpdateDescription: "Optional timeout for updating database options such as snapshot retention.",
				DeleteDescription: "Optional timeout for dropping a MotherDuck database.",
			}),
		},
	}
}

// databaseDataInliningRowLimitPlanModifier replaces a DuckLake database only
// when a recorded data_inlining_row_limit changes to a different known value.
// MotherDuck cannot report or change the option after creation, so a value
// added to state that has none, for example after import or an upgrade from a
// release without the attribute, is recorded in place instead of dropping the
// database and its data.
type databaseDataInliningRowLimitPlanModifier struct{}

func (databaseDataInliningRowLimitPlanModifier) Description(context.Context) string {
	return "Replaces the database only when a recorded value changes to a different configured value."
}

func (m databaseDataInliningRowLimitPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (databaseDataInliningRowLimitPlanModifier) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if req.ConfigValue.IsNull() || req.ConfigValue.Equal(req.StateValue) {
		return
	}
	if req.StateValue.IsNull() {
		resp.Diagnostics.AddAttributeWarning(req.Path, "DuckLake data inlining row limit recorded without verification",
			"Terraform will record `data_inlining_row_limit` for this existing database without running SQL. MotherDuck cannot report or change this option after creation, so the recorded value is not verified against the live database.")
		return
	}
	if req.ConfigValue.IsUnknown() || req.StateValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = true
}

func (r *databaseResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config databaseModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateDatabaseConfig(ctx, config, &resp.Diagnostics)
}

func validateDatabaseConfig(ctx context.Context, config databaseModel, diags *diag.Diagnostics) {
	databaseType := ""
	databaseTypeKnown := !config.DatabaseType.IsUnknown()
	if !config.DatabaseType.IsNull() && !config.DatabaseType.IsUnknown() {
		databaseType = strings.ToLower(strings.TrimSpace(config.DatabaseType.ValueString()))
	}
	if !config.DataPath.IsNull() && !config.DataPath.IsUnknown() {
		if strings.TrimSpace(config.DataPath.ValueString()) == "" {
			diags.AddAttributeError(path.Root("data_path"), "Invalid MotherDuck database configuration", "`data_path` must be omitted or set to a non-empty DuckLake storage path.")
		} else if databaseTypeKnown && databaseType != "ducklake" {
			diags.AddAttributeError(path.Root("data_path"), "Invalid MotherDuck database configuration", "`data_path` is only valid when `database_type = \"ducklake\"`.")
		}
	}
	if databaseTypeKnown && !config.Transient.IsNull() && !config.Transient.IsUnknown() && config.Transient.ValueBool() && databaseType == "ducklake" {
		diags.AddAttributeError(path.Root("transient"), "Invalid MotherDuck database configuration", "`transient = true` cannot be combined with `database_type = \"ducklake\"`.")
	}
	if databaseTypeKnown && !config.Encrypted.IsNull() && !config.Encrypted.IsUnknown() && databaseType != "ducklake" {
		diags.AddAttributeError(path.Root("encrypted"), "Invalid MotherDuck database configuration", "`encrypted` is only valid when `database_type = \"ducklake\"`.")
	}
	if databaseTypeKnown && !config.DataInliningRowLimit.IsNull() && !config.DataInliningRowLimit.IsUnknown() && databaseType != "ducklake" {
		diags.AddAttributeError(path.Root("data_inlining_row_limit"), "Invalid MotherDuck database configuration", "`data_inlining_row_limit` is only valid when `database_type = \"ducklake\"`.")
	}
	validateDatabaseIcebergConfig(ctx, config, databaseType, databaseTypeKnown, diags)
}

func (r *databaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := timeoutContext(ctx, plan.Timeouts, "create", 30*time.Minute, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	query := databaseCreateStatement(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck database", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	plan.UUID = types.StringNull()
	plan.CreatedTS = types.StringNull()
	if plan.Transient.IsUnknown() {
		plan.Transient = types.BoolNull()
	}
	if plan.DatabaseType.IsUnknown() {
		plan.DatabaseType = types.StringNull()
	}
	if plan.SnapshotRetentionDays.IsUnknown() {
		plan.SnapshotRetentionDays = types.Int64Null()
	}
	// Preserve configuration and a known identity before read-back, including
	// the configured timeout needed to clean up a failed creation.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.readDatabase(ctx, &plan, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.AddError("Unable to read MotherDuck database", "Database was created but was not visible in MD_INFORMATION_SCHEMA.DATABASES.")
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// databaseCreateStatement builds the CREATE DATABASE statement for a plan.
// Create-only options appear only when configured.
func databaseCreateStatement(ctx context.Context, plan databaseModel, diags *diag.Diagnostics) string {
	options := make([]string, 0)
	if !plan.Transient.IsNull() && plan.Transient.ValueBool() {
		options = append(options, "TRANSIENT")
	}
	if !plan.DatabaseType.IsNull() && strings.EqualFold(plan.DatabaseType.ValueString(), "ducklake") {
		options = append(options, "TYPE DUCKLAKE")
	}
	if !plan.DatabaseType.IsNull() && strings.EqualFold(plan.DatabaseType.ValueString(), "iceberg") {
		options = append(options, "TYPE ICEBERG")
		options = append(options, databaseIcebergCreateOptions(databaseIcebergOptions(ctx, plan.Iceberg, diags))...)
		if diags.HasError() {
			return ""
		}
	}
	if !plan.DataPath.IsNull() && plan.DataPath.ValueString() != "" {
		options = append(options, "DATA_PATH "+sqlbuild.StringLiteral(plan.DataPath.ValueString()))
	}
	if !plan.Encrypted.IsNull() && plan.Encrypted.ValueBool() {
		options = append(options, "ENCRYPTED")
	}
	if knownInt64(plan.DataInliningRowLimit) {
		options = append(options, fmt.Sprintf("DATA_INLINING_ROW_LIMIT %d", plan.DataInliningRowLimit.ValueInt64()))
	}
	if knownInt64(plan.SnapshotRetentionDays) {
		options = append(options, fmt.Sprintf("SNAPSHOT_RETENTION_DAYS %d", plan.SnapshotRetentionDays.ValueInt64()))
	}
	return "CREATE DATABASE " + sqlbuild.QuoteIdentifier(plan.Name.ValueString()) + sqlbuild.Options(options...)
}

func (r *databaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := timeoutContext(ctx, state.Timeouts, "read", 5*time.Minute, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readDatabase(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *databaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state databaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := timeoutContext(ctx, plan.Timeouts, "update", 30*time.Minute, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	statements := make([]string, 0, 2)
	if knownInt64(plan.SnapshotRetentionDays) && !plan.SnapshotRetentionDays.Equal(state.SnapshotRetentionDays) {
		statements = append(statements, fmt.Sprintf("ALTER DATABASE %s SET SNAPSHOT_RETENTION_DAYS = %d", sqlbuild.QuoteIdentifier(plan.Name.ValueString()), plan.SnapshotRetentionDays.ValueInt64()))
	}
	icebergAssignments := databaseIcebergAlterAssignments(
		databaseIcebergOptions(ctx, plan.Iceberg, &resp.Diagnostics),
		databaseIcebergOptions(ctx, state.Iceberg, &resp.Diagnostics),
	)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(icebergAssignments) > 0 {
		statements = append(statements, "ALTER DATABASE "+sqlbuild.QuoteIdentifier(plan.Name.ValueString())+" SET "+strings.Join(icebergAssignments, ", "))
	}
	if len(statements) > 0 {
		if err := client.AttachDatabase(ctx, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
			return
		}
	}
	for _, query := range statements {
		if err := client.Exec(ctx, query); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck database", err.Error())
			return
		}
	}
	if knownInt64(plan.DataInliningRowLimit) && knownInt64(state.DataInliningRowLimit) && !plan.DataInliningRowLimit.Equal(state.DataInliningRowLimit) {
		// The configured value was unknown at plan time, so the database was
		// not replaced. Record it, but say it was not applied.
		resp.Diagnostics.AddAttributeWarning(path.Root("data_inlining_row_limit"), "DuckLake data inlining row limit recorded without verification",
			"Terraform recorded a new `data_inlining_row_limit` without replacing the database because the value was unknown during planning. MotherDuck cannot change this option after creation, so the live database keeps its original limit.")
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	found := r.readDatabase(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck database", "Database was updated but was not visible in MD_INFORMATION_SCHEMA.DATABASES.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *databaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state databaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := timeoutContext(ctx, state.Timeouts, "delete", 30*time.Minute, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	resetDefaultDatabaseIfCurrent(ctx, client, state.Name.ValueString())
	query := "DROP DATABASE IF EXISTS " + sqlbuild.QuoteIdentifier(state.Name.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil {
		resp.Diagnostics.AddError("Unable to drop MotherDuck database", err.Error())
	}
}

func (r *databaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *databaseResource) readDatabase(ctx context.Context, model *databaseModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var uuid, createdTS, dbType stdsql.NullString
	var transient stdsql.NullBool
	var retention stdsql.NullString
	err := retry.SQL(ctx, func() error {
		// MotherDuck compares database names case-insensitively and reports the
		// spelling the database was created with. State keeps the configured
		// spelling.
		return client.QueryRow(ctx, `SELECT uuid::VARCHAR, created_ts::VARCHAR, transient, historical_snapshot_retention::VARCHAR, type FROM MD_INFORMATION_SCHEMA.DATABASES WHERE lower(name) = lower(?)`, model.Name.ValueString()).Scan(&uuid, &createdTS, &transient, &retention, &dbType)
	})
	if errors.Is(err, stdsql.ErrNoRows) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck database", err.Error())
		return false
	}
	applyDatabaseRow(model, uuid, createdTS, dbType, transient, retention)
	return true
}

// applyDatabaseRow maps an MD_INFORMATION_SCHEMA.DATABASES row onto the
// database model. Every attribute is assigned unconditionally so that NULL
// live values (for example the infinite DuckLake snapshot retention) become
// known nulls instead of leaving unknown or stale values in state.
func applyDatabaseRow(model *databaseModel, uuid, createdTS, dbType stdsql.NullString, transient stdsql.NullBool, retention stdsql.NullString) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.UUID = nullString(uuid)
	model.CreatedTS = nullString(createdTS)
	if transient.Valid {
		model.Transient = types.BoolValue(transient.Bool)
	} else {
		model.Transient = types.BoolNull()
	}
	model.DatabaseType = lowerNullString(dbType)
	if retention.Valid {
		model.SnapshotRetentionDays = intervalDays(retention.String)
	} else {
		model.SnapshotRetentionDays = types.Int64Null()
	}
}

func intervalDays(value string) types.Int64 {
	fields := strings.Fields(value)
	if len(fields) >= 2 && strings.HasPrefix(strings.ToLower(fields[1]), "day") {
		var days int64
		if _, err := fmt.Sscan(fields[0], &days); err == nil {
			return types.Int64Value(days)
		}
	}
	if strings.HasPrefix(value, "00:00:00") {
		return types.Int64Value(0)
	}
	return types.Int64Null()
}
