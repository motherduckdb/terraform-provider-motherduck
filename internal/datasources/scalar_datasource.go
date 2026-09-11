package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

type scalarDataSource struct {
	baseDataSource
	name        string
	description string
	query       string
}

type scalarModel struct {
	Value types.String `tfsdk:"value"`
}

func NewCurrentUserDataSource() datasource.DataSource {
	return &scalarDataSource{name: "current_user", description: "Reads the MotherDuck user for the current SQL session.", query: "SELECT md_user()"}
}

func NewVersionDataSource() datasource.DataSource {
	return &scalarDataSource{name: "version", description: "Reads the MotherDuck version for the current SQL session.", query: "SELECT md_version()"}
}

func NewLiveDucklingSizeDataSource() datasource.DataSource {
	return &scalarDataSource{name: "live_duckling_size", description: "Reads the live Duckling size for the current SQL session.", query: "SELECT type FROM md_live_duckling_size()"}
}

func (d *scalarDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.name
}

func (d *scalarDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: d.description,
		Attributes: map[string]schema.Attribute{
			"value": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Scalar value returned by the MotherDuck SQL data source.",
			},
		},
	}
}

func (d *scalarDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var value string
	err := retry.SQL(ctx, func() error {
		var readErr error
		value, readErr = client.ScalarString(ctx, d.query)
		return readErr
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck scalar data source", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, scalarModel{Value: types.StringValue(value)})...)
}
