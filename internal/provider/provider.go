package provider

import (
	"context"
	"net/url"
	"os"
	"strings"
	"time"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/datasources"
	ephemeralresources "github.com/motherduckdb/terraform-provider-motherduck/internal/ephemeral"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/resources"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type motherduckProvider struct {
	version string
}

type providerModel struct {
	Token           types.String `tfsdk:"token"`
	AdminToken      types.String `tfsdk:"admin_token"`
	APIBaseURL      types.String `tfsdk:"api_base_url"`
	Database        types.String `tfsdk:"database"`
	AttachMode      types.String `tfsdk:"attach_mode"`
	CustomUserAgent types.String `tfsdk:"custom_user_agent"`
	RequestTimeout  types.Int64  `tfsdk:"request_timeout_seconds"`
}

const maxDurationSeconds = int64((1<<63 - 1) / int64(time.Second))

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &motherduckProvider{version: version}
	}
}

func (p *motherduckProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "motherduck"
	resp.Version = p.version
}

func (p *motherduckProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Provider for MotherDuck REST administration and SQL-managed resources.",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "MotherDuck token for SQL/data-plane operations. Defaults to `MOTHERDUCK_TOKEN`.",
			},
			"admin_token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "MotherDuck organization admin token for REST/control-plane operations. Defaults to `MOTHERDUCK_ADMIN_TOKEN`.",
			},
			"api_base_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "MotherDuck REST API base URL. Must be an absolute HTTP or HTTPS URL with a host. Defaults to `https://api.motherduck.com`.",
				Validators:          []validator.String{apiBaseURLValidator{}},
			},
			"database": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional MotherDuck database to attach during provider SQL initialization.",
			},
			"attach_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional MotherDuck attach mode. Supported values are `workspace` and `single`. Use `single` with `database` to attach that existing database without attaching other workspace databases; DuckDB/MotherDuck system catalogs such as `memory` and `md_information_schema` can still be present. Omit this argument for MotherDuck's default workspace attachment behavior.",
				Validators:          []validator.String{attachModeValidator{}},
			},
			"custom_user_agent": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional custom user agent suffix sent on both the DuckDB/MotherDuck SQL connection and MotherDuck REST API requests.",
			},
			"request_timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional timeout, in seconds, for MotherDuck REST API requests. Defaults to 30 seconds.",
				Validators:          []validator.Int64{tfvalidators.Int64Range("MotherDuck REST request timeout", 1, maxDurationSeconds)},
			},
		},
	}
}

func (p *motherduckProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := stringValue(cfg.Token, os.Getenv("MOTHERDUCK_TOKEN"))
	adminToken := stringValue(cfg.AdminToken, os.Getenv("MOTHERDUCK_ADMIN_TOKEN"))
	apiBaseURL := stringValue(cfg.APIBaseURL, os.Getenv("MOTHERDUCK_API_BASE_URL"))
	if strings.TrimSpace(apiBaseURL) == "" {
		apiBaseURL = mdrest.DefaultBaseURL
	}
	database := stringValue(cfg.Database, "")
	attachMode := stringValue(cfg.AttachMode, "")
	customUserAgent := stringValue(cfg.CustomUserAgent, "terraform-provider-motherduck/"+p.version)
	requestTimeout := int64Value(cfg.RequestTimeout, 30)

	if cfg.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Unknown MotherDuck token", "The provider cannot configure SQL resources with an unknown token.")
	}
	if cfg.AdminToken.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("admin_token"), "Unknown MotherDuck admin token", "The provider cannot configure REST resources with an unknown admin token.")
	}
	if cfg.RequestTimeout.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("request_timeout_seconds"), "Unknown MotherDuck REST request timeout", "The provider cannot configure REST requests with an unknown timeout.")
	}
	if attachMode == "single" && strings.TrimSpace(database) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("database"), "MotherDuck database required", "`attach_mode = \"single\"` requires `database` so the provider can attach the intended MotherDuck database.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	restClient, err := mdrest.New(apiBaseURL, adminToken, mdrest.WithUserAgent(customUserAgent), mdrest.WithTimeout(time.Duration(requestTimeout)*time.Second))
	if err != nil {
		resp.Diagnostics.AddError("Invalid MotherDuck API base URL", err.Error())
		return
	}

	providerData := &providerctx.Context{
		REST: restClient,
		SQLConfig: mdsql.Config{
			Token:           token,
			Database:        database,
			AttachMode:      attachMode,
			CustomUserAgent: customUserAgent,
		},
	}
	resp.DataSourceData = providerData
	resp.ResourceData = providerData
	resp.EphemeralResourceData = providerData
}

func (p *motherduckProvider) Resources(ctx context.Context) []func() resource.Resource {
	return resources.All()
}

func (p *motherduckProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return datasources.All()
}

func (p *motherduckProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return ephemeralresources.All()
}

func stringValue(value types.String, fallback string) string {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueString()
}

func int64Value(value types.Int64, fallback int64) int64 {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueInt64()
}

type apiBaseURLValidator struct{}

func (apiBaseURLValidator) Description(context.Context) string {
	return "must be an absolute HTTP or HTTPS URL with a host"
}

func (apiBaseURLValidator) MarkdownDescription(context.Context) string {
	return "must be an absolute HTTP or HTTPS URL with a host"
}

type attachModeValidator struct{}

func (attachModeValidator) Description(context.Context) string {
	return "must be workspace or single when set"
}

func (attachModeValidator) MarkdownDescription(context.Context) string {
	return "must be `workspace` or `single` when set"
}

func (attachModeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	raw := req.ConfigValue.ValueString()
	value := strings.TrimSpace(raw)
	if value == "" {
		return
	}
	if raw != value {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck attach mode",
			"`attach_mode` must not include leading or trailing whitespace.",
		)
		return
	}
	switch value {
	case "workspace", "single":
	default:
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck attach mode",
			"`attach_mode` only supports `workspace` or `single`. Omit `attach_mode` for MotherDuck's default workspace attachment behavior.",
		)
	}
}

func (apiBaseURLValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	raw := req.ConfigValue.ValueString()
	value := strings.TrimSpace(raw)
	if value == "" {
		return
	}
	if raw != value {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck API base URL",
			"`api_base_url` must not include leading or trailing whitespace.",
		)
		return
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck API base URL",
			"`api_base_url` must be an absolute HTTP or HTTPS URL with a host, such as `https://api.motherduck.com`.",
		)
		return
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck API base URL",
			"`api_base_url` must use the `http` or `https` scheme.",
		)
		return
	}
	if parsed.User != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck API base URL",
			"`api_base_url` must not include username or password credentials.",
		)
		return
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck API base URL",
			"`api_base_url` must not include a query string or fragment.",
		)
	}
}
