package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

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
				PlanModifiers:       identifierID("database", "name"),
				MarkdownDescription: "Schema resource ID in `<database>.<schema>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the schema. Changing only the letter case updates state in place.",
				PlanModifiers:       databaseIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema name. Changing only the case of ASCII letters updates state in place. Any other change, including the case of a non-ASCII letter such as `Ä`, names a different schema and replaces it.",
				PlanModifiers:       catalogIdentifierRequiresReplace(),
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
		var existsErr error
		exists, existsErr = schemaExists(ctx, client, state.Database.ValueString(), state.Name.ValueString())
		return existsErr
	})
	if err != nil && isNotFoundFor(err, state.Database.ValueString(), state.Name.ValueString()) {
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

// schemaExists reports whether the database contains the schema. DuckDB
// resolves schema names by folding ASCII letters only, so the lower() filter
// fetches candidates and matchCatalogRow picks the schema DDL would resolve.
func schemaExists(ctx context.Context, client providerctx.SQLClient, database, name string) (bool, error) {
	if err := client.AttachDatabase(ctx, database); err != nil {
		return false, err
	}
	rows, err := queryCatalogRows(ctx, client, []string{"catalog_name", "schema_name"},
		`SELECT catalog_name, schema_name FROM information_schema.schemata WHERE lower(catalog_name) = lower(?) AND lower(schema_name) = lower(?)`,
		database, name)
	if err != nil {
		return false, err
	}
	return matchCatalogRow(rows, database, name) >= 0, nil
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
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !isDatabaseGone(err, state.Database.ValueString()) {
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
