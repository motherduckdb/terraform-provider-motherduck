package datasources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlfunc"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"
)

var (
	_ datasource.DataSource                   = &shareDataSource{}
	_ datasource.DataSourceWithConfigure      = &shareDataSource{}
	_ datasource.DataSourceWithValidateConfig = &shareDataSource{}
)

const describeShareFunction = "md_describe_database_share"

type shareDataSource struct{ baseDataSource }

type shareModel struct {
	Name               types.String `tfsdk:"name"`
	URL                types.String `tfsdk:"url"`
	SourceDatabase     types.String `tfsdk:"source_database"`
	SourceDatabaseUUID types.String `tfsdk:"source_database_uuid"`
	Access             types.String `tfsdk:"access"`
	Visibility         types.String `tfsdk:"visibility"`
	UpdateMode         types.String `tfsdk:"update_mode"`
	CreatedTS          types.String `tfsdk:"created_ts"`
}

// shareDescription is one row of MD_DESCRIBE_DATABASE_SHARE.
type shareDescription struct {
	Name               stdsql.NullString
	URL                stdsql.NullString
	SourceDatabase     stdsql.NullString
	SourceDatabaseUUID stdsql.NullString
	Access             stdsql.NullString
	Visibility         stdsql.NullString
	UpdateMode         stdsql.NullString
	CreatedTS          stdsql.NullString
}

func NewShareDataSource() datasource.DataSource { return &shareDataSource{} }

func (d *shareDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share"
}

func (d *shareDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	nonBlank := []validator.String{tfvalidators.StringLength("MotherDuck share name or URL", 1, 0)}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Describes one MotherDuck share with `MD_DESCRIBE_DATABASE_SHARE`. Set `name` for a share owned by the current account or `url` for any share the caller can access, including shares received from other accounts. Unlike `motherduck_shared_with_me`, this reports the source database of received shares.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Validators:          nonBlank,
				MarkdownDescription: "Name of a share owned by the current account. Exactly one of `name` or `url` must be set. A configured name keeps its configured spelling. When `url` is set, this is the name MotherDuck reports for the share.",
			},
			"url": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Sensitive:           true,
				Validators:          nonBlank,
				MarkdownDescription: "Share URL. Exactly one of `name` or `url` must be set. Use the URL to describe a share received from another account. A configured URL keeps its configured spelling. When `name` is set, this is the URL MotherDuck reports. Sensitive because unrestricted share URLs can grant access.",
			},
			"source_database": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Database backing the share.",
			},
			"source_database_uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UUID of the database backing the share.",
			},
			"access": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share access mode in lowercase.",
			},
			"visibility": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share visibility mode in lowercase.",
			},
			"update_mode": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share update mode in lowercase.",
			},
			"created_ts": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Share creation timestamp reported by MotherDuck.",
			},
		},
	}
}

func (d *shareDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var name, url types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("url"), &url)...)
	if resp.Diagnostics.HasError() || name.IsUnknown() || url.IsUnknown() {
		return
	}
	if name.IsNull() == url.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid MotherDuck share lookup", "Set exactly one of name or url.")
	}
}

func (d *shareDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config shareModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	available, err := sqlfunc.Exists(ctx, client, describeShareFunction)
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return
	}
	if !available {
		resp.Diagnostics.AddError("MotherDuck SQL function unavailable", describeShareFunction+" is not exposed by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_share data source.")
		return
	}

	lookup, label := config.Name.ValueString(), "named "+config.Name.ValueString()
	if !config.URL.IsNull() {
		lookup, label = config.URL.ValueString(), "at the configured URL"
	}
	share, err := describeShare(ctx, client, lookup)
	if errors.Is(err, stdsql.ErrNoRows) || shareNotFound(err) {
		resp.Diagnostics.AddError("MotherDuck share not found", fmt.Sprintf("No share %s is visible to the current account. A share received from another account must be read by url.", label))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck share", redactShareURL(err.Error(), config.URL))
		return
	}
	setShareModel(&config, share)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func describeShare(ctx context.Context, client interface {
	QueryRow(context.Context, string, ...any) mdsql.RowScanner
}, lookup string) (shareDescription, error) {
	var share shareDescription
	query := "SELECT name, url, source_db_name, source_db_uuid::VARCHAR, access, visibility, update, created_ts::VARCHAR FROM MD_DESCRIBE_DATABASE_SHARE(" + sqlbuild.StringLiteral(lookup) + ")"
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(
			&share.Name,
			&share.URL,
			&share.SourceDatabase,
			&share.SourceDatabaseUUID,
			&share.Access,
			&share.Visibility,
			&share.UpdateMode,
			&share.CreatedTS,
		)
	})
	return share, err
}

// setShareModel fills model from a share description. MotherDuck reports a
// canonical spelling of the name and URL, so a configured name or URL keeps
// its configured value and references to it stay stable.
func setShareModel(model *shareModel, share shareDescription) {
	if model.Name.IsNull() || model.Name.IsUnknown() {
		model.Name = dataSourceNullString(share.Name)
	}
	if model.URL.IsNull() || model.URL.IsUnknown() {
		model.URL = dataSourceNullString(share.URL)
	}
	model.SourceDatabase = dataSourceNullString(share.SourceDatabase)
	model.SourceDatabaseUUID = dataSourceNullString(share.SourceDatabaseUUID)
	model.Access = lowerDataSourceNullString(share.Access)
	model.Visibility = lowerDataSourceNullString(share.Visibility)
	model.UpdateMode = lowerDataSourceNullString(share.UpdateMode)
	model.CreatedTS = dataSourceNullString(share.CreatedTS)
}

const redactedShareURL = "<redacted share URL>"

// quotedShareURLPattern matches a share URL that starts a quoted literal, up
// to the quote that ends the literal. A quote followed by another character
// of the URL, as in a share name that contains a quote or a doubled SQL
// quote, does not end it. The quotes are kept in the message.
var quotedShareURLPattern = regexp.MustCompile(`(?i)(['"])md(?::|%3a)_share(?:/|%2f)[^\r\n]*?(['"](?:[\s.,;:)\]}]|$))`)

// shareURLPattern matches any other share URL token, plain or URL-encoded, up
// to whitespace, so a share URL in an error is redacted even when MotherDuck
// rewrites its case or name segment. Trailing punctuation is left in the
// message.
var shareURLPattern = regexp.MustCompile(`(?i)md(?::|%3a)_share(?:/|%2f)\S*[^\s'".,;:)\]}]`)

// redactShareURL keeps share URLs out of diagnostics, because MotherDuck
// errors echo the lookup value and the URL can grant access. It removes the
// configured URL in any letter case and in its URL-encoded forms, and then any
// remaining share URL, quoted or not.
func redactShareURL(message string, configured types.String) string {
	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		value := configured.ValueString()
		for _, form := range []string{value, url.QueryEscape(value), url.PathEscape(value)} {
			message = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(form)).ReplaceAllLiteralString(message, redactedShareURL)
		}
	}
	message = quotedShareURLPattern.ReplaceAllString(message, "${1}"+redactedShareURL+"${2}")
	return shareURLPattern.ReplaceAllLiteralString(message, redactedShareURL)
}

// shareNotFound matches the catalog error MotherDuck returns for a share name
// or URL the caller cannot see, for example `Share: 'name' not found`.
func shareNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "share") && strings.Contains(message, "not found")
}
