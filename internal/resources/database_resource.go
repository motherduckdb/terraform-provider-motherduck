package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"strings"
	"time"

	resourceTimeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
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
				PlanModifiers:       stringUseStateForUnknown(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database name. Must be a single MotherDuck SQL identifier.",
				PlanModifiers:       stringRequiresReplace(),
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
				MarkdownDescription: "`default` or `ducklake`. Use `transient = true` for transient databases.",
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
			"uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Database UUID reported by MotherDuck.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
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

func (r *databaseResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config databaseModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateDatabaseConfig(config, &resp.Diagnostics)
}

func validateDatabaseConfig(config databaseModel, diags *diag.Diagnostics) {
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
	options := make([]string, 0)
	if !plan.Transient.IsNull() && plan.Transient.ValueBool() {
		options = append(options, "TRANSIENT")
	}
	if !plan.DatabaseType.IsNull() && strings.EqualFold(plan.DatabaseType.ValueString(), "ducklake") {
		options = append(options, "TYPE DUCKLAKE")
	}
	if !plan.DataPath.IsNull() && plan.DataPath.ValueString() != "" {
		options = append(options, "DATA_PATH "+sqlbuild.StringLiteral(plan.DataPath.ValueString()))
	}
	if !plan.Encrypted.IsNull() && plan.Encrypted.ValueBool() {
		options = append(options, "ENCRYPTED")
	}
	if knownInt64(plan.SnapshotRetentionDays) {
		options = append(options, fmt.Sprintf("SNAPSHOT_RETENTION_DAYS %d", plan.SnapshotRetentionDays.ValueInt64()))
	}
	query := "CREATE DATABASE " + sqlbuild.QuoteIdentifier(plan.Name.ValueString()) + sqlbuild.Options(options...)
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
	if knownInt64(plan.SnapshotRetentionDays) && !plan.SnapshotRetentionDays.Equal(state.SnapshotRetentionDays) {
		if err := client.AttachDatabase(ctx, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
			return
		}
		query := fmt.Sprintf("ALTER DATABASE %s SET SNAPSHOT_RETENTION_DAYS = %d", sqlbuild.QuoteIdentifier(plan.Name.ValueString()), plan.SnapshotRetentionDays.ValueInt64())
		if err := client.Exec(ctx, query); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck database", err.Error())
			return
		}
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
		return client.QueryRow(ctx, `SELECT uuid::VARCHAR, created_ts::VARCHAR, transient, historical_snapshot_retention::VARCHAR, type FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?`, model.Name.ValueString()).Scan(&uuid, &createdTS, &transient, &retention, &dbType)
	})
	if err == stdsql.ErrNoRows {
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
