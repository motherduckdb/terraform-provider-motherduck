package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"fmt"
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

// identifierRequiresReplace replaces the resource when the name of a
// MotherDuck entity changes, except for a change in letter case. MotherDuck
// resolves database, share, and principal names with a Unicode
// case-insensitive collation and never allows two names that differ only in
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

// databaseIdentifierRequiresReplace is identifierRequiresReplace for database
// names. MotherDuck treats database names as equal across Unicode letter case,
// but the client can only attach a database by a spelling that differs in the
// case of ASCII letters. Attaching `md:äpfel` for the database `Äpfel` fails
// with "Database aliases are not yet supported". A change in the case of
// non-ASCII letters would leave every later refresh unable to attach, and
// replacing would drop the database or its objects, so the plan rejects it and
// names the existing spelling.
func databaseIdentifierRequiresReplace() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			current, planned := req.StateValue.ValueString(), req.PlanValue.ValueString()
			if !strings.EqualFold(current, planned) {
				resp.RequiresReplace = true
				return
			}
			if !catalogNamesEqual(current, planned) {
				resp.Diagnostics.AddAttributeError(
					req.Path,
					"Unsupported MotherDuck database name spelling",
					fmt.Sprintf("%q and %q name the same MotherDuck database, but MotherDuck cannot attach a database by a spelling that differs in the case of non-ASCII letters. Keep the existing spelling %q.", planned, current, current),
				)
			}
		},
		identifierRequiresReplaceDescription,
		identifierRequiresReplaceDescription,
	)}
}

// catalogIdentifierRequiresReplace is identifierRequiresReplace for schema,
// table, and view names. DuckDB stores these in the database catalog and folds
// only ASCII letters when it compares them, so `Äpfel` and `äpfel` are two
// different tables that can exist side by side. Only a change in the case of
// ASCII letters names the same object.
func catalogIdentifierRequiresReplace() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !catalogNamesEqual(req.StateValue.ValueString(), req.PlanValue.ValueString())
		},
		catalogIdentifierRequiresReplaceDescription,
		catalogIdentifierRequiresReplaceDescription,
	)}
}

const catalogIdentifierRequiresReplaceDescription = "Changing the name replaces the resource unless only the case of ASCII letters changes."

// catalogNamesEqual compares two catalog names the way DuckDB does. DuckDB
// folds only the ASCII letters A to Z, and compares every other character,
// including non-ASCII letters, exactly.
func catalogNamesEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := 0; i < len(left); i++ {
		if asciiLower(left[i]) != asciiLower(right[i]) {
			return false
		}
	}
	return true
}

func asciiLower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// matchCatalogRow returns the index of the row whose leading columns name want
// under DuckDB's catalog comparison, or -1 when no row does. Catalog lookups
// fetch candidates with lower(), which folds Unicode letters too, so callers
// must filter the rows again here. An exact match wins over a case-folded one.
func matchCatalogRow(rows [][]string, want ...string) int {
	found := -1
	for i, row := range rows {
		if len(row) < len(want) {
			continue
		}
		exact, equal := true, true
		for j, part := range want {
			if row[j] != part {
				exact = false
			}
			if !catalogNamesEqual(row[j], part) {
				equal = false
				break
			}
		}
		if equal && exact {
			return i
		}
		if equal && found < 0 {
			found = i
		}
	}
	return found
}

// queryCatalogRows runs a catalog query whose columns are all strings and
// returns its rows with the columns in the given order. A NULL value becomes
// an empty string.
func queryCatalogRows(ctx context.Context, client providerctx.SQLClient, columns []string, query string, args ...any) ([][]string, error) {
	rowsJSON, err := client.QueryRowsJSON(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var decoded []map[string]*string
	if err := json.Unmarshal([]byte(rowsJSON), &decoded); err != nil {
		return nil, fmt.Errorf("decode catalog rows: %w", err)
	}
	rows := make([][]string, 0, len(decoded))
	for _, values := range decoded {
		row := make([]string, len(columns))
		for i, column := range columns {
			if value := values[column]; value != nil {
				row[i] = *value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

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
		// DuckDB resolves identifiers by folding ASCII letters, but the
		// catalog reports the spelling the object was created with.
		rows, queryErr := queryCatalogRows(ctx, client, []string{"table_catalog", "table_schema", "table_name"},
			`SELECT table_catalog, table_schema, table_name FROM information_schema.tables WHERE lower(table_catalog) = lower(?) AND lower(table_schema) = lower(?) AND lower(table_name) = lower(?) AND table_type = ?`,
			database, schemaName, name, tableType)
		exists = queryErr == nil && matchCatalogRow(rows, database, schemaName, name) >= 0
		return queryErr
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
