package resources

import (
	"context"
	stdsql "database/sql"
	"fmt"
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
	if err != nil && isNotFoundFor(err, model.Database.ValueString(), model.Name.ValueString()) {
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
