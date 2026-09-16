package datasources

import (
	"context"
	stdsql "database/sql"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

type databaseDataSource struct{ baseDataSource }

type databaseModel struct {
	Name                        types.String `tfsdk:"name"`
	UUID                        types.String `tfsdk:"uuid"`
	CreatedTS                   types.String `tfsdk:"created_ts"`
	DatabaseType                types.String `tfsdk:"database_type"`
	Transient                   types.Bool   `tfsdk:"transient"`
	HistoricalSnapshotRetention types.String `tfsdk:"historical_snapshot_retention"`
}

func NewDatabaseDataSource() datasource.DataSource { return &databaseDataSource{} }

func (d *databaseDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (d *databaseDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one existing MotherDuck database by exact name without managing its lifecycle. Fails if the database is not visible in the caller's catalog.",
		Attributes: map[string]schema.Attribute{
			"name":                          schema.StringAttribute{Required: true, MarkdownDescription: "Exact database name to read."},
			"uuid":                          schema.StringAttribute{Computed: true, MarkdownDescription: "Database UUID reported by MotherDuck."},
			"created_ts":                    schema.StringAttribute{Computed: true, MarkdownDescription: "Database creation timestamp reported by MotherDuck."},
			"database_type":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Database type as reported by MotherDuck."},
			"transient":                     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the database is transient."},
			"historical_snapshot_retention": schema.StringAttribute{Computed: true, MarkdownDescription: "Historical snapshot retention as server interval text, or null when not reported. This is not an integer day count."},
		},
	}
}

func (d *databaseDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config databaseModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var uuid, createdTS, retention, databaseType stdsql.NullString
	var transient stdsql.NullBool
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, `SELECT uuid::VARCHAR, created_ts::VARCHAR, transient, historical_snapshot_retention::VARCHAR, type FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?`, config.Name.ValueString()).Scan(&uuid, &createdTS, &transient, &retention, &databaseType)
	})
	if errors.Is(err, stdsql.ErrNoRows) {
		resp.Diagnostics.AddError("MotherDuck database not found", "No database named "+config.Name.ValueString()+" was found in the caller's MD_INFORMATION_SCHEMA.DATABASES catalog.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck database", err.Error())
		return
	}
	config.UUID = dataSourceNullString(uuid)
	config.CreatedTS = dataSourceNullString(createdTS)
	config.DatabaseType = dataSourceNullString(databaseType)
	config.HistoricalSnapshotRetention = dataSourceNullString(retention)
	config.Transient = types.BoolNull()
	if transient.Valid {
		config.Transient = types.BoolValue(transient.Bool)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
