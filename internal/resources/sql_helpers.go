package resources

import (
	"context"
	stdsql "database/sql"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

var (
	_ resource.Resource                   = &databaseResource{}
	_ resource.ResourceWithConfigure      = &databaseResource{}
	_ resource.ResourceWithImportState    = &databaseResource{}
	_ resource.ResourceWithValidateConfig = &databaseResource{}
	_ resource.Resource                   = &schemaResource{}
	_ resource.ResourceWithConfigure      = &schemaResource{}
	_ resource.ResourceWithImportState    = &schemaResource{}
	_ resource.Resource                   = &tableResource{}
	_ resource.ResourceWithConfigure      = &tableResource{}
	_ resource.ResourceWithImportState    = &tableResource{}
	_ resource.ResourceWithModifyPlan     = &tableResource{}
	_ resource.ResourceWithValidateConfig = &tableResource{}
	_ resource.Resource                   = &viewResource{}
	_ resource.ResourceWithConfigure      = &viewResource{}
	_ resource.ResourceWithImportState    = &viewResource{}
	_ resource.ResourceWithValidateConfig = &viewResource{}
	_ resource.Resource                   = &secretResource{}
	_ resource.ResourceWithConfigure      = &secretResource{}
	_ resource.ResourceWithImportState    = &secretResource{}
	_ resource.ResourceWithModifyPlan     = &secretResource{}
	_ resource.ResourceWithValidateConfig = &secretResource{}
	_ resource.Resource                   = &shareResource{}
	_ resource.ResourceWithConfigure      = &shareResource{}
	_ resource.ResourceWithImportState    = &shareResource{}
	_ resource.ResourceWithValidateConfig = &shareResource{}
	_ resource.Resource                   = &shareGrantResource{}
	_ resource.ResourceWithConfigure      = &shareGrantResource{}
	_ resource.ResourceWithImportState    = &shareGrantResource{}
	_ resource.Resource                   = &snapshotResource{}
	_ resource.ResourceWithConfigure      = &snapshotResource{}
	_ resource.ResourceWithImportState    = &snapshotResource{}
)

// stateGetter and stateSetter match tfsdk.Plan and tfsdk.State so helpers
// shared by Create and Update can read a plan and write a state.
type stateGetter interface {
	Get(context.Context, any) diag.Diagnostics
}

type stateSetter interface {
	Set(context.Context, any) diag.Diagnostics
}

// identifierRequiresReplace replaces the resource when a SQL identifier
// changes, except for a change in letter case. DuckDB and MotherDuck resolve
// identifiers case-insensitively and never allow two names that differ only in
// case, so a case-only edit names the same object. Terraform then updates
// state in place to the new spelling instead of dropping the object.
func identifierRequiresReplace() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !strings.EqualFold(req.StateValue.ValueString(), req.PlanValue.ValueString())
		},
		identifierRequiresReplaceDescription,
		identifierRequiresReplaceDescription,
	)}
}

const identifierRequiresReplaceDescription = "Changing the name replaces the resource unless only its letter case changes."

// identifierIDPlanModifier keeps the prior ID like UseStateForUnknown, except
// that it plans the ID from the configured identifiers. When an identifier
// changes only in letter case, the resource is updated in place and its ID
// takes the new spelling, so the planned ID must match what Update writes.
type identifierIDPlanModifier struct {
	parts []path.Path
}

// identifierID returns the ID plan modifier for an ID that joins the given
// identifier attributes with dots.
func identifierID(parts ...string) []planmodifier.String {
	paths := make([]path.Path, 0, len(parts))
	for _, part := range parts {
		paths = append(paths, path.Root(part))
	}
	return []planmodifier.String{identifierIDPlanModifier{parts: paths}}
}

func (identifierIDPlanModifier) Description(context.Context) string {
	return "Keeps the prior ID, using the configured spelling of its identifiers."
}

func (m identifierIDPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m identifierIDPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.StateValue.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	values := make([]string, 0, len(m.parts))
	for _, part := range m.parts {
		var value types.String
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, part, &value)...)
		if resp.Diagnostics.HasError() || value.IsNull() || value.IsUnknown() {
			return
		}
		values = append(values, value.ValueString())
	}
	resp.PlanValue = types.StringValue(strings.Join(values, "."))
}

func relationExists(ctx context.Context, r interface {
	sql(context.Context, *diag.Diagnostics) providerctx.SQLClient
}, database, schemaName, name, tableType string, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	var exists bool
	err := retry.SQL(ctx, func() error {
		if err := client.AttachDatabase(ctx, database); err != nil {
			return err
		}
		var existsErr error
		// DuckDB resolves identifiers case-insensitively, but the catalog
		// reports the spelling the object was created with.
		exists, existsErr = client.Exists(ctx, `SELECT count(*) FROM information_schema.tables WHERE lower(table_catalog) = lower(?) AND lower(table_schema) = lower(?) AND lower(table_name) = lower(?) AND table_type = ?`, database, schemaName, name, tableType)
		return existsErr
	})
	if err != nil && isNotFoundFor(err, database, schemaName, name) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck relation", err.Error())
		return false
	}
	return exists
}

func dropRelation(ctx context.Context, r interface {
	sql(context.Context, *diag.Diagnostics) providerctx.SQLClient
}, keyword string, getter interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	var database, schemaName, name types.String
	diags.Append(getter.GetAttribute(ctx, path.Root("database"), &database)...)
	diags.Append(getter.GetAttribute(ctx, path.Root("schema"), &schemaName)...)
	diags.Append(getter.GetAttribute(ctx, path.Root("name"), &name)...)
	if diags.HasError() {
		return
	}
	if err := client.AttachDatabase(ctx, database.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		diags.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	if err := client.Exec(ctx, "DROP "+keyword+" IF EXISTS "+sqlbuild.QuoteQualifiedIdentifier(database.ValueString(), schemaName.ValueString(), name.ValueString())); err != nil && !isDatabaseGone(err, database.ValueString()) {
		diags.AddError("Unable to drop MotherDuck relation", err.Error())
	}
}

func importThreePartID(ctx context.Context, id string, resp *resource.ImportStateResponse) {
	parts, ok := splitSQLImportID(id, ".", 3, "`<database>.<schema>.<name>`", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("database"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("schema"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
}

func importSingleSQLIdentifier(ctx context.Context, id string, namePath path.Path, resp *resource.ImportStateResponse) {
	if !validateSQLImportIDPart(id, "`<name>`", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, namePath, id)...)
}

func splitSQLImportID(id, separator string, wantParts int, usage string, diags *diag.Diagnostics) ([]string, bool) {
	parts, ok := splitImportID(id, separator, wantParts, usage, diags)
	if !ok {
		return nil, false
	}
	for _, part := range parts {
		if !validateSQLImportIDPart(part, usage, diags) {
			return nil, false
		}
	}
	return parts, true
}

func splitImportID(id, separator string, wantParts int, usage string, diags *diag.Diagnostics) ([]string, bool) {
	parts := strings.Split(id, separator)
	if len(parts) != wantParts {
		diags.AddError("Invalid import ID", "Use "+usage+".")
		return nil, false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			diags.AddError("Invalid import ID", "Use "+usage+" with non-empty segments.")
			return nil, false
		}
	}
	return parts, true
}

func validateSQLImportIDPart(part, usage string, diags *diag.Diagnostics) bool {
	trimmed := strings.TrimSpace(part)
	if trimmed == "" {
		diags.AddError("Invalid import ID", "Use "+usage+" with non-empty SQL resource name segments.")
		return false
	}
	if part != trimmed {
		diags.AddError("Invalid import ID", "Use "+usage+" with SQL resource name segments that do not have leading or trailing whitespace.")
		return false
	}
	if strings.Contains(part, ".") {
		diags.AddError("Invalid import ID", "SQL resource name segments must not contain dots because Terraform import IDs use dots as separators.")
		return false
	}
	return true
}

func validateShareGrantPrincipalImportID(part, usage string, diags *diag.Diagnostics) bool {
	detail, ok := validateShareGrantPrincipalValue(part)
	if !ok {
		diags.AddError("Invalid import ID", "Use "+usage+". "+detail)
		return false
	}
	return true
}

func qualifiedObjectID(database, schemaName, name string) string {
	return database + "." + schemaName + "." + name
}

func nullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(value.String)
}

func knownString(value types.String) types.String {
	if value.IsUnknown() {
		return types.StringNull()
	}
	return value
}

func lowerNullString(value stdsql.NullString) types.String {
	if !value.Valid {
		return types.StringNull()
	}
	return types.StringValue(strings.ToLower(value.String))
}

func resetDefaultDatabaseIfCurrent(ctx context.Context, client providerctx.SQLClient, database string) {
	current, err := client.ScalarString(ctx, "SELECT current_database()")
	if err != nil || !strings.EqualFold(current, database) {
		return
	}
	_ = client.Exec(ctx, "USE memory")
}
