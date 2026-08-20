package datasources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &scalarDataSource{}
	_ datasource.DataSourceWithConfigure = &scalarDataSource{}
	_ datasource.DataSource              = &rowsDataSource{}
	_ datasource.DataSourceWithConfigure = &rowsDataSource{}
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

type rowSpec struct {
	name             string
	description      string
	requiredFunction string
	attrs            []string
	requiredAttrs    []string
	typedRows        []typedRowAttribute
	build            func(rowsModel) (string, error)
	postProcess      func([]map[string]any) []map[string]any
}

type typedRowAttribute struct {
	name        string
	source      string
	description string
	sensitive   bool
}

type rowsDataSource struct {
	baseDataSource
	spec rowSpec
}

type rowsModel struct {
	Name             types.String `tfsdk:"name"`
	DatabaseName     types.String `tfsdk:"database_name"`
	SecretName       types.String `tfsdk:"secret_name"`
	Path             types.String `tfsdk:"path"`
	DiveID           types.String `tfsdk:"dive_id"`
	FlightID         types.String `tfsdk:"flight_id"`
	GuideID          types.String `tfsdk:"guide_id"`
	RoleName         types.String `tfsdk:"role_name"`
	Username         types.String `tfsdk:"username"`
	Topic            types.String `tfsdk:"topic"`
	ReferenceType    types.String `tfsdk:"reference_type"`
	ReferenceURL     types.String `tfsdk:"reference_url"`
	ReferenceSchema  types.String `tfsdk:"reference_schema"`
	ReferenceTable   types.String `tfsdk:"reference_table"`
	ReferenceColumn  types.String `tfsdk:"reference_column"`
	ReferenceView    types.String `tfsdk:"reference_view"`
	ReferenceMacro   types.String `tfsdk:"reference_macro"`
	ReferenceUUID    types.String `tfsdk:"reference_uuid"`
	RunNumber        types.Int64  `tfsdk:"run_number"`
	Limit            types.Int64  `tfsdk:"limit"`
	Offset           types.Int64  `tfsdk:"offset"`
	IncludeOrgShares types.Bool   `tfsdk:"include_org_shares"`
	OwnerOnly        types.Bool   `tfsdk:"owner_only"`
	RowsJSON         types.String `tfsdk:"rows_json"`
}

func (d *rowsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.spec.name
}

func (d *rowsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"rows_json": schema.StringAttribute{
			Computed:            true,
			Sensitive:           true,
			MarkdownDescription: "Raw MotherDuck catalog rows encoded as JSON. Sensitive because catalog rows can include share URLs and account metadata.",
		},
	}
	for _, attr := range d.spec.attrs {
		attrs[attr] = rowAttribute(attr, d.spec.attrRequired(attr))
	}
	if len(d.spec.typedRows) > 0 {
		attrs["rows"] = d.spec.typedRowsAttribute()
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: d.spec.description,
		Attributes:          attrs,
	}
}

func (d *rowsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client := d.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	config := d.readConfig(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	query, err := d.spec.build(config)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MotherDuck data source configuration", err.Error())
		return
	}
	if d.spec.requiredFunction != "" && !d.functionAvailable(ctx, client, &resp.Diagnostics) {
		return
	}
	rowsJSON, ok := d.queryRows(ctx, client, query, &resp.Diagnostics)
	if !ok {
		return
	}
	rowsJSONValue := types.StringValue(rowsJSON)
	var typedRows types.List
	if len(d.spec.typedRows) > 0 {
		var diags diag.Diagnostics
		typedRows, diags = d.spec.typedRowsValue(rowsJSON)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	d.setState(ctx, &resp.State, config, rowsJSONValue, typedRows, resp)
}

func rowSpecs() []rowSpec {
	return []rowSpec{
		{name: "databases", description: "Reads MotherDuck database metadata from MD_INFORMATION_SCHEMA.DATABASES.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Database name."},
			{name: "uuid", description: "Database UUID."},
			{name: "created_ts", description: "Database creation timestamp."},
			{name: "database_type", source: "type", description: "Database type reported by MotherDuck."},
			{name: "transient", description: "Whether the database is transient."},
			{name: "historical_snapshot_retention", description: "Historical snapshot retention reported by MotherDuck."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.DATABASES"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "attached_databases", description: "Reads databases attached to the current MotherDuck SQL session.", requiredFunction: "md_attached_databases", build: func(m rowsModel) (string, error) {
			return "SELECT * FROM md_attached_databases()", nil
		}},
		{name: "database_snapshots", description: "Reads MotherDuck database snapshot metadata from public SQL catalog tables.", attrs: []string{"database_name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "database_name", description: "Database that owns the snapshot."},
			{name: "snapshot_id", description: "MotherDuck snapshot ID."},
			{name: "snapshot_name", description: "Snapshot name when the snapshot is named."},
			{name: "created_ts", description: "Snapshot creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS"
			if !m.DatabaseName.IsNull() {
				query += " WHERE database_name = " + sqlbuild.StringLiteral(m.DatabaseName.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY database_name, created_ts, snapshot_id", m), nil
		}},
		{name: "owned_shares", description: "Reads metadata for shares owned by the current MotherDuck account.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Share name."},
			{name: "source_database", source: "source_db_name", description: "Database backing the share."},
			{name: "access", description: "Share access mode."},
			{name: "visibility", description: "Share visibility mode."},
			{name: "update_mode", source: "update", description: "Share update mode."},
			{name: "include_pattern", description: "Catalog include patterns applied to the share."},
			{name: "url", description: "Share URL. This is sensitive because unrestricted share URLs can grant access.", sensitive: true},
			{name: "created_ts", description: "Share creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.OWNED_SHARES"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "shared_with_me", description: "Reads metadata for shares available to the current MotherDuck account.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Share name."},
			{name: "source_database", source: "source_db_name", description: "Database backing the share."},
			{name: "owner", description: "Share owner when exposed by MotherDuck."},
			{name: "url", description: "Share URL. This is sensitive because unrestricted share URLs can grant access.", sensitive: true},
			{name: "created_ts", description: "Share creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.SHARED_WITH_ME"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "secrets", description: "Reads MotherDuck-managed secret metadata without exposing secret values.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Secret name."},
			{name: "type", description: "Secret type."},
			{name: "provider", description: "Secret provider."},
			{name: "persistent", description: "Whether the secret is persistent."},
			{name: "storage", description: "Secret storage backend."},
			{name: "scope", description: "Secret scope."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT name, type, provider, persistent, storage, scope::VARCHAR FROM duckdb_secrets() WHERE storage = 'motherduck'"
			if !m.Name.IsNull() {
				query += " AND name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "buckets_for_secret", description: "Lists object-storage buckets accessible through a MotherDuck secret.", requiredFunction: "md_list_buckets_for_secret", attrs: []string{"secret_name"}, requiredAttrs: []string{"secret_name"}, build: func(m rowsModel) (string, error) {
			if m.SecretName.IsNull() {
				return "", fmt.Errorf("secret_name is required")
			}
			return "SELECT * FROM md_list_buckets_for_secret(" + sqlbuild.StringLiteral(m.SecretName.ValueString()) + ")", nil
		}},
		{name: "files", description: "Lists files for an object-storage path through MotherDuck SQL.", requiredFunction: "md_list_files", attrs: []string{"path"}, requiredAttrs: []string{"path"}, build: func(m rowsModel) (string, error) {
			if m.Path.IsNull() {
				return "", fmt.Errorf("path is required")
			}
			return "SELECT * FROM md_list_files(" + sqlbuild.StringLiteral(m.Path.ValueString()) + ")", nil
		}},
		{name: "roles", description: "Lists MotherDuck roles visible to the current account.", typedRows: []typedRowAttribute{
			{name: "role_name", description: "Role name."},
			{name: "role_type", description: "Role type."},
			{name: "included_roles", description: "Roles directly included in this role."},
			{name: "created_at", description: "Role creation timestamp."},
		}, postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			return "SHOW ALL ROLES", nil
		}},
		{name: "role_members", description: "Lists users and roles directly granted to one MotherDuck role.", requiredFunction: "md_get_role_members", attrs: []string{"role_name"}, requiredAttrs: []string{"role_name"}, typedRows: []typedRowAttribute{
			{name: "member_name", description: "User or role principal name."},
			{name: "member_type", description: "Principal type: user or role."},
			{name: "email", description: "User email when the member is a user."},
			{name: "is_service_account", description: "Whether the user member is a service account."},
			{name: "granted_at", description: "Grant creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			if m.RoleName.IsNull() {
				return "", fmt.Errorf("role_name is required")
			}
			role := sqlbuild.StringLiteral(m.RoleName.ValueString())
			return "SELECT username AS member_name, 'user' AS member_type, email, is_service_account::VARCHAR, granted_at::VARCHAR FROM MD_GET_ROLE_MEMBERS(" + role + ", 'USER') UNION ALL SELECT role_name AS member_name, 'role' AS member_type, NULL::VARCHAR AS email, NULL::VARCHAR AS is_service_account, granted_at::VARCHAR FROM MD_GET_ROLE_MEMBERS(" + role + ", 'ROLE') ORDER BY member_type, member_name", nil
		}},
		{name: "roles_for_user", description: "Lists roles granted to one MotherDuck user, including inherited membership.", attrs: []string{"username"}, requiredAttrs: []string{"username"}, typedRows: roleMembershipRows(), postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			if m.Username.IsNull() {
				return "", fmt.Errorf("username is required")
			}
			return "SHOW ROLES TO USER " + sqlbuild.QuoteIdentifier(m.Username.ValueString()), nil
		}},
		{name: "roles_for_role", description: "Lists roles granted to one MotherDuck role, including inherited membership.", attrs: []string{"role_name"}, requiredAttrs: []string{"role_name"}, typedRows: roleMembershipRows(), postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			if m.RoleName.IsNull() {
				return "", fmt.Errorf("role_name is required")
			}
			return "SHOW ROLES TO ROLE " + sqlbuild.QuoteIdentifier(m.RoleName.ValueString()), nil
		}},
		{name: "dives", description: "Lists MotherDuck Dives available to the current account.", requiredFunction: "md_list_dives", attrs: []string{"limit", "offset", "include_org_shares"}, build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			if !m.IncludeOrgShares.IsNull() {
				args["include_org_shares"] = sqlbuild.BoolLiteral(m.IncludeOrgShares.ValueBool())
			}
			return "SELECT * FROM MD_LIST_DIVES" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "dive", description: "Reads metadata for one MotherDuck Dive.", requiredFunction: "md_get_dive", attrs: []string{"dive_id"}, requiredAttrs: []string{"dive_id"}, build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			return "SELECT * FROM MD_GET_DIVE(id := " + sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID)", nil
		}},
		{name: "dive_versions", description: "Lists versions for one MotherDuck Dive.", requiredFunction: "md_list_dive_versions", attrs: []string{"dive_id"}, requiredAttrs: []string{"dive_id"}, build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			return "SELECT * FROM MD_LIST_DIVE_VERSIONS(id := " + sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID)", nil
		}},
		{name: "flights", description: "Lists MotherDuck Flights available to the current account. Callers with organization-wide Flight visibility can restrict results to their own Flights.", requiredFunction: "md_list_flights", attrs: []string{"limit", "offset", "owner_only"}, typedRows: flightSummaryRows(), build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			if !m.OwnerOnly.IsNull() {
				args["owner_only"] = fmt.Sprintf("%t", m.OwnerOnly.ValueBool())
			}
			return "SELECT * FROM MD_LIST_FLIGHTS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight", description: "Reads metadata for one MotherDuck Flight.", requiredFunction: "md_get_flight", attrs: []string{"flight_id"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			return "SELECT * FROM MD_GET_FLIGHT(flight_id := " + sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID)", nil
		}},
		{name: "flight_versions", description: "Lists versions for one MotherDuck Flight.", requiredFunction: "md_list_flight_versions", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			args := map[string]string{"flight_id": sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_FLIGHT_VERSIONS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight_runs", description: "Lists runs for one MotherDuck Flight.", requiredFunction: "md_list_flight_runs", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			args := map[string]string{"flight_id": sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_FLIGHT_RUNS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight_logs", description: "Reads logs for one MotherDuck Flight run.", requiredFunction: "md_get_flight_logs", attrs: []string{"flight_id", "run_number"}, requiredAttrs: []string{"flight_id", "run_number"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() || m.RunNumber.IsNull() {
				return "", fmt.Errorf("flight_id and run_number are required")
			}
			return fmt.Sprintf("SELECT * FROM MD_GET_FLIGHT_LOGS(flight_id := %s::UUID, run_number := %d)", sqlbuild.StringLiteral(m.FlightID.ValueString()), m.RunNumber.ValueInt64()), nil
		}},
		{name: "guides", description: "Lists MotherDuck Guides visible to the current account, optionally filtered by topic or referenced object.", requiredFunction: "md_list_guides", attrs: []string{"topic", "reference_type", "reference_url", "reference_schema", "reference_table", "reference_column", "reference_view", "reference_macro", "reference_uuid", "limit", "offset"}, typedRows: guideSummaryRows(), build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Topic.IsNull() {
				args["topic"] = sqlbuild.StringLiteral(m.Topic.ValueString())
			}
			reference, hasReference, err := guideReferenceFilterArg(m)
			if err != nil {
				return "", err
			}
			if hasReference {
				args["reference"] = reference
			}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_GUIDES" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "guide", description: "Reads one MotherDuck Guide, including its current content and references.", requiredFunction: "md_get_guide", attrs: []string{"guide_id"}, requiredAttrs: []string{"guide_id"}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			return "SELECT * FROM MD_GET_GUIDE(id := " + sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID)", nil
		}},
		{name: "guide_versions", description: "Lists version history for one MotherDuck Guide without returning content.", requiredFunction: "md_list_guide_versions", attrs: []string{"guide_id", "limit", "offset"}, requiredAttrs: []string{"guide_id"}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			args := map[string]string{"id": sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_GUIDE_VERSIONS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "guide_grantees", description: "Lists the direct roles or organization configured to read one MotherDuck Guide.", requiredFunction: "md_list_guide_grantees", attrs: []string{"guide_id"}, requiredAttrs: []string{"guide_id"}, typedRows: []typedRowAttribute{
			{name: "grantee_name", description: "Role or organization name."},
			{name: "grantee_type", description: "Grantee type: role or organization."},
			{name: "privilege", description: "Granted Guide privilege."},
			{name: "granted_at", description: "Grant creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			return "SELECT * FROM MD_LIST_GUIDE_GRANTEES(id := " + sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID) ORDER BY grantee_type, grantee_name", nil
		}},
	}
}

func roleMembershipRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "role_name", description: "Role name."},
		{name: "role_type", description: "Role type."},
		{name: "is_direct", description: "Whether the membership was granted directly."},
		{name: "granted_at", description: "Direct grant creation timestamp when available."},
	}
}

func guideSummaryRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "id", description: "Guide ID."},
		{name: "topic", description: "Optional slash-separated guide topic."},
		{name: "title", description: "Guide title."},
		{name: "description", description: "Guide description."},
		{name: "owner_id", description: "Guide owner ID."},
		{name: "owner_name", description: "Guide owner name."},
		{name: "access", description: "Guide access mode."},
		{name: "current_version", description: "Current guide version."},
		{name: "created_at", description: "Guide creation timestamp."},
		{name: "updated_at", description: "Guide update timestamp."},
	}
}

func flightSummaryRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "flight_id", description: "Flight ID."},
		{name: "flight_name", description: "Flight name."},
		{name: "schedule_cron", description: "Flight cron schedule."},
		{name: "schedule_status", description: "Flight schedule status."},
		{name: "status", description: "Flight status."},
		{name: "current_version", description: "Current Flight version."},
		{name: "created_at", description: "Flight creation timestamp."},
		{name: "updated_at", description: "Flight update timestamp."},
		{name: "owner_name", description: "Flight owner name when exposed to the caller."},
	}
}

func guideReferenceFilterArg(model rowsModel) (string, bool, error) {
	values := []types.String{
		model.ReferenceType, model.ReferenceURL, model.ReferenceSchema, model.ReferenceTable,
		model.ReferenceColumn, model.ReferenceView, model.ReferenceMacro, model.ReferenceUUID,
	}
	hasValue := false
	for _, value := range values {
		if !value.IsNull() && !value.IsUnknown() {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return "", false, nil
	}
	if model.ReferenceType.IsNull() || model.ReferenceType.IsUnknown() {
		return "", false, fmt.Errorf("reference_type is required when filtering Guides by a reference")
	}
	refType := model.ReferenceType.ValueString()
	switch refType {
	case "catalog":
		if model.ReferenceURL.IsNull() || !strings.HasPrefix(model.ReferenceURL.ValueString(), "md:") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_url beginning with the MotherDuck md scheme")
		}
		narrowings := 0
		for _, value := range []types.String{model.ReferenceTable, model.ReferenceView, model.ReferenceMacro} {
			if !value.IsNull() && !value.IsUnknown() && value.ValueString() != "" {
				narrowings++
			}
		}
		if narrowings > 1 {
			return "", false, fmt.Errorf("catalog Guide reference filters may set at most one of reference_table, reference_view, or reference_macro")
		}
		if !model.ReferenceColumn.IsNull() && model.ReferenceColumn.ValueString() != "" &&
			(model.ReferenceTable.IsNull() || model.ReferenceTable.ValueString() == "") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_table when reference_column is set")
		}
		if narrowings > 0 && (model.ReferenceSchema.IsNull() || model.ReferenceSchema.ValueString() == "") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_schema when narrowing to a table, view, or macro")
		}
	case "dive", "flight", "guide":
		if model.ReferenceUUID.IsNull() {
			return "", false, fmt.Errorf("%s Guide reference filters require reference_uuid", refType)
		}
	default:
		return "", false, fmt.Errorf("reference_type must be catalog, dive, flight, or guide")
	}
	fields := []string{
		"'type': " + rowReferenceString(model.ReferenceType, "VARCHAR"),
		"'url': " + rowReferenceString(model.ReferenceURL, "VARCHAR"),
		"'schema': " + rowReferenceString(model.ReferenceSchema, "VARCHAR"),
		"'table': " + rowReferenceString(model.ReferenceTable, "VARCHAR"),
		"'column': " + rowReferenceString(model.ReferenceColumn, "VARCHAR"),
		"'view': " + rowReferenceString(model.ReferenceView, "VARCHAR"),
		"'macro': " + rowReferenceString(model.ReferenceMacro, "VARCHAR"),
		"'uuid': " + rowReferenceString(model.ReferenceUUID, "UUID"),
		"'description': NULL::VARCHAR",
	}
	return "{" + strings.Join(fields, ", ") + "}", true, nil
}

func rowReferenceString(value types.String, sqlType string) string {
	if value.IsNull() || value.IsUnknown() {
		return "NULL::" + sqlType
	}
	literal := sqlbuild.StringLiteral(value.ValueString())
	if sqlType == "UUID" {
		return literal + "::UUID"
	}
	return literal
}

func (d *rowsDataSource) queryRows(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, query string, diags *diag.Diagnostics) (string, bool) {
	var rowsJSON string
	err := retry.SQL(ctx, func() error {
		var readErr error
		rowsJSON, readErr = client.QueryRowsJSON(ctx, query)
		return readErr
	})
	if err != nil {
		if mdsql.IsUnsupportedCommand(err) {
			diags.AddError(
				"MotherDuck SQL command unavailable",
				fmt.Sprintf("%s is not supported by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_%s data source.", query, d.spec.name),
			)
			return "", false
		}
		diags.AddError("Unable to read MotherDuck rows data source", err.Error())
		return "", false
	}
	if d.spec.postProcess == nil {
		return rowsJSON, true
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		diags.AddError("Unable to decode MotherDuck rows data source", err.Error())
		return "", false
	}
	data, err := json.Marshal(d.spec.postProcess(rows))
	if err != nil {
		diags.AddError("Unable to encode MotherDuck rows data source", err.Error())
		return "", false
	}
	return string(data), true
}

func sortRowsBy(field string) func([]map[string]any) []map[string]any {
	return func(rows []map[string]any) []map[string]any {
		sort.SliceStable(rows, func(i, j int) bool {
			left, _ := rows[i][field].(string)
			right, _ := rows[j][field].(string)
			return left < right
		})
		return rows
	}
}

func (d *rowsDataSource) functionAvailable(ctx context.Context, client interface {
	Exists(context.Context, string, ...any) (bool, error)
}, diags *diag.Diagnostics) bool {
	var available bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		available, existsErr = client.Exists(ctx, "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = lower(?)", d.spec.requiredFunction)
		return existsErr
	})
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return false
	}
	if !available {
		diags.AddError(
			"MotherDuck SQL function unavailable",
			fmt.Sprintf("%s is not exposed by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the motherduck_%s data source.", d.spec.requiredFunction, d.spec.name),
		)
		return false
	}
	return true
}

func (s rowSpec) attrRequired(name string) bool {
	for _, required := range s.requiredAttrs {
		if required == name {
			return true
		}
	}
	return false
}

func rowAttribute(name string, required bool) schema.Attribute {
	switch name {
	case "limit":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source limit", 0)}, "Maximum number of rows to return when the underlying MotherDuck catalog function supports limits.")
	case "offset":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source offset", 0)}, "Number of rows to skip when the underlying MotherDuck catalog function supports offsets.")
	case "run_number":
		return rowInt64Attribute(required, []validator.Int64{tfvalidators.Int64Min("MotherDuck data source run number", 1)}, "MotherDuck Flight run number.")
	case "dive_id":
		return rowStringAttribute(required, uuidValidators(), "Dive ID. Must be a UUID with no leading or trailing whitespace.")
	case "flight_id":
		return rowStringAttribute(required, uuidValidators(), "Flight ID. Must be a UUID with no leading or trailing whitespace.")
	case "guide_id":
		return rowStringAttribute(required, uuidValidators(), "Guide ID. Must be a UUID with no leading or trailing whitespace.")
	case "role_name":
		return rowStringAttribute(required, nil, "MotherDuck role name.")
	case "username":
		return rowStringAttribute(required, nil, "MotherDuck user or service-account principal.")
	case "topic":
		return rowStringAttribute(required, nil, "Optional slash-separated Guide topic subtree filter.")
	case "reference_type":
		return rowStringAttribute(required, nil, "Optional Guide reference filter type: catalog, dive, flight, or guide.")
	case "reference_url":
		return rowStringAttribute(required, nil, "MotherDuck database or share URL for a catalog Guide reference filter.")
	case "reference_schema":
		return rowStringAttribute(required, nil, "Optional schema narrowing for a catalog Guide reference filter.")
	case "reference_table":
		return rowStringAttribute(required, nil, "Optional table narrowing for a catalog Guide reference filter.")
	case "reference_column":
		return rowStringAttribute(required, nil, "Optional column narrowing for a catalog Guide reference filter.")
	case "reference_view":
		return rowStringAttribute(required, nil, "Optional view narrowing for a catalog Guide reference filter.")
	case "reference_macro":
		return rowStringAttribute(required, nil, "Optional macro narrowing for a catalog Guide reference filter.")
	case "reference_uuid":
		return rowStringAttribute(required, uuidValidators(), "Dive, Flight, or Guide UUID for a Guide reference filter.")
	case "include_org_shares":
		if required {
			return schema.BoolAttribute{Required: true, MarkdownDescription: "Whether to include organization-shared Dives when the MotherDuck function supports it."}
		}
		return schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether to include organization-shared Dives when the MotherDuck function supports it."}
	case "owner_only":
		if required {
			return schema.BoolAttribute{Required: true, MarkdownDescription: "Whether to restrict Flight listings to Flights owned by the current user."}
		}
		return schema.BoolAttribute{Optional: true, MarkdownDescription: "Whether to restrict Flight listings to Flights owned by the current user. Ignored for callers who can only view their own Flights."}
	case "name":
		return rowStringAttribute(required, nil, "Optional exact object name filter.")
	case "database_name":
		return rowStringAttribute(required, nil, "MotherDuck database name filter.")
	case "secret_name":
		return rowStringAttribute(required, nil, "MotherDuck secret name.")
	case "path":
		return rowStringAttribute(required, nil, "Object-storage path to list through MotherDuck SQL.")
	default:
		return rowStringAttribute(required, nil, "MotherDuck data source argument.")
	}
}

func rowInt64Attribute(required bool, validators []validator.Int64, description string) schema.Attribute {
	if required {
		return schema.Int64Attribute{Required: true, Validators: validators, MarkdownDescription: description}
	}
	return schema.Int64Attribute{Optional: true, Validators: validators, MarkdownDescription: description}
}

func rowStringAttribute(required bool, validators []validator.String, description string) schema.Attribute {
	if required {
		return schema.StringAttribute{Required: true, Validators: validators, MarkdownDescription: description}
	}
	return schema.StringAttribute{Optional: true, Validators: validators, MarkdownDescription: description}
}

func (d *rowsDataSource) readConfig(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) rowsModel {
	var config rowsModel
	for _, attr := range d.spec.attrs {
		switch attr {
		case "name":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Name)...)
		case "database_name":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.DatabaseName)...)
		case "secret_name":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.SecretName)...)
		case "path":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Path)...)
		case "dive_id":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.DiveID)...)
		case "flight_id":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.FlightID)...)
		case "guide_id":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.GuideID)...)
		case "role_name":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.RoleName)...)
		case "username":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Username)...)
		case "topic":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Topic)...)
		case "reference_type":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceType)...)
		case "reference_url":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceURL)...)
		case "reference_schema":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceSchema)...)
		case "reference_table":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceTable)...)
		case "reference_column":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceColumn)...)
		case "reference_view":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceView)...)
		case "reference_macro":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceMacro)...)
		case "reference_uuid":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.ReferenceUUID)...)
		case "run_number":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.RunNumber)...)
		case "limit":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Limit)...)
		case "offset":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.Offset)...)
		case "include_org_shares":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.IncludeOrgShares)...)
		case "owner_only":
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attr), &config.OwnerOnly)...)
		default:
			resp.Diagnostics.AddError("Invalid row data source schema", fmt.Sprintf("Unsupported attribute %q configured for %s.", attr, d.spec.name))
		}
	}
	return config
}

func (d *rowsDataSource) setState(ctx context.Context, state interface {
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, config rowsModel, rowsJSON types.String, typedRows types.List, resp *datasource.ReadResponse) {
	for _, attr := range d.spec.attrs {
		switch attr {
		case "name":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Name)...)
		case "database_name":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.DatabaseName)...)
		case "secret_name":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.SecretName)...)
		case "path":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Path)...)
		case "dive_id":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.DiveID)...)
		case "flight_id":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.FlightID)...)
		case "guide_id":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.GuideID)...)
		case "role_name":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.RoleName)...)
		case "username":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Username)...)
		case "topic":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Topic)...)
		case "reference_type":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceType)...)
		case "reference_url":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceURL)...)
		case "reference_schema":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceSchema)...)
		case "reference_table":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceTable)...)
		case "reference_column":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceColumn)...)
		case "reference_view":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceView)...)
		case "reference_macro":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceMacro)...)
		case "reference_uuid":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.ReferenceUUID)...)
		case "run_number":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.RunNumber)...)
		case "limit":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Limit)...)
		case "offset":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.Offset)...)
		case "include_org_shares":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.IncludeOrgShares)...)
		case "owner_only":
			resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root(attr), config.OwnerOnly)...)
		}
	}
	resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root("rows_json"), rowsJSON)...)
	if len(d.spec.typedRows) > 0 {
		resp.Diagnostics.Append(state.SetAttribute(ctx, path.Root("rows"), typedRows)...)
	}
}

func (s rowSpec) typedRowsAttribute() schema.ListNestedAttribute {
	nested := make(map[string]schema.Attribute, len(s.typedRows))
	for _, rowAttr := range s.typedRows {
		nested[rowAttr.name] = schema.StringAttribute{
			Computed:            true,
			Sensitive:           rowAttr.sensitive,
			MarkdownDescription: rowAttr.description,
		}
	}
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Typed catalog rows for stable MotherDuck metadata. `rows_json` remains available for raw server columns.",
		NestedObject:        schema.NestedAttributeObject{Attributes: nested},
	}
}

func (s rowSpec) typedRowsValue(rowsJSON string) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	attrTypes := make(map[string]attr.Type, len(s.typedRows))
	for _, rowAttr := range s.typedRows {
		attrTypes[rowAttr.name] = types.StringType
	}
	objectType := types.ObjectType{AttrTypes: attrTypes}

	var rawRows []map[string]any
	if err := json.Unmarshal([]byte(rowsJSON), &rawRows); err != nil {
		diags.AddError("Unable to decode typed MotherDuck rows", err.Error())
		return types.ListNull(objectType), diags
	}
	values := make([]attr.Value, 0, len(rawRows))
	for _, rawRow := range rawRows {
		rowValues := make(map[string]attr.Value, len(s.typedRows))
		for _, rowAttr := range s.typedRows {
			source := rowAttr.source
			if source == "" {
				source = rowAttr.name
			}
			rowValues[rowAttr.name] = typedRowStringValue(rawRow[source])
		}
		objectValue, objectDiags := types.ObjectValue(attrTypes, rowValues)
		diags.Append(objectDiags...)
		values = append(values, objectValue)
	}
	if diags.HasError() {
		return types.ListNull(objectType), diags
	}
	listValue, listDiags := types.ListValue(objectType, values)
	diags.Append(listDiags...)
	return listValue, diags
}

func typedRowStringValue(value any) types.String {
	switch v := value.(type) {
	case nil:
		return types.StringNull()
	case string:
		return types.StringValue(v)
	case bool:
		return types.StringValue(fmt.Sprintf("%t", v))
	case float64:
		return types.StringValue(fmt.Sprintf("%v", v))
	default:
		payload, err := json.Marshal(v)
		if err != nil {
			return types.StringValue(fmt.Sprintf("%v", v))
		}
		return types.StringValue(string(payload))
	}
}

func appendRowLimitOffset(query string, model rowsModel) string {
	if !model.Limit.IsNull() && !model.Limit.IsUnknown() {
		query += fmt.Sprintf(" LIMIT %d", model.Limit.ValueInt64())
	}
	if !model.Offset.IsNull() && !model.Offset.IsUnknown() {
		query += fmt.Sprintf(" OFFSET %d", model.Offset.ValueInt64())
	}
	return query
}
