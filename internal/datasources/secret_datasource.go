package datasources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
)

type secretDataSource struct{ baseDataSource }

type secretModel struct {
	Name       types.String `tfsdk:"name"`
	Type       types.String `tfsdk:"type"`
	Provider   types.String `tfsdk:"secret_provider"`
	Persistent types.Bool   `tfsdk:"persistent"`
	Scope      types.String `tfsdk:"scope"`
}

func NewSecretDataSource() datasource.DataSource { return &secretDataSource{} }

func (d *secretDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (d *secretDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one MotherDuck-managed secret by name without exposing its value or managing its lifecycle. Fails when the secret is absent.",
		Attributes: map[string]schema.Attribute{
			"name":            schema.StringAttribute{Required: true, MarkdownDescription: "Secret name to look up. Matching is case-insensitive because DuckDB stores secret names in lowercase."},
			"type":            schema.StringAttribute{Computed: true, MarkdownDescription: "Secret type reported by DuckDB."},
			"secret_provider": schema.StringAttribute{Computed: true, MarkdownDescription: "Secret provider reported by DuckDB."},
			"persistent":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the secret is persistent."},
			"scope":           schema.StringAttribute{Computed: true, MarkdownDescription: "Secret scope, or null when absent."},
		},
	}
}

func (d *secretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config secretModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if strings.TrimSpace(config.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid MotherDuck secret name", "Provide a non-empty secret name.")
		return
	}
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var secretType, provider, scope stdsql.NullString
	var persistent stdsql.NullBool
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, `SELECT type, provider, persistent, scope::VARCHAR FROM duckdb_secrets() WHERE lower(name) = lower(?) AND storage = 'motherduck'`, config.Name.ValueString()).Scan(&secretType, &provider, &persistent, &scope)
	})
	if errors.Is(err, stdsql.ErrNoRows) {
		resp.Diagnostics.AddError("MotherDuck secret not found", "No MotherDuck-managed secret named "+config.Name.ValueString()+" was found in duckdb_secrets().")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck secret", err.Error())
		return
	}
	config.Type = dataSourceNullString(secretType)
	config.Provider = dataSourceNullString(provider)
	config.Scope = dataSourceNullString(scope)
	config.Persistent = types.BoolNull()
	if persistent.Valid {
		config.Persistent = types.BoolValue(persistent.Bool)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
