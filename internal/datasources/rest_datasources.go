package datasources

import (
	"context"
	"encoding/json"
	"sort"

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
	_ datasource.DataSource              = &usersDataSource{}
	_ datasource.DataSourceWithConfigure = &usersDataSource{}
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
		MarkdownDescription: "Lists metadata for the unexpired access tokens of a MotherDuck user or service account, sorted by token ID. MotherDuck omits expired and revoked tokens from the listing.",
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
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Free-form notes on the token's purpose, when set.",
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
	tokens = tokenMetadataOnly(tokens)
	payload, err := json.Marshal(tokens)
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

// tokenMetadataOnly returns a copy without secrets, sorted by token ID so the
// listing does not depend on the order the API returns tokens in.
func tokenMetadataOnly(tokens []mdrest.Token) []mdrest.Token {
	metadata := make([]mdrest.Token, len(tokens))
	copy(metadata, tokens)
	for i := range metadata {
		metadata[i].Token = ""
	}
	sort.SliceStable(metadata, func(i, j int) bool { return metadata[i].ID < metadata[j].ID })
	return metadata
}

func userTokensListValue(tokens []mdrest.Token, diags *diag.Diagnostics) types.List {
	attrTypes := map[string]attr.Type{
		"id":          types.StringType,
		"name":        types.StringType,
		"description": types.StringType,
		"expire_at":   types.StringType,
		"created_ts":  types.StringType,
		"read_only":   types.BoolType,
		"token_type":  types.StringType,
	}
	objectType := types.ObjectType{AttrTypes: attrTypes}
	values := make([]attr.Value, 0, len(tokens))
	for _, token := range tokens {
		objectValue, objectDiags := types.ObjectValue(attrTypes, map[string]attr.Value{
			"id":          types.StringValue(token.ID),
			"name":        optionalRESTString(token.Name),
			"description": optionalRESTString(token.Description),
			"expire_at":   optionalRESTString(token.ExpireAt),
			"created_ts":  types.StringValue(token.CreatedTS),
			"read_only":   types.BoolValue(token.ReadOnly),
			"token_type":  types.StringValue(token.TokenType),
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

type usersDataSource struct{ baseDataSource }

type usersModel struct {
	IsServiceAccount types.Bool `tfsdk:"is_service_account"`
	IsDeprovisioned  types.Bool `tfsdk:"is_deprovisioned"`
	Users            types.List `tfsdk:"users"`
}

func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

func (d *usersDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the users and service accounts in the organization of the admin token, sorted by username. The provider reads every page of the listing. The token needs the `member_management.view_all_members` privilege, which the built-in `admin`, `builder` and `explorer` roles include.",
		Attributes: map[string]schema.Attribute{
			"is_service_account": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Set to `true` to list only service accounts, or `false` to list only human users. Omit it to list both.",
			},
			"is_deprovisioned": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Set to `true` to list only deprovisioned users, or `false` to list only active users. Omit it to list both.",
			},
			"users": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Users that match the filters, sorted by username and then by ID.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "User ID.",
						},
						"username": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Username. Service account names can be passed to `motherduck_user_tokens` and `motherduck_access_token`.",
						},
						"email": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Email address MotherDuck has on record for the user.",
						},
						"first_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "First name. Null when MotherDuck has none, as for most service accounts.",
						},
						"last_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Last name. Null when MotherDuck has none, as for most service accounts.",
						},
						"is_service_account": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the user is a service account.",
						},
						"is_deprovisioned": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the user is deprovisioned.",
						},
						"roles": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Names of the roles granted directly to the user, sorted. Roles inherited through other roles are not listed. Use `motherduck_roles_for_user` for those.",
						},
					},
				},
			},
		},
	}
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.rest(&resp.Diagnostics)
	if client == nil {
		return
	}
	var config usersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	users, err := client.ListUsers(ctx, mdrest.ListUsersFilter{
		IsServiceAccount: config.IsServiceAccount.ValueBoolPointer(),
		IsDeprovisioned:  config.IsDeprovisioned.ValueBoolPointer(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Unable to list MotherDuck users", err.Error())
		return
	}
	config.Users = usersListValue(users, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// usersListValue sorts by username and ID so the listing does not depend on
// the collation MotherDuck sorts with.
func usersListValue(users []mdrest.User, diags *diag.Diagnostics) types.List {
	attrTypes := map[string]attr.Type{
		"id":                 types.StringType,
		"username":           types.StringType,
		"email":              types.StringType,
		"first_name":         types.StringType,
		"last_name":          types.StringType,
		"is_service_account": types.BoolType,
		"is_deprovisioned":   types.BoolType,
		"roles":              types.ListType{ElemType: types.StringType},
	}
	objectType := types.ObjectType{AttrTypes: attrTypes}
	sorted := make([]mdrest.User, len(users))
	copy(sorted, users)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Username != sorted[j].Username {
			return sorted[i].Username < sorted[j].Username
		}
		return sorted[i].ID < sorted[j].ID
	})
	values := make([]attr.Value, 0, len(sorted))
	for _, user := range sorted {
		roleNames := make([]string, len(user.Roles))
		copy(roleNames, user.Roles)
		sort.Strings(roleNames)
		roles, roleDiags := types.ListValueFrom(context.Background(), types.StringType, roleNames)
		diags.Append(roleDiags...)
		objectValue, objectDiags := types.ObjectValue(attrTypes, map[string]attr.Value{
			"id":                 types.StringValue(user.ID),
			"username":           types.StringValue(user.Username),
			"email":              types.StringValue(user.Email),
			"first_name":         optionalRESTString(user.FirstName),
			"last_name":          optionalRESTString(user.LastName),
			"is_service_account": types.BoolValue(user.IsServiceAccount),
			"is_deprovisioned":   types.BoolValue(user.IsDeprovisioned),
			"roles":              roles,
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
			"session_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional name used to reuse the same read-scaling session across embed requests. Must be non-blank when set. Conflicts with `session_hint`.",
				Validators:          diveembed.SessionNameValidators(),
			},
			"session_hint": schema.StringAttribute{
				Optional:            true,
				DeprecationMessage:  diveembed.SessionHintDeprecationMessage,
				MarkdownDescription: "Deprecated alias for `session_name`. Must be non-blank when set.",
				Validators:          diveembed.SessionHintValidators(),
			},
			"version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Optional Dive version to embed. Must be a positive integer. Omit it to embed the current version.",
				Validators:          diveembed.VersionValidators(),
			},
			"required_resources": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Optional override for the databases and shares the Dive renders against. When set, it replaces the Dive's declared required resources for this session. An empty list declares no resources, so the Dive attaches the whole workspace of the session user. The JSON encoding of the list must be at most 8192 bytes.",
				Validators:          diveembed.RequiredResourcesValidators(),
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"url": schema.StringAttribute{
							Required:            true,
							Sensitive:           true,
							MarkdownDescription: "MotherDuck share URL the Dive renders against. This value is sensitive and can be sourced from `motherduck_share.url`. Must be non-blank.",
							Validators:          diveembed.RequiredResourceURLValidators(),
						},
						"alias": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional alias exposed to the Dive content for this resource.",
						},
					},
				},
			},
			"initial_state": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional JSON object that seeds the embedded Dive's UI state. Each key is read by the matching `useDiveState` call in the Dive. Build it with `jsonencode()`. The JSON encoding must be at most 64 KiB.",
				Validators:          diveembed.InitialStateValidators(),
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
