package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	resourceTimeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &databaseResource{}
	_ resource.ResourceWithConfigure      = &databaseResource{}
	_ resource.ResourceWithImportState    = &databaseResource{}
	_ resource.ResourceWithValidateConfig = &databaseResource{}
	_ resource.Resource                   = &schemaResource{}
	_ resource.ResourceWithConfigure      = &schemaResource{}
	_ resource.ResourceWithImportState    = &schemaResource{}
	_ resource.Resource                   = &tableResource{}
	_ resource.ResourceWithConfigure      = &tableResource{}
	_ resource.ResourceWithImportState    = &tableResource{}
	_ resource.ResourceWithValidateConfig = &tableResource{}
	_ resource.Resource                   = &viewResource{}
	_ resource.ResourceWithConfigure      = &viewResource{}
	_ resource.ResourceWithImportState    = &viewResource{}
	_ resource.ResourceWithValidateConfig = &viewResource{}
	_ resource.Resource                   = &secretResource{}
	_ resource.ResourceWithConfigure      = &secretResource{}
	_ resource.ResourceWithImportState    = &secretResource{}
	_ resource.ResourceWithModifyPlan     = &secretResource{}
	_ resource.ResourceWithValidateConfig = &secretResource{}
	_ resource.Resource                   = &shareResource{}
	_ resource.ResourceWithConfigure      = &shareResource{}
	_ resource.ResourceWithImportState    = &shareResource{}
	_ resource.ResourceWithValidateConfig = &shareResource{}
	_ resource.Resource                   = &shareGrantResource{}
	_ resource.ResourceWithConfigure      = &shareGrantResource{}
	_ resource.ResourceWithImportState    = &shareGrantResource{}
	_ resource.Resource                   = &snapshotResource{}
	_ resource.ResourceWithConfigure      = &snapshotResource{}
	_ resource.ResourceWithImportState    = &snapshotResource{}
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
				MarkdownDescription: "Historical snapshot retention in days. Must be nonnegative; MotherDuck enforces any account-specific upper bound.",
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
				MarkdownDescription: "DuckLake-only. When true, emits the `ENCRYPTED` database option at creation; when false, omits the option.",
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
	found := r.readDatabase(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck database", "Database was created but was not visible in MD_INFORMATION_SCHEMA.DATABASES.")
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

type schemaResource struct{ baseResource }

type schemaModel struct {
	ID              types.String `tfsdk:"id"`
	Database        types.String `tfsdk:"database"`
	Name            types.String `tfsdk:"name"`
	CascadeOnDelete types.Bool   `tfsdk:"cascade_on_delete"`
}

func NewSchemaResource() resource.Resource { return &schemaResource{} }

func (r *schemaResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_schema"
}

func (r *schemaResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a schema in a MotherDuck database.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Schema resource ID in `<database>.<schema>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the schema.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"cascade_on_delete": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, uses `DROP SCHEMA ... CASCADE` during destroy. The default is restrictive and fails if the schema contains unmanaged objects.",
			},
		},
	}
}

func (r *schemaResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan schemaModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.Database.ValueString() + "." + plan.Name.ValueString()
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	if err := client.Exec(ctx, "CREATE SCHEMA "+sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Name.ValueString())); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck schema", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *schemaResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state schemaModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var exists bool
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, state.Database.ValueString()); err != nil {
			return err
		}
		var existsErr error
		exists, existsErr = client.Exists(ctx, `SELECT count(*) FROM information_schema.schemata WHERE catalog_name = ? AND schema_name = ?`, state.Database.ValueString(), state.Name.ValueString())
		return existsErr
	})
	if err != nil && isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck schema", err.Error())
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
	}
}

func (r *schemaResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan schemaModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(plan.Database.ValueString() + "." + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *schemaResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state schemaModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := client.AttachDatabase(ctx, state.Database.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	query := "DROP SCHEMA IF EXISTS " + sqlbuild.QuoteQualifiedIdentifier(state.Database.ValueString(), state.Name.ValueString()) + schemaDropMode(state)
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil {
		resp.Diagnostics.AddError("Unable to drop MotherDuck schema", err.Error())
	}
}

func schemaDropMode(model schemaModel) string {
	if !model.CascadeOnDelete.IsNull() && model.CascadeOnDelete.ValueBool() {
		return " CASCADE"
	}
	return ""
}

func (r *schemaResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitSQLImportID(req.ID, ".", 2, "`<database>.<schema>`", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}

type tableResource struct{ baseResource }

type tableModel struct {
	ID       types.String `tfsdk:"id"`
	Database types.String `tfsdk:"database"`
	Schema   types.String `tfsdk:"schema"`
	Name     types.String `tfsdk:"name"`
	Columns  types.Map    `tfsdk:"columns"`
}

func NewTableResource() resource.Resource { return &tableResource{} }

func (r *tableResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (r *tableResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck table definition. Column changes replace the table.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Table resource ID in `<database>.<schema>.<table>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the table.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema that contains the table.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Table name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"columns": schema.MapAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Map of column name to DuckDB SQL type. Type aliases are compared semantically during refresh to avoid replacement churn.",
				PlanModifiers:       mapRequiresReplace(),
			},
		},
	}
}

func (r *tableResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config tableModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateTableColumns(ctx, config.Columns, &resp.Diagnostics)
}

func (r *tableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan tableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	columns := map[string]string{}
	resp.Diagnostics.Append(plan.Columns.ElementsAs(ctx, &columns, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString())
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	columns = canonicalTableColumns(ctx, client, columns, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "CREATE TABLE " + sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()) + " (" + columnDDL(columns) + ")"
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck table", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state tableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists := relationExists(ctx, r, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), "BASE TABLE", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	liveColumns := readTableColumnTypes(ctx, client, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Columns.IsNull() || state.Columns.IsUnknown() {
		state.Columns = tableColumnsValue(ctx, liveColumns, &resp.Diagnostics)
	} else if !tableColumnsSemanticallyEqual(ctx, client, state.Columns, liveColumns, &resp.Diagnostics) {
		state.Columns = tableColumnsValue(ctx, liveColumns, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *tableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Table updates are not supported", "Change columns by replacing the table resource.")
}

func (r *tableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	dropRelation(ctx, r, "TABLE", req.State, &resp.Diagnostics)
}

func (r *tableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importThreePartID(ctx, req.ID, resp)
}

type viewResource struct{ baseResource }

type viewModel struct {
	ID       types.String `tfsdk:"id"`
	Database types.String `tfsdk:"database"`
	Schema   types.String `tfsdk:"schema"`
	Name     types.String `tfsdk:"name"`
	Query    types.String `tfsdk:"query"`
}

const viewServerDefinitionPrivateKey = "view_server_definition_v1"

type privateState interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

func NewViewResource() resource.Resource { return &viewResource{} }

func (r *viewResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_view"
}

func (r *viewResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck SQL view.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "View resource ID in `<database>.<schema>.<view>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the view.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema that contains the view.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "View name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"query": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Single SELECT query body for the view. Semicolons are rejected.",
			},
		},
	}
}

func (r *viewResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config viewModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Query.IsNull() && !config.Query.IsUnknown() && strings.Contains(config.Query.ValueString(), ";") {
		resp.Diagnostics.AddAttributeError(path.Root("query"), "Invalid MotherDuck view query", "View queries must be a single SELECT body and must not contain semicolons.")
	}
}

func (r *viewResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.createOrReplaceView(ctx, req.Plan, &resp.State, resp.Private, &resp.Diagnostics)
}

func (r *viewResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state viewModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists := relationExists(ctx, r, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), "VIEW", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	definition, found := readViewServerDefinition(ctx, client, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if found {
		storedDefinition, ok := loadViewServerDefinition(ctx, req.Private, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		if !ok {
			state.Query = types.StringValue(viewQueryFromDefinition(definition))
			storeViewServerDefinition(ctx, resp.Private, definition, &resp.Diagnostics)
		} else if storedDefinition != definition {
			state.Query = types.StringValue(viewQueryFromDefinition(definition))
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *viewResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.createOrReplaceView(ctx, req.Plan, &resp.State, resp.Private, &resp.Diagnostics)
}

func (r *viewResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	dropRelation(ctx, r, "VIEW", req.State, &resp.Diagnostics)
}

func (r *viewResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importThreePartID(ctx, req.ID, resp)
}

func (r *viewResource) createOrReplaceView(ctx context.Context, getter interface {
	Get(context.Context, any) diag.Diagnostics
}, setter interface {
	Set(context.Context, any) diag.Diagnostics
}, private privateState, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var plan viewModel
	diags.Append(getter.Get(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	id := qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString())
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		diags.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	query := "CREATE OR REPLACE VIEW " + sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()) + " AS " + plan.Query.ValueString()
	if err := client.Exec(ctx, query); err != nil {
		diags.AddError("Unable to create MotherDuck view", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	definition, found := readViewServerDefinition(ctx, client, plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString(), diags)
	if diags.HasError() {
		return
	}
	if !found {
		diags.AddError("Unable to read MotherDuck view", "View was created but was not visible in information_schema.views.")
		return
	}
	storeViewServerDefinition(ctx, private, definition, diags)
	diags.Append(setter.Set(ctx, &plan)...)
}

type secretResource struct{ baseResource }

type secretModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Type           types.String `tfsdk:"type"`
	SecretProvider types.String `tfsdk:"secret_provider"`
	Params         types.Map    `tfsdk:"params"`
	Storage        types.String `tfsdk:"storage"`
	Scope          types.String `tfsdk:"scope"`
	SecretSQL      types.String `tfsdk:"secret_sql"`
}

func NewSecretResource() resource.Resource { return &secretResource{} }

func (r *secretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a persistent MotherDuck secret. Secret values are write-only and redacted by MotherDuck.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret resource ID. This is the secret name.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Secret name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "DuckDB secret type, such as `S3`.",
				Validators:          sqlBareWordValidators(),
			},
			"secret_provider": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Optional DuckDB secret provider.",
				Validators:          sqlBareWordValidators(),
			},
			"params": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Sensitive secret parameters inserted into the CREATE SECRET statement.",
			},
			"storage": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret storage backend reported by DuckDB.",
			},
			"scope": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret scope reported by DuckDB.",
			},
			"secret_sql": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Optional raw secret body entries, for advanced provider-specific clauses. Values are inserted inside the CREATE SECRET parentheses after TYPE/PROVIDER.",
			},
		},
	}
}

func (r *secretResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateSecretConfig(config, &resp.Diagnostics)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.createSecret(ctx, req.Plan, &resp.State, false, &resp.Diagnostics)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readSecret(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.createSecret(ctx, req.Plan, &resp.State, true, &resp.Diagnostics)
}

func (r *secretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var config, state secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desiredScope, ok := desiredSecretScopeFromParams(config.Params)
	if !ok || state.Scope.IsNull() || state.Scope.IsUnknown() {
		return
	}
	if state.Scope.ValueString() != desiredScope {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("scope"), desiredScope)...)
	}
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "DROP SECRET " + sqlbuild.QuoteIdentifier(state.Name.ValueString()) + " FROM motherduck"
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to drop MotherDuck secret", err.Error())
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *secretResource) createSecret(ctx context.Context, getter interface {
	Get(context.Context, any) diag.Diagnostics
}, setter interface {
	Set(context.Context, any) diag.Diagnostics
}, replace bool, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var plan secretModel
	diags.Append(getter.Get(ctx, &plan)...)
	if diags.HasError() {
		return
	}
	params := map[string]string{}
	if !plan.Params.IsNull() {
		diags.Append(plan.Params.ElementsAs(ctx, &params, false)...)
	}
	if diags.HasError() {
		return
	}
	entries := []string{"TYPE " + strings.ToUpper(plan.Type.ValueString())}
	if !plan.SecretProvider.IsNull() && plan.SecretProvider.ValueString() != "" {
		entries = append(entries, "PROVIDER "+strings.ToUpper(plan.SecretProvider.ValueString()))
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entries = append(entries, strings.ToUpper(key)+" "+sqlbuild.StringLiteral(params[key]))
	}
	if !plan.SecretSQL.IsNull() && strings.TrimSpace(plan.SecretSQL.ValueString()) != "" {
		entries = append(entries, strings.TrimSpace(plan.SecretSQL.ValueString()))
	}
	createKeyword := "CREATE SECRET"
	if replace {
		createKeyword = "CREATE OR REPLACE SECRET"
	}
	query := fmt.Sprintf("%s %s IN MOTHERDUCK (%s)", createKeyword, sqlbuild.QuoteIdentifier(plan.Name.ValueString()), strings.Join(entries, ", "))
	if err := client.Exec(ctx, query); err != nil {
		diags.AddError("Unable to create MotherDuck secret", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Name.ValueString())
	found := r.readSecret(ctx, &plan, diags)
	if !found && !diags.HasError() {
		diags.AddError("Unable to read MotherDuck secret", "Secret was created but was not visible in duckdb_secrets().")
		return
	}
	diags.Append(setter.Set(ctx, &plan)...)
}

func (r *secretResource) readSecret(ctx context.Context, model *secretModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var secretType, provider, storage, scope stdsql.NullString
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, `SELECT type, provider, storage, scope::VARCHAR FROM duckdb_secrets() WHERE name = ? AND storage = 'motherduck'`, model.Name.ValueString()).Scan(&secretType, &provider, &storage, &scope)
	})
	if err == stdsql.ErrNoRows {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck secret", err.Error())
		return false
	}
	model.ID = types.StringValue(model.Name.ValueString())
	model.Type = lowerNullString(secretType)
	model.SecretProvider = lowerNullString(provider)
	model.Storage = nullString(storage)
	model.Scope = nullString(scope)
	return true
}

type shareResource struct{ baseResource }

type shareModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	SourceDatabase types.String `tfsdk:"source_database"`
	Access         types.String `tfsdk:"access"`
	Visibility     types.String `tfsdk:"visibility"`
	UpdateMode     types.String `tfsdk:"update_mode"`
	IncludePattern types.List   `tfsdk:"include_pattern"`
	URL            types.String `tfsdk:"url"`
	CreatedTS      types.String `tfsdk:"created_ts"`
}

func NewShareResource() resource.Resource { return &shareResource{} }

func (r *shareResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share"
}

func (r *shareResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck database share.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share resource ID. This is the share name.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Share name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"source_database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database to share.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"access": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Share access mode: `organization`, `restricted`, or `unrestricted`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareAccessValidators(),
			},
			"visibility": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Share visibility mode: `discoverable` or `hidden`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareVisibilityValidators(),
			},
			"update_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Share update mode: `manual` or `automatic`.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareUpdateModeValidators(),
			},
			"include_pattern": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Optional preview catalog include patterns. Null shares the entire database; an empty list shares no objects. Changes are applied in place. The MotherDuck client must have filtered shares enabled.",
			},
			"url": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Share URL reported by MotherDuck. This is sensitive because unrestricted share URLs can grant access.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (r *shareResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config shareModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.IncludePattern.IsNull() || config.IncludePattern.IsUnknown() {
		return
	}
	validateShareIncludePattern(config.IncludePattern, &resp.Diagnostics)
}

func validateShareIncludePattern(includePattern types.List, diags *diag.Diagnostics) {
	totalLength := 0
	for _, element := range includePattern.Elements() {
		pattern, ok := element.(types.String)
		if !ok || pattern.IsUnknown() {
			return
		}
		if pattern.IsNull() {
			diags.AddAttributeError(path.Root("include_pattern"), "Invalid MotherDuck share include pattern", "Include-pattern entries must not be null.")
			return
		}
		totalLength += len(pattern.ValueString())
	}
	if totalLength > 16384 {
		diags.AddAttributeError(
			path.Root("include_pattern"),
			"Invalid MotherDuck share include pattern",
			fmt.Sprintf("The combined include-pattern length is %d characters; MotherDuck accepts at most 16384.", totalLength),
		)
	}
}

func (r *shareResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	options := []string{}
	if !plan.Access.IsNull() && plan.Access.ValueString() != "" {
		options = append(options, "ACCESS "+strings.ToUpper(plan.Access.ValueString()))
	}
	if !plan.Visibility.IsNull() && plan.Visibility.ValueString() != "" {
		options = append(options, "VISIBILITY "+strings.ToUpper(plan.Visibility.ValueString()))
	}
	if !plan.UpdateMode.IsNull() && plan.UpdateMode.ValueString() != "" {
		options = append(options, "UPDATE "+strings.ToUpper(plan.UpdateMode.ValueString()))
	}
	if !plan.IncludePattern.IsNull() {
		pattern, ok := shareIncludePattern(ctx, plan.IncludePattern, &resp.Diagnostics)
		if !ok {
			return
		}
		options = append(options, "INCLUDE_PATTERN "+sqlbuild.StringLiteral(pattern))
	}
	if err := client.AttachDatabase(ctx, plan.SourceDatabase.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck source database", err.Error())
		return
	}
	query := "CREATE SHARE " + sqlbuild.QuoteIdentifier(plan.Name.ValueString()) + " FROM " + sqlbuild.QuoteIdentifier(plan.SourceDatabase.ValueString()) + sqlbuild.Options(options...)
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck share", err.Error())
		return
	}
	prepareShareCreateState(&plan)
	found := r.readShare(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck share", "Share was created but was not visible in MD_INFORMATION_SCHEMA.OWNED_SHARES.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readShare(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *shareResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan, state shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	if !plan.IncludePattern.Equal(state.IncludePattern) {
		query := "ALTER SHARE " + sqlbuild.QuoteIdentifier(state.Name.ValueString())
		if plan.IncludePattern.IsNull() {
			query += " RESET INCLUDE_PATTERN"
		} else {
			pattern, ok := shareIncludePattern(ctx, plan.IncludePattern, &resp.Diagnostics)
			if !ok {
				return
			}
			query += " SET INCLUDE_PATTERN " + sqlbuild.StringLiteral(pattern)
		}
		if err := client.Exec(ctx, query); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck share include pattern", err.Error())
			return
		}
	}
	if !r.readShare(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck share", "Share was updated but was not visible in MD_INFORMATION_SCHEMA.OWNED_SHARES.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "DROP SHARE IF EXISTS " + sqlbuild.QuoteIdentifier(state.Name.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil {
		resp.Diagnostics.AddError("Unable to drop MotherDuck share", err.Error())
	}
}

func (r *shareResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingleSQLIdentifier(ctx, req.ID, path.Root("name"), resp)
}

func (r *shareResource) readShare(ctx context.Context, model *shareModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	share, err := sqlcatalog.ReadOwnedShare(ctx, client, model.Name.ValueString())
	if err == stdsql.ErrNoRows {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck share", err.Error())
		return false
	}
	applyOwnedShare(ctx, model, share, diags)
	return true
}

// applyOwnedShare maps an OWNED_SHARES row onto the share model. The
// `access`, `visibility`, and `update_mode` attributes are optional-only and
// config-owned: OWNED_SHARES always reports server defaults for omitted
// options, so refreshing them into state when the configuration omits them
// would produce an inconsistent result after apply.
func applyOwnedShare(ctx context.Context, model *shareModel, share sqlcatalog.OwnedShare, diags *diag.Diagnostics) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.URL = nullString(share.URL)
	model.SourceDatabase = nullString(share.SourceDatabase)
	model.Access = optionalConfigOwnedLowerStringFromLive(model.Access, share.Access)
	model.Visibility = optionalConfigOwnedLowerStringFromLive(model.Visibility, share.Visibility)
	model.UpdateMode = optionalConfigOwnedLowerStringFromLive(model.UpdateMode, share.UpdateMode)
	model.IncludePattern = optionalStringListFromJSON(ctx, model.IncludePattern, share.IncludePattern, "include_pattern", diags)
	model.CreatedTS = nullString(share.CreatedTS)
}

func prepareShareCreateState(model *shareModel) {
	model.ID = types.StringValue(model.Name.ValueString())
	model.URL = knownString(model.URL)
	model.CreatedTS = knownString(model.CreatedTS)
}

func shareIncludePattern(ctx context.Context, value types.List, diags *diag.Diagnostics) (string, bool) {
	var patterns []string
	diags.Append(value.ElementsAs(ctx, &patterns, false)...)
	if diags.HasError() {
		return "", false
	}
	return strings.Join(patterns, ","), true
}

type shareGrantResource struct{ baseResource }

type shareGrantModel struct {
	ID          types.String `tfsdk:"id"`
	Share       types.String `tfsdk:"share"`
	Username    types.String `tfsdk:"username"`
	GranteeType types.String `tfsdk:"grantee_type"`
}

func NewShareGrantResource() resource.Resource { return &shareGrantResource{} }

func (r *shareGrantResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share_grant"
}

func (r *shareGrantResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Grants READ on a restricted MotherDuck share to one user or role.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share grant ID in `<share>/<grantee_type>/<username>` form.",
			},
			"share": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Restricted share name to grant.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Grantable MotherDuck user or service-account principal. Must be non-blank and must not include leading or trailing whitespace. Email-like principals are allowed. The PAT email, PAT session name, and `motherduck_current_user` value may not be valid share-grant usernames.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          shareGrantPrincipalValidators(),
			},
			"grantee_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("user"),
				MarkdownDescription: "Principal type: `user` or `role`. Defaults to `user`. The `username` field stores the principal name for both types.",
				PlanModifiers:       stringOptionalComputedRequiresReplaceIfConfigured(),
				Validators:          roleGranteeTypeValidators(),
			},
		},
	}
}

func (r *shareGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan shareGrantModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := shareGrantStatement("GRANT", plan.Share.ValueString(), "TO", plan.GranteeType.ValueString(), plan.Username.ValueString())
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to grant MotherDuck share access", shareGrantErrorDetail(err))
		return
	}
	plan.ID = shareGrantID(plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.GranteeType.IsNull() || state.GranteeType.IsUnknown() {
		state.GranteeType = types.StringValue("user")
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_list_share_grantees", "motherduck_share_grant") {
		return
	}
	var exists bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		exists, existsErr = client.Exists(ctx, `SELECT count(*) FROM MD_LIST_SHARE_GRANTEES(?) WHERE lower(grantee_name) = lower(?) AND lower(grantee_type) = lower(?) AND lower(privilege) = 'read'`, state.Share.ValueString(), state.Username.ValueString(), state.GranteeType.ValueString())
		return existsErr
	})
	remove, err := shareGrantReadDecision(exists, err)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck share grant", err.Error())
		return
	}
	if remove {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = shareGrantID(state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *shareGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Share grant updates are not supported", "Replace the grant to change share or username.")
}

func (r *shareGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareGrantModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.GranteeType.IsNull() || state.GranteeType.IsUnknown() {
		state.GranteeType = types.StringValue("user")
	}
	query := shareGrantStatement("REVOKE", state.Share.ValueString(), "FROM", state.GranteeType.ValueString(), state.Username.ValueString())
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to revoke MotherDuck share access", err.Error())
	}
}

func (r *shareGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 && len(parts) != 3 {
		resp.Diagnostics.AddError("Invalid import ID", "Expected `<share>/<username>` or `<share>/<grantee_type>/<username>`.")
		return
	}
	granteeType := "user"
	usernameIndex := 1
	if len(parts) == 3 {
		granteeType = strings.ToLower(parts[1])
		usernameIndex = 2
	}
	if !validateSQLImportIDPart(parts[0], "`<share>/<grantee_type>/<username>`", &resp.Diagnostics) {
		return
	}
	if granteeType != "user" && granteeType != "role" {
		resp.Diagnostics.AddError("Invalid import ID", "Grantee type must be `user` or `role`.")
		return
	}
	if !validateShareGrantPrincipalImportID(parts[usernameIndex], "`<share>/<grantee_type>/<username>`", &resp.Diagnostics) {
		return
	}
	canonicalID := parts[0] + "/" + granteeType + "/" + parts[usernameIndex]
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), canonicalID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("share"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("grantee_type"), granteeType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), parts[usernameIndex])...)
}

// shareGrantReadDecision classifies a grantee-listing refresh result. A
// not-found error means the share itself was dropped out of band, so the
// grant no longer exists and must be removed from state instead of failing
// every subsequent refresh and destroy.
func shareGrantReadDecision(exists bool, err error) (remove bool, readErr error) {
	if err != nil {
		if isNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return !exists, nil
}

func shareGrantID(model shareGrantModel) types.String {
	return types.StringValue(model.Share.ValueString() + "/" + model.GranteeType.ValueString() + "/" + model.Username.ValueString())
}

func shareGrantStatement(action, share, preposition, granteeType, username string) string {
	return action + " READ ON SHARE " + sqlbuild.QuoteIdentifier(share) + " " + preposition + " " + strings.ToUpper(granteeType) + " " + sqlbuild.QuoteIdentifier(username)
}

func shareGrantErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	detail := err.Error()
	if strings.Contains(strings.ToLower(detail), "unable to find user") {
		return detail + "\n\nUse a grantable MotherDuck user or service-account principal. The PAT email, PAT session name, and `motherduck_current_user` value may not be valid share-grant usernames."
	}
	return detail
}

type snapshotResource struct{ baseResource }

type snapshotModel struct {
	ID        types.String           `tfsdk:"id"`
	Database  types.String           `tfsdk:"database"`
	Name      types.String           `tfsdk:"name"`
	CreatedTS types.String           `tfsdk:"created_ts"`
	Timeouts  resourceTimeouts.Value `tfsdk:"timeouts"`
}

func NewSnapshotResource() resource.Resource { return &snapshotResource{} }

func (r *snapshotResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_snapshot"
}

func (r *snapshotResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Creates a named MotherDuck database snapshot. Delete removes the snapshot name because MotherDuck has no public snapshot delete command.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "MotherDuck snapshot ID.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database to snapshot.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Snapshot name.",
				Validators:          sqlIdentifierValidators(),
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Snapshot creation timestamp reported by MotherDuck.",
			},
			"timeouts": resourceTimeoutsAttribute(ctx, resourceTimeouts.Opts{
				Create:            true,
				Read:              true,
				Update:            true,
				Delete:            true,
				CreateDescription: "Optional timeout for creating a MotherDuck database snapshot.",
				ReadDescription:   "Optional timeout for refreshing MotherDuck snapshot metadata.",
				UpdateDescription: "Optional timeout for renaming a MotherDuck database snapshot.",
				DeleteDescription: "Optional timeout for removing the managed snapshot name.",
			}),
		},
	}
}

func (r *snapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan snapshotModel
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
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	query := "CREATE SNAPSHOT " + sqlbuild.QuoteIdentifier(plan.Name.ValueString()) + " OF " + sqlbuild.QuoteIdentifier(plan.Database.ValueString())
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck snapshot", err.Error())
		return
	}
	prepareSnapshotCreateState(&plan)
	found := r.readSnapshot(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck snapshot", "Snapshot was created but was not visible in MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *snapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state snapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := timeoutContext(ctx, state.Timeouts, "read", 5*time.Minute, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}
	found := r.readSnapshot(ctx, &state, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *snapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state snapshotModel
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
	if plan.Name.ValueString() != state.Name.ValueString() {
		if err := client.AttachDatabase(ctx, state.Database.ValueString()); err != nil {
			resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
			return
		}
		query := "ALTER SNAPSHOT " + sqlbuild.QuoteQualifiedIdentifier(state.Database.ValueString(), state.Name.ValueString()) + " SET snapshot_name = " + sqlbuild.StringLiteral(plan.Name.ValueString())
		if err := client.Exec(ctx, query); err != nil {
			resp.Diagnostics.AddError("Unable to rename MotherDuck snapshot", err.Error())
			return
		}
	}
	found := r.readSnapshot(ctx, &plan, &resp.Diagnostics)
	if !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck snapshot", "Snapshot was updated but was not visible in MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *snapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state snapshotModel
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
	if state.ID.IsNull() || state.ID.ValueString() == "" {
		return
	}
	if err := client.AttachDatabase(ctx, state.Database.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	err := retry.SQL(ctx, func() error {
		return client.WithDatabaseUse(ctx, state.Database.ValueString(), func(exec func(string, ...any) error) error {
			return exec("ALTER SNAPSHOT " + sqlbuild.StringLiteral(state.ID.ValueString()) + " SET snapshot_name = ''")
		})
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to unname MotherDuck snapshot", err.Error())
	}
}

func (r *snapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitSQLImportID(req.ID, ".", 2, "`<database>.<snapshot_name>`", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}

func (r *snapshotResource) readSnapshot(ctx context.Context, model *snapshotModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var id, created stdsql.NullString
	var matches int
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, model.Database.ValueString()); err != nil {
			return err
		}
		return client.QueryRow(ctx, `SELECT snapshot_id::VARCHAR, created_ts::VARCHAR, count(*) OVER () FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = ? AND snapshot_name = ?`, model.Database.ValueString(), model.Name.ValueString()).Scan(&id, &created, &matches)
	})
	if err != nil && isNotFound(err) {
		return false
	}
	if err == stdsql.ErrNoRows {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck snapshot", err.Error())
		return false
	}
	if matches > 1 {
		diags.AddError(
			"Ambiguous MotherDuck snapshot",
			fmt.Sprintf("Found %d snapshots named %q for database %q. Rename or remove duplicates before managing this snapshot with Terraform.", matches, model.Name.ValueString(), model.Database.ValueString()),
		)
		return false
	}
	model.ID = nullString(id)
	model.CreatedTS = nullString(created)
	return true
}

func prepareSnapshotCreateState(model *snapshotModel) {
	model.ID = knownString(model.ID)
	model.CreatedTS = knownString(model.CreatedTS)
}

func relationExists(ctx context.Context, r interface {
	sql(context.Context, *diag.Diagnostics) providerctx.SQLClient
}, database, schemaName, name, tableType string, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var exists bool
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, database); err != nil {
			return err
		}
		var existsErr error
		exists, existsErr = client.Exists(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_catalog = ? AND table_schema = ? AND table_name = ? AND table_type = ?`, database, schemaName, name, tableType)
		return existsErr
	})
	if err != nil && isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck relation", err.Error())
		return false
	}
	return exists
}

func readTableColumnTypes(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) map[string]string {
	var rowsJSON string
	err := retry.SQL(ctx, func() error {
		var queryErr error
		rowsJSON, queryErr = client.QueryRowsJSON(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_catalog = ? AND table_schema = ? AND table_name = ? ORDER BY ordinal_position`, database, schemaName, name)
		return queryErr
	})
	if err != nil {
		diags.AddError("Unable to read MotherDuck table columns", err.Error())
		return nil
	}
	var rows []struct {
		ColumnName string `json:"column_name"`
		DataType   string `json:"data_type"`
	}
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		diags.AddError("Unable to decode MotherDuck table columns", err.Error())
		return nil
	}
	columns := make(map[string]string, len(rows))
	for _, row := range rows {
		columns[row.ColumnName] = row.DataType
	}
	return columns
}

func tableColumnsValue(ctx context.Context, columns map[string]string, diags *diag.Diagnostics) types.Map {
	value, valueDiags := types.MapValueFrom(ctx, types.StringType, columns)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return types.MapNull(types.StringType)
	}
	return value
}

type scalarStringer interface {
	ScalarString(context.Context, string, ...any) (string, error)
}

func tableColumnsSemanticallyEqual(ctx context.Context, client scalarStringer, configuredValue types.Map, liveColumns map[string]string, diags *diag.Diagnostics) bool {
	configuredColumns := map[string]string{}
	diags.Append(configuredValue.ElementsAs(ctx, &configuredColumns, false)...)
	if diags.HasError() {
		return false
	}
	if len(configuredColumns) != len(liveColumns) {
		return false
	}
	for name, configuredType := range configuredColumns {
		liveType, ok := liveColumns[name]
		if !ok {
			return false
		}
		equal, err := columnTypesSemanticallyEqual(ctx, client, configuredType, liveType)
		if err != nil {
			diags.AddError("Unable to normalize MotherDuck table column type", err.Error())
			return false
		}
		if !equal {
			return false
		}
	}
	return true
}

func columnTypesSemanticallyEqual(ctx context.Context, client scalarStringer, configuredType, liveType string) (bool, error) {
	canonical, err := canonicalColumnType(ctx, client, configuredType)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(canonical, strings.TrimSpace(liveType)), nil
}

func canonicalColumnType(ctx context.Context, client scalarStringer, columnType string) (string, error) {
	trimmed := strings.TrimSpace(columnType)
	if detail := columnTypeSyntaxError(trimmed); detail != "" {
		return "", fmt.Errorf("%s", detail)
	}
	return client.ScalarString(ctx, "SELECT typeof(CAST(NULL AS "+trimmed+"))")
}

func readViewServerDefinition(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) (string, bool) {
	var definition stdsql.NullString
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, database); err != nil {
			return err
		}
		return client.QueryRow(ctx, `SELECT view_definition FROM information_schema.views WHERE table_catalog = ? AND table_schema = ? AND table_name = ?`, database, schemaName, name).Scan(&definition)
	})
	if err != nil && isNotFound(err) {
		return "", false
	}
	if err == stdsql.ErrNoRows {
		return "", false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck view definition", err.Error())
		return "", false
	}
	if !definition.Valid {
		return "", false
	}
	return definition.String, true
}

func storeViewServerDefinition(ctx context.Context, private privateState, definition string, diags *diag.Diagnostics) {
	if private == nil {
		return
	}
	data, err := json.Marshal(definition)
	if err != nil {
		diags.AddError("Unable to encode MotherDuck view private state", err.Error())
		return
	}
	diags.Append(private.SetKey(ctx, viewServerDefinitionPrivateKey, data)...)
}

func loadViewServerDefinition(ctx context.Context, private privateState, diags *diag.Diagnostics) (string, bool) {
	if private == nil {
		return "", false
	}
	data, privateDiags := private.GetKey(ctx, viewServerDefinitionPrivateKey)
	diags.Append(privateDiags...)
	if privateDiags.HasError() || len(data) == 0 {
		return "", false
	}
	var definition string
	if err := json.Unmarshal(data, &definition); err != nil {
		diags.AddError("Unable to decode MotherDuck view private state", err.Error())
		return "", false
	}
	return definition, true
}

func viewQueryFromDefinition(definition string) string {
	body := strings.TrimSpace(definition)
	upperBody := strings.ToUpper(body)
	if strings.HasPrefix(upperBody, "CREATE ") {
		idx := strings.Index(upperBody, " AS ")
		if idx >= 0 {
			body = strings.TrimSpace(body[idx+len(" AS "):])
		}
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, ";"))
	return body
}

func dropRelation(ctx context.Context, r interface {
	sql(context.Context, *diag.Diagnostics) providerctx.SQLClient
}, keyword string, getter interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var database, schemaName, name types.String
	diags.Append(getter.GetAttribute(ctx, path.Root("database"), &database)...)
	diags.Append(getter.GetAttribute(ctx, path.Root("schema"), &schemaName)...)
	diags.Append(getter.GetAttribute(ctx, path.Root("name"), &name)...)
	if diags.HasError() {
		return
	}
	if err := client.AttachDatabase(ctx, database.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		diags.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	if err := client.Exec(ctx, "DROP "+keyword+" IF EXISTS "+sqlbuild.QuoteQualifiedIdentifier(database.ValueString(), schemaName.ValueString(), name.ValueString())); err != nil {
		diags.AddError("Unable to drop MotherDuck relation", err.Error())
	}
}

func importThreePartID(ctx context.Context, id string, resp *resource.ImportStateResponse) {
	parts, ok := splitSQLImportID(id, ".", 3, "`<database>.<schema>.<name>`", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("schema"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
}

func importSingleSQLIdentifier(ctx context.Context, id string, namePath path.Path, resp *resource.ImportStateResponse) {
	if !validateSQLImportIDPart(id, "`<name>`", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, namePath, id)...)
}

func splitSQLImportID(id, separator string, wantParts int, usage string, diags *diag.Diagnostics) ([]string, bool) {
	parts, ok := splitImportID(id, separator, wantParts, usage, diags)
	if !ok {
		return nil, false
	}
	for _, part := range parts {
		if !validateSQLImportIDPart(part, usage, diags) {
			return nil, false
		}
	}
	return parts, true
}

func splitImportID(id, separator string, wantParts int, usage string, diags *diag.Diagnostics) ([]string, bool) {
	parts := strings.Split(id, separator)
	if len(parts) != wantParts {
		diags.AddError("Invalid import ID", "Use "+usage+".")
		return nil, false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			diags.AddError("Invalid import ID", "Use "+usage+" with non-empty segments.")
			return nil, false
		}
	}
	return parts, true
}

func validateSQLImportIDPart(part, usage string, diags *diag.Diagnostics) bool {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		diags.AddError("Invalid import ID", "Use "+usage+" with non-empty SQL resource name segments.")
		return false
	}
	if part != trimmed {
		diags.AddError("Invalid import ID", "Use "+usage+" with SQL resource name segments that do not have leading or trailing whitespace.")
		return false
	}
	if strings.Contains(part, ".") {
		diags.AddError("Invalid import ID", "SQL resource name segments must not contain dots because Terraform import IDs use dots as separators.")
		return false
	}
	return true
}

func validateShareGrantPrincipalImportID(part, usage string, diags *diag.Diagnostics) bool {
	detail, ok := validateShareGrantPrincipalValue(part)
	if !ok {
		diags.AddError("Invalid import ID", "Use "+usage+". "+detail)
		return false
	}
	return true
}

func qualifiedObjectID(database, schemaName, name string) string {
	return database + "." + schemaName + "." + name
}

func columnDDL(columns map[string]string) string {
	keys := make([]string, 0, len(columns))
	for key := range columns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, sqlbuild.QuoteIdentifier(key)+" "+columns[key])
	}
	return strings.Join(parts, ", ")
}

func validateTableColumns(ctx context.Context, columnsValue types.Map, diags *diag.Diagnostics) {
	if columnsValue.IsNull() || columnsValue.IsUnknown() {
		return
	}
	columns := map[string]string{}
	diags.Append(columnsValue.ElementsAs(ctx, &columns, false)...)
	if diags.HasError() {
		return
	}
	if len(columns) == 0 {
		diags.AddAttributeError(path.Root("columns"), "Invalid MotherDuck table columns", "A table must define at least one column.")
		return
	}
	for name, columnType := range columns {
		columnPath := path.Root("columns").AtMapKey(name)
		if strings.TrimSpace(name) == "" {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column", "Column names must not be empty.")
		}
		if detail := columnTypeSyntaxError(columnType); detail != "" {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column type", detail)
		}
	}
}

func columnTypeSyntaxError(columnType string) string {
	value := strings.TrimSpace(columnType)
	if value == "" {
		return "Column types must not be empty."
	}
	stack := make([]byte, 0, 4)
	var quote byte
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == 0 {
			return "Column types must not contain NULL bytes."
		}
		if quote != 0 {
			if ch == quote {
				if i+1 < len(value) && value[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ';' {
			return "Column types must be single DuckDB data type expressions and must not contain semicolons."
		}
		if i+1 < len(value) {
			pair := value[i : i+2]
			if pair == "--" || pair == "/*" || pair == "*/" {
				return "Column types must not contain SQL comments."
			}
		}
		switch ch {
		case '(', '[':
			stack = append(stack, ch)
		case ')', ']':
			if len(stack) == 0 || (ch == ')' && stack[len(stack)-1] != '(') || (ch == ']' && stack[len(stack)-1] != '[') {
				return "Column types must have balanced parentheses and brackets."
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return "Column types must not contain unterminated quoted values or identifiers."
	}
	if len(stack) != 0 {
		return "Column types must have balanced parentheses and brackets."
	}
	return ""
}

func canonicalTableColumns(ctx context.Context, client scalarStringer, columns map[string]string, diags *diag.Diagnostics) map[string]string {
	canonical := make(map[string]string, len(columns))
	keys := make([]string, 0, len(columns))
	for name := range columns {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		columnType, err := canonicalColumnType(ctx, client, columns[name])
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Invalid MotherDuck table column type", err.Error())
			continue
		}
		canonical[name] = columnType
	}
	return canonical
}

func validateSecretConfig(config secretModel, diags *diag.Diagnostics) {
	if !config.Params.IsNull() && !config.Params.IsUnknown() {
		for key := range config.Params.Elements() {
			if !isBareSQLWord(key) {
				diags.AddAttributeError(path.Root("params").AtMapKey(key), "Invalid MotherDuck secret parameter", "Secret parameter keys must be single bare SQL option words containing only letters, numbers, and underscores, starting with a letter or underscore.")
			}
		}
	}
	if !config.SecretSQL.IsNull() && !config.SecretSQL.IsUnknown() && strings.Contains(config.SecretSQL.ValueString(), ";") {
		diags.AddAttributeError(path.Root("secret_sql"), "Invalid MotherDuck secret SQL", "Raw secret SQL clauses must not contain semicolons.")
	}
}

func desiredSecretScopeFromParams(params types.Map) (string, bool) {
	if params.IsNull() || params.IsUnknown() {
		return "", false
	}
	for key, value := range params.Elements() {
		if !strings.EqualFold(key, "scope") {
			continue
		}
		scope, ok := value.(types.String)
		if !ok || scope.IsNull() || scope.IsUnknown() {
			return "", false
		}
		return secretScopeStateValue(scope.ValueString()), true
	}
	return "", false
}

func secretScopeStateValue(scope string) string {
	return "['" + strings.ReplaceAll(scope, "'", "''") + "']"
}

func nullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(value.String)
}

func knownString(value types.String) types.String {
	if value.IsUnknown() {
		return types.StringNull()
	}
	return value
}

func lowerNullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(strings.ToLower(value.String))
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

func resetDefaultDatabaseIfCurrent(ctx context.Context, client providerctx.SQLClient, database string) {
	current, err := client.ScalarString(ctx, "SELECT current_database()")
	if err != nil || !strings.EqualFold(current, database) {
		return
	}
	_ = client.Exec(ctx, "USE memory")
}
