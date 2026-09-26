package resources

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// databaseIcebergModel holds the options of a `TYPE ICEBERG` database.
// MotherDuck does not expose database options for read-back, so every value is
// config-owned and state keeps what was last applied.
type databaseIcebergModel struct {
	Secret                         types.String `tfsdk:"secret"`
	DefaultSchema                  types.String `tfsdk:"default_schema"`
	Endpoint                       types.String `tfsdk:"endpoint"`
	Warehouse                      types.String `tfsdk:"warehouse"`
	EndpointType                   types.String `tfsdk:"endpoint_type"`
	ReadOnly                       types.Bool   `tfsdk:"read_only"`
	DefaultRegion                  types.String `tfsdk:"default_region"`
	AccessDelegationMode           types.String `tfsdk:"access_delegation_mode"`
	MaxTableStaleness              types.String `tfsdk:"max_table_staleness"`
	StageCreateTables              types.Bool   `tfsdk:"stage_create_tables"`
	SkipCreateTableMetadataUpdates types.Bool   `tfsdk:"skip_create_table_metadata_updates"`
	DisableMultiTableCommit        types.Bool   `tfsdk:"disable_multi_table_commit"`
	RemoveFilesOnDelete            types.Bool   `tfsdk:"remove_files_on_delete"`
	PurgeRequested                 types.Bool   `tfsdk:"purge_requested"`
	SupportNestedNamespaces        types.Bool   `tfsdk:"support_nested_namespaces"`
	EncodeEntirePrefix             types.Bool   `tfsdk:"encode_entire_prefix"`
}

var databaseIcebergEndpointTypes = []string{"glue", "s3_tables"}

var databaseIcebergAccessDelegationModes = []string{"none", "vended_credentials"}

// icebergStringOption and icebergBoolOption describe how one model field maps
// to a MotherDuck database option. Options that identify the catalog cannot be
// altered, so a change to them replaces the database.
type icebergStringOption struct {
	attribute string
	sqlName   string
	value     func(*databaseIcebergModel) types.String
	immutable bool
	required  bool
}

type icebergBoolOption struct {
	attribute string
	sqlName   string
	value     func(*databaseIcebergModel) types.Bool
	immutable bool
	// presenceEnables marks options that the Iceberg extension turns on
	// whenever they are present, whatever their value. They are emitted only
	// when true and cleared with NULL otherwise.
	presenceEnables bool
}

// "secret" is a keyword, so it is quoted in both CREATE and ALTER.
var databaseIcebergStringOptions = []icebergStringOption{
	{attribute: "secret", sqlName: `"secret"`, value: func(m *databaseIcebergModel) types.String { return m.Secret }, required: true},
	{attribute: "default_schema", sqlName: "DEFAULT_SCHEMA", value: func(m *databaseIcebergModel) types.String { return m.DefaultSchema }, required: true},
	{attribute: "endpoint", sqlName: "ENDPOINT", value: func(m *databaseIcebergModel) types.String { return m.Endpoint }, immutable: true},
	{attribute: "warehouse", sqlName: "WAREHOUSE", value: func(m *databaseIcebergModel) types.String { return m.Warehouse }, immutable: true},
	{attribute: "endpoint_type", sqlName: "ENDPOINT_TYPE", value: func(m *databaseIcebergModel) types.String { return m.EndpointType }, immutable: true},
	{attribute: "default_region", sqlName: "DEFAULT_REGION", value: func(m *databaseIcebergModel) types.String { return m.DefaultRegion }},
	{attribute: "access_delegation_mode", sqlName: "ACCESS_DELEGATION_MODE", value: func(m *databaseIcebergModel) types.String { return m.AccessDelegationMode }},
	{attribute: "max_table_staleness", sqlName: "MAX_TABLE_STALENESS", value: func(m *databaseIcebergModel) types.String { return m.MaxTableStaleness }},
}

var databaseIcebergBoolOptions = []icebergBoolOption{
	{attribute: "read_only", sqlName: "READ_ONLY", value: func(m *databaseIcebergModel) types.Bool { return m.ReadOnly }, immutable: true},
	{attribute: "stage_create_tables", sqlName: "STAGE_CREATE_TABLES", value: func(m *databaseIcebergModel) types.Bool { return m.StageCreateTables }},
	{attribute: "skip_create_table_metadata_updates", sqlName: "SKIP_CREATE_TABLE_METADATA_UPDATES", value: func(m *databaseIcebergModel) types.Bool { return m.SkipCreateTableMetadataUpdates }},
	{attribute: "disable_multi_table_commit", sqlName: "DISABLE_MULTI_TABLE_COMMIT", value: func(m *databaseIcebergModel) types.Bool { return m.DisableMultiTableCommit }},
	{attribute: "remove_files_on_delete", sqlName: "REMOVE_FILES_ON_DELETE", value: func(m *databaseIcebergModel) types.Bool { return m.RemoveFilesOnDelete }},
	{attribute: "purge_requested", sqlName: "PURGE_REQUESTED", value: func(m *databaseIcebergModel) types.Bool { return m.PurgeRequested }},
	{attribute: "support_nested_namespaces", sqlName: "SUPPORT_NESTED_NAMESPACES", value: func(m *databaseIcebergModel) types.Bool { return m.SupportNestedNamespaces }},
	{attribute: "encode_entire_prefix", sqlName: "ENCODE_ENTIRE_PREFIX", value: func(m *databaseIcebergModel) types.Bool { return m.EncodeEntirePrefix }, presenceEnables: true},
}

func databaseIcebergAttribute() schema.SingleNestedAttribute {
	identity := "Identifies the catalog, so changing it replaces the database."
	mutable := "Changed in place with `ALTER DATABASE`. Removing it clears the option so the catalog default applies."
	return schema.SingleNestedAttribute{
		Optional: true,
		MarkdownDescription: "Options for an Iceberg REST catalog database. Required when `database_type = \"iceberg\"` and invalid otherwise. " +
			"MotherDuck does not report these options back, so Terraform keeps the configured values and does not detect changes made outside Terraform. " +
			"Removing the whole block together with `database_type` keeps the database and stops managing its options. " +
			"Catalog credentials belong in a `motherduck_secret`, not here.",
		Attributes: map[string]schema.Attribute{
			"secret": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the MotherDuck Iceberg or S3 secret that holds the catalog credentials. Changed in place with `ALTER DATABASE`.",
			},
			"default_schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Catalog namespace used to resolve unqualified table names. It must already exist in the catalog. Changed in place with `ALTER DATABASE`.",
			},
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "URL of the Iceberg REST catalog. Required unless the secret sets it or `endpoint_type` derives it. " + identity,
				PlanModifiers:       icebergIdentityStringModifiers(),
			},
			"warehouse": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Catalog warehouse identifier. For Amazon S3 Tables this is the table bucket ARN, and for AWS Glue it is the AWS account ID. " + identity,
				PlanModifiers:       icebergIdentityStringModifiers(),
			},
			"endpoint_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Well-known catalog flavor, `s3_tables` or `glue`. " + identity,
				PlanModifiers:       icebergIdentityStringModifiers(),
			},
			"read_only": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Attach the catalog as read-only. " + identity,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplaceIf(icebergIdentityBoolReplace, icebergIdentityReplaceDescription, icebergIdentityReplaceDescription)},
			},
			"default_region": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Per-catalog region override. Defaults to the MotherDuck organization region. " + mutable,
			},
			"access_delegation_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "`vended_credentials` requests short-lived, table-scoped credentials from the catalog. `none` uses the secret's credentials directly. " + mutable,
			},
			"max_table_staleness": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "DuckDB interval, such as `10 minutes`, for how long cached table metadata may be reused before it is reloaded. " + mutable,
			},
			"stage_create_tables": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Create tables through the catalog's stage-create flow. Turn it off for catalogs that do not support staged creates. " + mutable,
			},
			"skip_create_table_metadata_updates": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip the metadata update that follows a non-staged `CREATE TABLE`, for catalogs that reject it. " + mutable,
			},
			"disable_multi_table_commit": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Commit tables one at a time instead of using the catalog's multi-table commit endpoint. " + mutable,
			},
			"remove_files_on_delete": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Delete the underlying data files when a table is dropped. Turn it off for catalogs that handle their own cleanup, such as S3 Tables. " + mutable,
			},
			"purge_requested": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Ask the catalog to purge table data on `DROP TABLE`. " + mutable,
			},
			"support_nested_namespaces": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Address nested catalog namespaces as multi-level schema names. " + mutable,
			},
			"encode_entire_prefix": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Send the catalog prefix as a single URL-encoded path component. MotherDuck turns this on whenever the option is present, so `false` and omitting it both leave it off. " + mutable,
			},
		},
	}
}

const icebergIdentityReplaceDescription = "Changing an option that identifies the Iceberg catalog replaces the database."

func icebergIdentityStringModifiers() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplaceIf(icebergIdentityStringReplace, icebergIdentityReplaceDescription, icebergIdentityReplaceDescription)}
}

// Identity options replace the database only while Terraform manages the
// options in both state and plan. An imported database has no options in
// state, so adding the block adopts the configured values without replacing
// the database, and removing the block stops managing them.
func icebergIdentityStringReplace(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = icebergOptionsManaged(ctx, req.State.GetAttribute, req.Plan.GetAttribute, &resp.Diagnostics)
}

// An unset read_only attaches the catalog read-write, so moving between unset
// and false changes nothing and does not replace the database.
func icebergIdentityBoolReplace(ctx context.Context, req planmodifier.BoolRequest, resp *boolplanmodifier.RequiresReplaceIfFuncResponse) {
	if !req.StateValue.ValueBool() && !req.PlanValue.IsUnknown() && !req.PlanValue.ValueBool() {
		return
	}
	resp.RequiresReplace = icebergOptionsManaged(ctx, req.State.GetAttribute, req.Plan.GetAttribute, &resp.Diagnostics)
}

type attributeGetter func(context.Context, path.Path, any) diag.Diagnostics

func icebergOptionsManaged(ctx context.Context, state, plan attributeGetter, diags *diag.Diagnostics) bool {
	var prior, planned types.Object
	diags.Append(state(ctx, path.Root("iceberg"), &prior)...)
	diags.Append(plan(ctx, path.Root("iceberg"), &planned)...)
	if diags.HasError() {
		return false
	}
	return !prior.IsNull() && !prior.IsUnknown() && !planned.IsNull()
}

func databaseIcebergOptions(ctx context.Context, value types.Object, diags *diag.Diagnostics) *databaseIcebergModel {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var model databaseIcebergModel
	diags.Append(value.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil
	}
	return &model
}

func validateDatabaseIcebergConfig(ctx context.Context, config databaseModel, databaseType string, databaseTypeKnown bool, diags *diag.Diagnostics) {
	icebergPath := path.Root("iceberg")
	if databaseTypeKnown && databaseType == "iceberg" {
		if config.Iceberg.IsNull() {
			diags.AddAttributeError(icebergPath, "Invalid MotherDuck database configuration", "`database_type = \"iceberg\"` requires an `iceberg` block with at least `secret` and `default_schema`.")
		}
		if !config.Transient.IsNull() && !config.Transient.IsUnknown() && config.Transient.ValueBool() {
			diags.AddAttributeError(path.Root("transient"), "Invalid MotherDuck database configuration", "`transient = true` cannot be combined with `database_type = \"iceberg\"`.")
		}
		if !config.SnapshotRetentionDays.IsNull() && !config.SnapshotRetentionDays.IsUnknown() {
			diags.AddAttributeError(path.Root("snapshot_retention_days"), "Invalid MotherDuck database configuration", "`snapshot_retention_days` is not supported for Iceberg databases. Snapshots are managed by the Iceberg catalog.")
		}
	}
	if config.Iceberg.IsNull() || config.Iceberg.IsUnknown() {
		return
	}
	if databaseTypeKnown && databaseType != "iceberg" {
		diags.AddAttributeError(icebergPath, "Invalid MotherDuck database configuration", "`iceberg` is only valid when `database_type = \"iceberg\"`.")
	}
	options := databaseIcebergOptions(ctx, config.Iceberg, diags)
	if options == nil {
		return
	}
	for _, option := range databaseIcebergStringOptions {
		value := option.value(options)
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		if strings.TrimSpace(value.ValueString()) == "" {
			diags.AddAttributeError(icebergPath.AtName(option.attribute), "Invalid MotherDuck database configuration", "`"+option.attribute+"` must be omitted or set to a non-empty value.")
		}
	}
	validateIcebergEnum(icebergPath.AtName("endpoint_type"), options.EndpointType, databaseIcebergEndpointTypes, diags)
	validateIcebergEnum(icebergPath.AtName("access_delegation_mode"), options.AccessDelegationMode, databaseIcebergAccessDelegationModes, diags)
}

func validateIcebergEnum(attributePath path.Path, value types.String, allowed []string, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() || strings.TrimSpace(value.ValueString()) == "" {
		return
	}
	for _, candidate := range allowed {
		if value.ValueString() == candidate {
			return
		}
	}
	diags.AddAttributeError(attributePath, "Invalid MotherDuck database configuration", "Value must be one of `"+strings.Join(allowed, "`, `")+"`.")
}

// databaseIcebergCreateOptions renders the CREATE DATABASE options that follow
// `TYPE ICEBERG`, in a fixed order.
func databaseIcebergCreateOptions(options *databaseIcebergModel) []string {
	if options == nil {
		return nil
	}
	rendered := make([]string, 0, len(databaseIcebergStringOptions)+len(databaseIcebergBoolOptions))
	for _, option := range databaseIcebergStringOptions {
		if value := option.value(options); knownNonNullString(value) {
			rendered = append(rendered, option.sqlName+" "+sqlbuild.StringLiteral(value.ValueString()))
		}
	}
	for _, option := range databaseIcebergBoolOptions {
		value := option.value(options)
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		if option.presenceEnables {
			if value.ValueBool() {
				rendered = append(rendered, option.sqlName+" TRUE")
			}
			continue
		}
		if value.ValueBool() {
			rendered = append(rendered, option.sqlName+" TRUE")
		} else {
			rendered = append(rendered, option.sqlName+" FALSE")
		}
	}
	return rendered
}

// databaseIcebergAlterAssignments renders the `ALTER DATABASE ... SET`
// assignments that move the applied options to the planned ones. It returns
// nothing when Terraform stops managing the options. When the prior state has
// no options, as after an import, every configured mutable option is applied
// and nothing is cleared.
func databaseIcebergAlterAssignments(planned, prior *databaseIcebergModel) []string {
	if planned == nil {
		return nil
	}
	adopting := prior == nil
	if adopting {
		prior = &databaseIcebergModel{}
	}
	assignments := make([]string, 0)
	for _, option := range databaseIcebergStringOptions {
		if option.immutable {
			continue
		}
		want, have := option.value(planned), option.value(prior)
		if !adopting && want.Equal(have) {
			continue
		}
		switch {
		case knownNonNullString(want):
			assignments = append(assignments, option.sqlName+" = "+sqlbuild.StringLiteral(want.ValueString()))
		case !adopting && !option.required && !have.IsNull():
			assignments = append(assignments, option.sqlName+" = NULL")
		}
	}
	for _, option := range databaseIcebergBoolOptions {
		if option.immutable {
			continue
		}
		want, have := option.value(planned), option.value(prior)
		if option.presenceEnables {
			wantOn := !want.IsNull() && !want.IsUnknown() && want.ValueBool()
			haveOn := !have.IsNull() && !have.IsUnknown() && have.ValueBool()
			switch {
			case wantOn && (adopting || !haveOn):
				assignments = append(assignments, option.sqlName+" = 'true'")
			case !wantOn && haveOn && !adopting:
				assignments = append(assignments, option.sqlName+" = NULL")
			}
			continue
		}
		if !adopting && want.Equal(have) {
			continue
		}
		switch {
		case !want.IsNull() && !want.IsUnknown():
			assignments = append(assignments, option.sqlName+" = "+sqlbuild.StringLiteral(sqlbuild.BoolLiteral(want.ValueBool())))
		case !adopting && !have.IsNull():
			assignments = append(assignments, option.sqlName+" = NULL")
		}
	}
	return assignments
}

func knownNonNullString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func databaseIcebergAttributeTypes() map[string]attr.Type {
	attrTypes := make(map[string]attr.Type, len(databaseIcebergStringOptions)+len(databaseIcebergBoolOptions))
	for _, option := range databaseIcebergStringOptions {
		attrTypes[option.attribute] = basetypes.StringType{}
	}
	for _, option := range databaseIcebergBoolOptions {
		attrTypes[option.attribute] = basetypes.BoolType{}
	}
	return attrTypes
}
