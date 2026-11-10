package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type tableResource struct{ baseResource }

type tableModel struct {
	ID          types.String `tfsdk:"id"`
	Database    types.String `tfsdk:"database"`
	Schema      types.String `tfsdk:"schema"`
	Name        types.String `tfsdk:"name"`
	Columns     types.Map    `tfsdk:"columns"`
	ColumnOrder types.List   `tfsdk:"column_order"`
	PrimaryKey  types.List   `tfsdk:"primary_key"`
	NotNull     types.Set    `tfsdk:"not_null"`
}

func NewTableResource() resource.Resource { return &tableResource{} }

func (r *tableResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (r *tableResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck table definition. Adding or dropping a column updates the table in place. Changing a column type, the primary key, or the order of existing columns replaces the table.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       identifierID("database", "schema", "name"),
				MarkdownDescription: "Table resource ID in `<database>.<schema>.<table>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the table. Changing only the letter case updates state in place.",
				PlanModifiers:       databaseIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema that contains the table. Changing only the case of ASCII letters updates state in place. Any other change replaces the table.",
				PlanModifiers:       catalogIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Table name. Changing only the case of ASCII letters updates state in place. Any other change, including the case of a non-ASCII letter such as `Ä`, names a different table and replaces it.",
				PlanModifiers:       catalogIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"columns": schema.MapAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Map of column name to DuckDB SQL type. Adding a column runs `ALTER TABLE ... ADD COLUMN`, and removing one runs `ALTER TABLE ... DROP COLUMN`, which deletes the data in that column only. Renaming a key drops the old column and adds a new one. Changing the type of an existing column replaces the table. Type aliases such as `INT` and `INTEGER` are compared semantically, so a change that only respells a type updates state in place.",
			},
			"column_order": schema.ListAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Order of the columns in the table. When set, it must list every key of `columns` exactly once. When omitted, a new table gets its columns in alphabetical order and Terraform records the order MotherDuck reports. A column added later is appended after the existing columns. Any other change to the order of existing columns replaces the table.",
			},
			"primary_key": schema.ListAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Columns of the table's primary key, in key order. Each entry must be a key of `columns`. Changing a configured value replaces the table. When omitted, Terraform records the primary key MotherDuck reports and does not change it. DuckLake databases do not support primary keys.",
			},
			"not_null": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Columns that reject `NULL`. Each entry must be a key of `columns`. Changes run `ALTER TABLE ... ALTER COLUMN ... SET NOT NULL` or `DROP NOT NULL` in place. MotherDuck rejects `SET NOT NULL` when the column already holds a `NULL`. Primary key columns always reject `NULL` and do not need to be listed. When omitted, Terraform records the constraints MotherDuck reports and does not change them.",
			},
		},
	}
}

func (r *tableResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config tableModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateTableColumns(ctx, config.Columns, &resp.Diagnostics)
	validateTableShape(config, &resp.Diagnostics)
}

// validateTableShape checks column_order, primary_key, and not_null against
// the column names. It runs at plan time on known values and again before
// every write, because the names are spliced into DDL.
func validateTableShape(model tableModel, diags *diag.Diagnostics) {
	if model.Columns.IsNull() || model.Columns.IsUnknown() {
		return
	}
	columns := model.Columns.Elements()
	if order, ok := knownStrings(model.ColumnOrder.Elements(), model.ColumnOrder.IsNull() || model.ColumnOrder.IsUnknown()); ok {
		seen := map[string]bool{}
		for _, name := range order {
			if _, exists := columns[name]; !exists {
				diags.AddAttributeError(path.Root("column_order"), "Invalid MotherDuck table column order", fmt.Sprintf("`column_order` names %q, which is not a key of `columns`. Names must match the keys exactly, including letter case.", name))
			} else if seen[name] {
				diags.AddAttributeError(path.Root("column_order"), "Invalid MotherDuck table column order", fmt.Sprintf("`column_order` lists %q more than once.", name))
			}
			seen[name] = true
		}
		if !diags.HasError() && len(seen) != len(columns) {
			missing := make([]string, 0)
			for name := range columns {
				if !seen[name] {
					missing = append(missing, name)
				}
			}
			sort.Strings(missing)
			diags.AddAttributeError(path.Root("column_order"), "Invalid MotherDuck table column order", "`column_order` must list every key of `columns`. Missing: "+strings.Join(missing, ", ")+".")
		}
	}
	if key, ok := knownStrings(model.PrimaryKey.Elements(), model.PrimaryKey.IsNull() || model.PrimaryKey.IsUnknown()); ok {
		seen := map[string]bool{}
		for _, name := range key {
			if _, exists := columns[name]; !exists {
				diags.AddAttributeError(path.Root("primary_key"), "Invalid MotherDuck table primary key", fmt.Sprintf("`primary_key` names %q, which is not a key of `columns`. Names must match the keys exactly, including letter case.", name))
			} else if seen[name] {
				diags.AddAttributeError(path.Root("primary_key"), "Invalid MotherDuck table primary key", fmt.Sprintf("`primary_key` lists %q more than once.", name))
			}
			seen[name] = true
		}
	}
	if notNull, ok := knownStrings(model.NotNull.Elements(), model.NotNull.IsNull() || model.NotNull.IsUnknown()); ok {
		for _, name := range notNull {
			if _, exists := columns[name]; !exists {
				diags.AddAttributeError(path.Root("not_null"), "Invalid MotherDuck table constraint", fmt.Sprintf("`not_null` names %q, which is not a key of `columns`. Names must match the keys exactly, including letter case.", name))
			}
		}
	}
}

// knownStrings returns the string elements of a collection. ok is false when
// the collection is unset or any element is null or unknown.
func knownStrings(elements []attr.Value, unset bool) ([]string, bool) {
	if unset {
		return nil, false
	}
	values := make([]string, 0, len(elements))
	for _, element := range elements {
		value, ok := element.(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			return nil, false
		}
		values = append(values, value.ValueString())
	}
	return values, true
}

func stringListValue(values []string) types.List {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
}

func stringSetValue(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}

func (r *tableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan tableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	columns := map[string]string{}
	resp.Diagnostics.Append(plan.Columns.ElementsAs(ctx, &columns, false)...)
	validateTableShape(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString())
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", attachErrorDetail(err, plan.Database.ValueString()))
		return
	}
	columns = canonicalTableColumns(ctx, client, columns, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	order, ok := knownStrings(plan.ColumnOrder.Elements(), plan.ColumnOrder.IsNull() || plan.ColumnOrder.IsUnknown())
	if !ok {
		order = sortedKeys(columns)
	}
	primaryKey, _ := knownStrings(plan.PrimaryKey.Elements(), plan.PrimaryKey.IsNull() || plan.PrimaryKey.IsUnknown())
	notNull, _ := knownStrings(plan.NotNull.Elements(), plan.NotNull.IsNull() || plan.NotNull.IsUnknown())
	query := "CREATE TABLE " + sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()) + " (" + tableDDL(columns, order, primaryKey, notNull) + ")"
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck table", schemaCreateErrorDetail(err, createErrorDetail(err, err.Error(), "table", id), plan.Schema.ValueString()))
		return
	}
	plan.ID = types.StringValue(id)
	// The statement fixes the shape, so state records it without a read-back.
	plan.ColumnOrder = stringListValue(order)
	plan.PrimaryKey = stringListValue(primaryKey)
	sort.Strings(notNull)
	plan.NotNull = stringSetValue(notNull)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state tableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	exists := relationExists(ctx, r, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), "BASE TABLE", &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	shape := readTableShape(ctx, client, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Columns.IsNull() || state.Columns.IsUnknown() {
		state.Columns = tableColumnsValue(ctx, shape.types, &resp.Diagnostics)
	} else if !tableColumnsSemanticallyEqual(ctx, client, state.Columns, shape.types, &resp.Diagnostics) {
		state.Columns = tableColumnsValue(ctx, shape.types, &resp.Diagnostics)
	}
	applyTableShape(&state, shape, state.NotNull)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// applyTableShape records the live column order and constraints. A primary
// key column always rejects NULL, so it is listed in not_null only when the
// prior value already listed it.
func applyTableShape(model *tableModel, shape tableShape, priorNotNull types.Set) {
	model.ColumnOrder = stringListValue(shape.order)
	model.PrimaryKey = stringListValue(shape.primaryKey)
	prior, _ := knownStrings(priorNotNull.Elements(), priorNotNull.IsNull() || priorNotNull.IsUnknown())
	notNull := make([]string, 0, len(shape.notNull))
	for _, name := range shape.notNull {
		if !slices.Contains(shape.primaryKey, name) || slices.Contains(prior, name) {
			notNull = append(notNull, name)
		}
	}
	sort.Strings(notNull)
	model.NotNull = stringSetValue(notNull)
}

// tableColumnChange describes how planned columns differ from current ones.
type tableColumnChange struct {
	added       []string // sorted
	dropped     []string // sorted
	typeChanged bool
}

// classifyTableColumns compares two column maps. A column present in both
// must keep an equivalent type. A new name that differs from a dropped name
// only in the case of ASCII letters names the same column in DuckDB, so it
// cannot be added before the old one is dropped and counts as a type change,
// which replaces the table.
func classifyTableColumns(ctx context.Context, client func() scalarStringer, planned, current map[string]string, diags *diag.Diagnostics) tableColumnChange {
	var change tableColumnChange
	for _, name := range sortedKeys(planned) {
		currentType, ok := current[name]
		if !ok {
			change.added = append(change.added, name)
			continue
		}
		if sameColumnTypeSpelling(planned[name], currentType) {
			continue
		}
		sql := client()
		if sql == nil {
			change.typeChanged = true
			return change
		}
		plannedCanonical, err := canonicalColumnType(ctx, sql, planned[name])
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Unable to normalize MotherDuck table column type", err.Error())
			return change
		}
		currentCanonical, err := canonicalColumnType(ctx, sql, currentType)
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Unable to normalize MotherDuck table column type", err.Error())
			return change
		}
		if !sameColumnTypeSpelling(plannedCanonical, currentCanonical) {
			change.typeChanged = true
		}
	}
	for _, name := range sortedKeys(current) {
		if _, ok := planned[name]; !ok {
			change.dropped = append(change.dropped, name)
		}
	}
	for _, added := range change.added {
		for _, dropped := range change.dropped {
			if catalogNamesEqual(added, dropped) {
				change.typeChanged = true
			}
		}
	}
	return change
}

// tableOrderAfterChange returns the column order an in-place change produces:
// the current order without the dropped columns, followed by the added
// columns. Added columns follow configured when it is set and are sorted
// otherwise.
func tableOrderAfterChange(current []string, change tableColumnChange, configured []string, hasConfigured bool) []string {
	order := make([]string, 0, len(current)+len(change.added))
	for _, name := range current {
		if !slices.Contains(change.dropped, name) {
			order = append(order, name)
		}
	}
	added := change.added
	if hasConfigured {
		added = make([]string, 0, len(change.added))
		for _, name := range configured {
			if slices.Contains(change.added, name) {
				added = append(added, name)
			}
		}
	}
	return append(order, added...)
}

// ModifyPlan decides whether a change updates the table in place or replaces
// it, and plans the computed shape attributes. The decision cannot use static
// plan modifiers because type equivalence needs the SQL client. For example,
// importing a table stores canonical `INTEGER` types, and a configuration that
// says `INT` must not drop the table.
func (r *tableResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.warnMissingSQLToken(req, resp)
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.Plan.Raw.Equal(req.State.Raw) {
		return
	}
	var plan, state, config tableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Columns.IsUnknown() || mapHasUnknownElement(plan.Columns) || state.Columns.IsNull() || state.Columns.IsUnknown() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("columns"))
		return
	}
	planned, current := map[string]string{}, map[string]string{}
	resp.Diagnostics.Append(plan.Columns.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(state.Columns.ElementsAs(ctx, &current, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var compareDiags diag.Diagnostics
	change := classifyTableColumns(ctx, func() scalarStringer {
		client := r.sql(ctx, &compareDiags)
		if client == nil {
			return nil
		}
		return client
	}, planned, current, &compareDiags)
	if compareDiags.HasError() {
		// Replacing drops the table and its data, so a failed comparison, such
		// as a transient connection error, must fail the plan instead of
		// planning a replacement that the types may not need.
		first := compareDiags.Errors()[0]
		errorPath := path.Root("columns")
		if withPath, ok := first.(diag.DiagnosticWithPath); ok {
			errorPath = withPath.Path()
		}
		resp.Diagnostics.AddAttributeError(errorPath, "Unable to compare MotherDuck table column types",
			"The provider could not normalize the planned column types, so it cannot tell whether the change needs to replace the table. "+first.Detail()+"\n\n"+tableColumnTypeResolutionHint)
		return
	}
	if change.typeChanged {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("columns"))
		return
	}

	statePrimaryKey, statePrimaryKeyKnown := knownStrings(state.PrimaryKey.Elements(), state.PrimaryKey.IsNull() || state.PrimaryKey.IsUnknown())
	for _, name := range change.dropped {
		if slices.Contains(statePrimaryKey, name) {
			// MotherDuck cannot drop a primary key column.
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("columns"))
			return
		}
	}
	if configured, ok := knownStrings(config.PrimaryKey.Elements(), config.PrimaryKey.IsNull() || config.PrimaryKey.IsUnknown()); ok {
		if statePrimaryKeyKnown && !slices.Equal(configured, statePrimaryKey) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("primary_key"))
			return
		}
	} else if config.PrimaryKey.IsNull() && statePrimaryKeyKnown {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("primary_key"), state.PrimaryKey)...)
	}

	stateOrder, stateOrderKnown := knownStrings(state.ColumnOrder.Elements(), state.ColumnOrder.IsNull() || state.ColumnOrder.IsUnknown())
	configuredOrder, hasConfiguredOrder := knownStrings(config.ColumnOrder.Elements(), config.ColumnOrder.IsNull() || config.ColumnOrder.IsUnknown())
	if stateOrderKnown {
		expected := tableOrderAfterChange(stateOrder, change, configuredOrder, hasConfiguredOrder)
		switch {
		case hasConfiguredOrder && !slices.Equal(configuredOrder, expected):
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("column_order"))
			return
		case config.ColumnOrder.IsNull():
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("column_order"), stringListValue(expected))...)
		}
	}

	if config.NotNull.IsNull() {
		if stateNotNull, ok := knownStrings(state.NotNull.Elements(), state.NotNull.IsNull() || state.NotNull.IsUnknown()); ok {
			kept := make([]string, 0, len(stateNotNull))
			for _, name := range stateNotNull {
				if !slices.Contains(change.dropped, name) {
					kept = append(kept, name)
				}
			}
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("not_null"), stringSetValue(kept))...)
		}
	}
}

// tableColumnTypeResolutionHint explains the common cause of a type that
// cannot be normalized. The provider resolves types with
// `typeof(CAST(NULL AS <type>))` in its connection's current database, while
// CREATE TABLE resolves an unqualified user-defined type in the table's own
// database.
const tableColumnTypeResolutionHint = "The provider resolves column types in the current database of its connection, not in the table's database. A type created with CREATE TYPE in the table's database does not resolve there. Qualify such a type with its database and schema, for example `analytics.main.mood`, or write out its definition, for example `ENUM('ok', 'sad')`."

// Update applies the changes ModifyPlan allowed in place: added and dropped
// columns, NOT NULL changes, and respelled identifiers or types. It compares
// the plan with the live table instead of state, so an update that failed
// part way can be applied again.
func (r *tableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan, state tableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateTableShape(plan, &resp.Diagnostics)
	planned := map[string]string{}
	resp.Diagnostics.Append(plan.Columns.ElementsAs(ctx, &planned, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	database, schemaName, name := state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString()
	if err := client.AttachDatabase(ctx, database); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", attachErrorDetail(err, database))
		return
	}
	live := readTableShape(ctx, client, database, schemaName, name, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	change := classifyTableColumns(ctx, func() scalarStringer { return client }, planned, live.types, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if change.typeChanged {
		resp.Diagnostics.AddError("Table column type changes are not supported in place", "The live table has a column whose type differs from the configuration. Run `terraform plan` again so that the plan replaces the table, or change the configured type to match.")
		return
	}
	if plannedKey, ok := knownStrings(plan.PrimaryKey.Elements(), plan.PrimaryKey.IsNull() || plan.PrimaryKey.IsUnknown()); ok && !slices.Equal(plannedKey, live.primaryKey) {
		resp.Diagnostics.AddAttributeError(path.Root("primary_key"), "Table primary key changes are not supported in place", fmt.Sprintf("The live table has the primary key [%s], and the plan expects [%s]. Run `terraform plan` again so that the plan replaces the table, or change `primary_key` to match.", strings.Join(live.primaryKey, ", "), strings.Join(plannedKey, ", ")))
		return
	}

	table := sqlbuild.QuoteQualifiedIdentifier(database, schemaName, name)
	configuredOrder, hasConfiguredOrder := knownStrings(plan.ColumnOrder.Elements(), plan.ColumnOrder.IsNull() || plan.ColumnOrder.IsUnknown())
	addOrder := tableOrderAfterChange(nil, change, configuredOrder, hasConfiguredOrder)
	statements := make([]string, 0, len(change.added)+len(change.dropped))
	if len(addOrder) > 0 {
		addTypes := map[string]string{}
		for _, column := range addOrder {
			addTypes[column] = planned[column]
		}
		addTypes = canonicalTableColumns(ctx, client, addTypes, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, column := range addOrder {
			statements = append(statements, "ALTER TABLE "+table+" ADD COLUMN "+sqlbuild.QuoteIdentifier(column)+" "+addTypes[column])
		}
	}
	desiredNotNull, manageNotNull := knownStrings(plan.NotNull.Elements(), plan.NotNull.IsNull() || plan.NotNull.IsUnknown())
	if manageNotNull {
		sort.Strings(desiredNotNull)
		for _, column := range desiredNotNull {
			if !slices.Contains(live.notNull, column) {
				statements = append(statements, "ALTER TABLE "+table+" ALTER COLUMN "+sqlbuild.QuoteIdentifier(column)+" SET NOT NULL")
			}
		}
		for _, column := range live.notNull {
			if !slices.Contains(desiredNotNull, column) && !slices.Contains(live.primaryKey, column) && !slices.Contains(change.dropped, column) {
				statements = append(statements, "ALTER TABLE "+table+" ALTER COLUMN "+sqlbuild.QuoteIdentifier(column)+" DROP NOT NULL")
			}
		}
	}
	for _, column := range change.dropped {
		statements = append(statements, "ALTER TABLE "+table+" DROP COLUMN "+sqlbuild.QuoteIdentifier(column))
	}
	for _, statement := range statements {
		if err := client.Exec(ctx, statement); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck table", withoutSQLContext(err.Error())+"\n\nStatement: "+statement+"\n\nEarlier statements of this update were applied. Terraform recorded the live table, so the next plan shows the remaining changes.")
			break
		}
	}

	// Record what is live, including after a statement failed part way.
	var readDiags diag.Diagnostics
	after := readTableShape(ctx, client, database, schemaName, name, &readDiags)
	if readDiags.HasError() {
		resp.Diagnostics.Append(readDiags...)
		return
	}
	result := plan
	result.ID = types.StringValue(qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()))
	if resp.Diagnostics.HasError() || !tableColumnsSemanticallyEqual(ctx, client, plan.Columns, after.types, &readDiags) {
		result.Columns = tableColumnsValue(ctx, after.types, &resp.Diagnostics)
	}
	prior := plan.NotNull
	if !manageNotNull {
		prior = state.NotNull
	}
	applyTableShape(&result, after, prior)
	resp.Diagnostics.Append(resp.State.Set(ctx, &result)...)
}

// mapHasUnknownElement reports whether a known map holds an unknown value,
// such as a column type that references another resource.
func mapHasUnknownElement(value types.Map) bool {
	for _, element := range value.Elements() {
		if element.IsUnknown() {
			return true
		}
	}
	return false
}

// tableColumnMapsEquivalent reports whether two column maps have the same
// column names and canonical DuckDB types.
func tableColumnMapsEquivalent(ctx context.Context, client scalarStringer, left, right types.Map, diags *diag.Diagnostics) bool {
	leftColumns := map[string]string{}
	rightColumns := map[string]string{}
	diags.Append(left.ElementsAs(ctx, &leftColumns, false)...)
	diags.Append(right.ElementsAs(ctx, &rightColumns, false)...)
	if diags.HasError() || len(leftColumns) != len(rightColumns) {
		return false
	}
	names := make([]string, 0, len(leftColumns))
	for name := range leftColumns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rightType, ok := rightColumns[name]
		if !ok {
			return false
		}
		if sameColumnTypeSpelling(leftColumns[name], rightType) {
			continue
		}
		leftCanonical, err := canonicalColumnType(ctx, client, leftColumns[name])
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Unable to normalize MotherDuck table column type", err.Error())
			return false
		}
		rightCanonical, err := canonicalColumnType(ctx, client, rightType)
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Unable to normalize MotherDuck table column type", err.Error())
			return false
		}
		if !sameColumnTypeSpelling(leftCanonical, rightCanonical) {
			return false
		}
	}
	return true
}

// sameColumnTypeSpelling compares two type spellings. Type keywords are case
// insensitive, but quoted parts such as ENUM values and struct field names are
// case sensitive, so any spelling with a quote must match exactly.
func sameColumnTypeSpelling(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == right {
		return true
	}
	if strings.ContainsAny(left+right, `'"`) {
		return false
	}
	return strings.EqualFold(left, right)
}

func (r *tableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	dropRelation(ctx, r, "TABLE", req.State, &resp.Diagnostics)
}

func (r *tableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importThreePartID(ctx, req.ID, resp)
}

// tableShape is the live definition of a table.
type tableShape struct {
	types      map[string]string
	order      []string
	notNull    []string // in column order
	primaryKey []string // in key order
}

func readTableColumnTypes(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) map[string]string {
	return readTableShape(ctx, client, database, schemaName, name, diags).types
}

func readTableShape(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) tableShape {
	shape := tableShape{types: map[string]string{}, order: []string{}, notNull: []string{}, primaryKey: []string{}}
	var rows, constraints [][]string
	err := retry.SQL(ctx, func() error {
		var queryErr error
		rows, queryErr = queryCatalogRows(ctx, client, []string{"table_catalog", "table_schema", "table_name", "column_name", "data_type", "is_nullable"},
			`SELECT table_catalog, table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns WHERE lower(table_catalog) = lower(?) AND lower(table_schema) = lower(?) AND lower(table_name) = lower(?) ORDER BY ordinal_position`,
			database, schemaName, name)
		return queryErr
	})
	if err != nil {
		diags.AddError("Unable to read MotherDuck table columns", err.Error())
		return shape
	}
	// lower() also matches tables whose names differ only in the case of
	// non-ASCII letters, which DuckDB treats as different tables. Keep the
	// columns of the one table that DDL on these identifiers would resolve.
	match := matchCatalogRow(rows, database, schemaName, name)
	if match < 0 {
		return shape
	}
	identity := rows[match][:3]
	for _, row := range rows {
		if row[0] == identity[0] && row[1] == identity[1] && row[2] == identity[2] {
			shape.types[row[3]] = row[4]
			shape.order = append(shape.order, row[3])
			if strings.EqualFold(row[5], "NO") {
				shape.notNull = append(shape.notNull, row[3])
			}
		}
	}
	err = retry.SQL(ctx, func() error {
		var queryErr error
		constraints, queryErr = queryCatalogRows(ctx, client, []string{"database_name", "schema_name", "table_name", "key_columns"},
			`SELECT database_name, schema_name, table_name, to_json(constraint_column_names)::VARCHAR AS key_columns FROM duckdb_constraints() WHERE lower(database_name) = lower(?) AND lower(schema_name) = lower(?) AND lower(table_name) = lower(?) AND constraint_type = 'PRIMARY KEY'`,
			database, schemaName, name)
		return queryErr
	})
	if err != nil {
		diags.AddError("Unable to read MotherDuck table constraints", err.Error())
		return shape
	}
	for _, row := range constraints {
		if row[0] != identity[0] || row[1] != identity[1] || row[2] != identity[2] {
			continue
		}
		if err := json.Unmarshal([]byte(row[3]), &shape.primaryKey); err != nil {
			diags.AddError("Unable to read MotherDuck table constraints", "MotherDuck reported primary key columns that could not be decoded: "+err.Error())
		}
		if shape.primaryKey == nil {
			shape.primaryKey = []string{}
		}
	}
	return shape
}

func tableColumnsValue(ctx context.Context, columns map[string]string, diags *diag.Diagnostics) types.Map {
	value, valueDiags := types.MapValueFrom(ctx, types.StringType, columns)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return types.MapNull(types.StringType)
	}
	return value
}

type scalarStringer interface {
	ScalarString(context.Context, string, ...any) (string, error)
}

func tableColumnsSemanticallyEqual(ctx context.Context, client scalarStringer, configuredValue types.Map, liveColumns map[string]string, diags *diag.Diagnostics) bool {
	configuredColumns := map[string]string{}
	diags.Append(configuredValue.ElementsAs(ctx, &configuredColumns, false)...)
	if diags.HasError() {
		return false
	}
	if len(configuredColumns) != len(liveColumns) {
		return false
	}
	for name, configuredType := range configuredColumns {
		liveType, ok := liveColumns[name]
		if !ok {
			return false
		}
		equal, err := columnTypesSemanticallyEqual(ctx, client, configuredType, liveType)
		if err != nil {
			diags.AddError("Unable to normalize MotherDuck table column type", err.Error())
			return false
		}
		if !equal {
			return false
		}
	}
	return true
}

func columnTypesSemanticallyEqual(ctx context.Context, client scalarStringer, configuredType, liveType string) (bool, error) {
	canonical, err := canonicalColumnType(ctx, client, configuredType)
	if err != nil {
		return false, err
	}
	return sameColumnTypeSpelling(canonical, liveType), nil
}

func canonicalColumnType(ctx context.Context, client scalarStringer, columnType string) (string, error) {
	trimmed := strings.TrimSpace(columnType)
	if detail := columnTypeSyntaxError(trimmed); detail != "" {
		return "", fmt.Errorf("%s", detail)
	}
	return client.ScalarString(ctx, "SELECT typeof(CAST(NULL AS "+trimmed+"))")
}

func columnDDL(columns map[string]string) string {
	keys := make([]string, 0, len(columns))
	for key := range columns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, sqlbuild.QuoteIdentifier(key)+" "+columns[key])
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// tableDDL renders the body of CREATE TABLE: the columns in the given order,
// NOT NULL on the listed columns, and a primary key constraint when set.
func tableDDL(columns map[string]string, order, primaryKey, notNull []string) string {
	parts := make([]string, 0, len(order)+1)
	for _, name := range order {
		part := sqlbuild.QuoteIdentifier(name) + " " + columns[name]
		if slices.Contains(notNull, name) {
			part += " NOT NULL"
		}
		parts = append(parts, part)
	}
	if len(primaryKey) > 0 {
		quoted := make([]string, 0, len(primaryKey))
		for _, name := range primaryKey {
			quoted = append(quoted, sqlbuild.QuoteIdentifier(name))
		}
		parts = append(parts, "PRIMARY KEY ("+strings.Join(quoted, ", ")+")")
	}
	return strings.Join(parts, ", ")
}

func validateTableColumns(ctx context.Context, columnsValue types.Map, diags *diag.Diagnostics) {
	if columnsValue.IsNull() || columnsValue.IsUnknown() {
		return
	}
	columns := columnsValue.Elements()
	if len(columns) == 0 {
		diags.AddAttributeError(path.Root("columns"), "Invalid MotherDuck table columns", "A table must define at least one column.")
		return
	}
	for name, value := range columns {
		columnPath := path.Root("columns").AtMapKey(name)
		if strings.TrimSpace(name) == "" {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column", "Column names must not be empty.")
		}
		// A type that depends on another resource is checked once it is known.
		columnType, ok := value.(types.String)
		if !ok || columnType.IsUnknown() {
			continue
		}
		if columnType.IsNull() {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column type", "Column types must not be null.")
			continue
		}
		if detail := columnTypeSyntaxError(columnType.ValueString()); detail != "" {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column type", detail)
		}
	}
}

func columnTypeSyntaxError(columnType string) string {
	value := strings.TrimSpace(columnType)
	if value == "" {
		return "Column types must not be empty."
	}
	stack := make([]byte, 0, 4)
	var quote byte
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == 0 {
			return "Column types must not contain NULL bytes."
		}
		if quote != 0 {
			if ch == quote {
				if i+1 < len(value) && value[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ';' {
			return "Column types must be single DuckDB data type expressions and must not contain semicolons."
		}
		if i+1 < len(value) {
			pair := value[i : i+2]
			if pair == "--" || pair == "/*" || pair == "*/" {
				return "Column types must not contain SQL comments."
			}
		}
		switch ch {
		case '(', '[':
			stack = append(stack, ch)
		case ')', ']':
			if len(stack) == 0 || (ch == ')' && stack[len(stack)-1] != '(') || (ch == ']' && stack[len(stack)-1] != '[') {
				return "Column types must have balanced parentheses and brackets."
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return "Column types must not contain unterminated quoted values or identifiers."
	}
	if len(stack) != 0 {
		return "Column types must have balanced parentheses and brackets."
	}
	return ""
}

func canonicalTableColumns(ctx context.Context, client scalarStringer, columns map[string]string, diags *diag.Diagnostics) map[string]string {
	canonical := make(map[string]string, len(columns))
	keys := make([]string, 0, len(columns))
	for name := range columns {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		columnType, err := canonicalColumnType(ctx, client, columns[name])
		if err != nil {
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Invalid MotherDuck table column type", fmt.Sprintf("MotherDuck could not resolve the type %q of column %q: %s", columns[name], name, withoutSQLContext(err.Error())))
			continue
		}
		canonical[name] = columnType
	}
	return canonical
}
