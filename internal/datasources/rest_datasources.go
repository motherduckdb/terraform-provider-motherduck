package datasources

import (
	"context"
	"encoding/json"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/diveembed"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &activeAccountsDataSource{}
	_ datasource.DataSourceWithConfigure = &activeAccountsDataSource{}
	_ datasource.DataSource              = &userTokensDataSource{}
	_ datasource.DataSourceWithConfigure = &userTokensDataSource{}
	_ datasource.DataSource              = &diveEmbedSessionDataSource{}
	_ datasource.DataSourceWithConfigure = &diveEmbedSessionDataSource{}
)

type activeAccountsDataSource struct{ baseDataSource }

type activeAccountsModel struct {
	AccountsJSON types.String `tfsdk:"accounts_json"`
}

func NewActiveAccountsDataSource() datasource.DataSource { return &activeAccountsDataSource{} }

func (d *activeAccountsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_active_accounts"
}

func (d *activeAccountsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads preview active account and active Duckling metadata from the MotherDuck REST API.",
		Attributes: map[string]schema.Attribute{
			"accounts_json": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Raw active-account inventory returned by the MotherDuck REST API. Sensitive because it can reveal account and Duckling metadata.",
			},
		},
	}
}

func (d *activeAccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	accounts, err := client.ActiveAccounts(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck active accounts", err.Error())
		return
	}
	payload, err := json.Marshal(accounts.Accounts)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode active accounts", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, activeAccountsModel{AccountsJSON: types.StringValue(string(payload))})...)
}

type userTokensDataSource struct{ baseDataSource }

type userTokensModel struct {
	Username   types.String `tfsdk:"username"`
	TokensJSON types.String `tfsdk:"tokens_json"`
	Tokens     types.List   `tfsdk:"tokens"`
}

func NewUserTokensDataSource() datasource.DataSource { return &userTokensDataSource{} }

func (d *userTokensDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_tokens"
}

func (d *userTokensDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists access token metadata for a MotherDuck user or service account.",
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "User or service account username. Must be non-blank and 1-255 characters.",
				Validators:          restUsernameValidators(),
			},
			"tokens_json": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Raw access token metadata returned by the MotherDuck REST API. Sensitive because token inventory reveals account security metadata.",
			},
			"tokens": schema.ListNestedAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Typed access token metadata. Sensitive because token inventory reveals account security metadata.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Access token ID.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Access token label.",
						},
						"expire_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Token expiration timestamp when present.",
						},
						"created_ts": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Token creation timestamp.",
						},
						"read_only": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the token is read-only according to MotherDuck.",
						},
						"token_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Token type reported by MotherDuck.",
						},
					},
				},
			},
		},
	}
}

func (d *userTokensDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var config userTokensModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tokens, err := client.ListTokens(ctx, config.Username.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to list MotherDuck user tokens", err.Error())
		return
	}
	payload, err := json.Marshal(tokenMetadataOnly(tokens))
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode token metadata", err.Error())
		return
	}
	config.TokensJSON = types.StringValue(string(payload))
	config.Tokens = userTokensListValue(tokens, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func tokenMetadataOnly(tokens []mdrest.Token) []mdrest.Token {
	metadata := make([]mdrest.Token, len(tokens))
	copy(metadata, tokens)
	for i := range metadata {
		metadata[i].Token = ""
	}
	return metadata
}

func userTokensListValue(tokens []mdrest.Token, diags *diag.Diagnostics) types.List {
	attrTypes := map[string]attr.Type{
		"id":         types.StringType,
		"name":       types.StringType,
		"expire_at":  types.StringType,
		"created_ts": types.StringType,
		"read_only":  types.BoolType,
		"token_type": types.StringType,
	}
	objectType := types.ObjectType{AttrTypes: attrTypes}
	values := make([]attr.Value, 0, len(tokens))
	for _, token := range tokens {
		objectValue, objectDiags := types.ObjectValue(attrTypes, map[string]attr.Value{
			"id":         types.StringValue(token.ID),
			"name":       optionalRESTString(token.Name),
			"expire_at":  optionalRESTString(token.ExpireAt),
			"created_ts": types.StringValue(token.CreatedTS),
			"read_only":  types.BoolValue(token.ReadOnly),
			"token_type": types.StringValue(token.TokenType),
		})
		diags.Append(objectDiags...)
		values = append(values, objectValue)
	}
	if diags.HasError() {
		return types.ListNull(objectType)
	}
	listValue, listDiags := types.ListValue(objectType, values)
	diags.Append(listDiags...)
	return listValue
}

func optionalRESTString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

type diveEmbedSessionDataSource struct{ baseDataSource }

func NewDiveEmbedSessionDataSource() datasource.DataSource { return &diveEmbedSessionDataSource{} }

func (d *diveEmbedSessionDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dive_embed_session"
}

func (d *diveEmbedSessionDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates a short-lived Dive embed session for a service account.",
		DeprecationMessage:  "Use the motherduck_dive_embed_session ephemeral resource instead. The data source creates a credential and persists it in Terraform state.",
		Attributes: map[string]schema.Attribute{
			"dive_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Dive ID. Must be a UUID with no leading or trailing whitespace.",
				Validators:          diveembed.DiveIDValidators(),
			},
			"username": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Service account username. Must be non-blank and 1-255 characters.",
				Validators:          diveembed.UsernameValidators(),
			},
			"session_hint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional hint used to reuse the same read-scaling session across embed requests. Must be non-blank when set.",
				Validators:          diveembed.SessionHintValidators(),
			},
			"session": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Short-lived embed session credential returned by MotherDuck. The data source persists this sensitive value in Terraform state.",
			},
		},
	}
}

func (d *diveEmbedSessionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var config diveembed.Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := diveembed.Create(ctx, client, &config); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck Dive embed session", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
