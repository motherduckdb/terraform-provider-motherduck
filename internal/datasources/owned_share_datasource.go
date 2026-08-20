package datasources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"strings"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &ownedShareDataSource{}
	_ datasource.DataSourceWithConfigure = &ownedShareDataSource{}
)

type ownedShareDataSource struct{ baseDataSource }

type ownedShareModel struct {
	Name           types.String `tfsdk:"name"`
	SourceDatabase types.String `tfsdk:"source_database"`
	Access         types.String `tfsdk:"access"`
	Visibility     types.String `tfsdk:"visibility"`
	UpdateMode     types.String `tfsdk:"update_mode"`
	IncludePattern types.List   `tfsdk:"include_pattern"`
	URL            types.String `tfsdk:"url"`
	CreatedTS      types.String `tfsdk:"created_ts"`
}

func NewOwnedShareDataSource() datasource.DataSource {
	return &ownedShareDataSource{}
}

func (d *ownedShareDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_owned_share"
}

func (d *ownedShareDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one MotherDuck share owned by the current account.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Exact owned share name to read.",
			},
			"source_database": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Database backing the owned share.",
			},
			"access": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share access mode.",
			},
			"visibility": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share visibility mode.",
			},
			"update_mode": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share update mode.",
			},
			"include_pattern": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Catalog include patterns applied to the share. Null means the share is unfiltered.",
			},
			"url": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Share URL. Sensitive because unrestricted share URLs can grant access.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (d *ownedShareDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var config ownedShareModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	share, err := sqlcatalog.ReadOwnedShare(ctx, client, config.Name.ValueString())
	if err == stdsql.ErrNoRows {
		resp.Diagnostics.AddError("MotherDuck owned share not found", "No owned share named "+config.Name.ValueString()+" was found in MD_INFORMATION_SCHEMA.OWNED_SHARES.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck owned share", err.Error())
		return
	}

	config.URL = dataSourceNullString(share.URL)
	config.SourceDatabase = dataSourceNullString(share.SourceDatabase)
	config.Access = lowerDataSourceNullString(share.Access)
	config.Visibility = lowerDataSourceNullString(share.Visibility)
	config.UpdateMode = lowerDataSourceNullString(share.UpdateMode)
	config.IncludePattern = dataSourceStringListFromJSON(ctx, share.IncludePattern, &resp.Diagnostics)
	config.CreatedTS = dataSourceNullString(share.CreatedTS)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func dataSourceStringListFromJSON(ctx context.Context, value stdsql.NullString, diags *diag.Diagnostics) types.List {
	if !value.Valid {
		return types.ListNull(types.StringType)
	}
	var values []string
	if err := json.Unmarshal([]byte(value.String), &values); err != nil {
		diags.AddError("Unable to decode MotherDuck string list", err.Error())
		return types.ListNull(types.StringType)
	}
	result, resultDiags := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(resultDiags...)
	return result
}

func dataSourceNullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(value.String)
}

func lowerDataSourceNullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(strings.ToLower(value.String))
}
