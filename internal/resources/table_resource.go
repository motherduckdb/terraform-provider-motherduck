package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	ID       types.String `tfsdk:"id"`
	Database types.String `tfsdk:"database"`
	Schema   types.String `tfsdk:"schema"`
	Name     types.String `tfsdk:"name"`
	Columns  types.Map    `tfsdk:"columns"`
}

func NewTableResource() resource.Resource { return &tableResource{} }

func (r *tableResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_table"
}

func (r *tableResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a MotherDuck table definition. Column changes replace the table.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Table resource ID in `<database>.<schema>.<table>` form.",
			},
			"database": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Database that contains the table.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Schema that contains the table.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Table name.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"columns": schema.MapAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Map of column name to DuckDB SQL type. Type aliases are compared semantically during refresh to avoid replacement churn.",
				PlanModifiers:       mapRequiresReplace(),
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
	if resp.Diagnostics.HasError() {
		return
	}
	id := qualifiedObjectID(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString())
	if err := client.AttachDatabase(ctx, plan.Database.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to attach MotherDuck database", err.Error())
		return
	}
	columns = canonicalTableColumns(ctx, client, columns, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "CREATE TABLE " + sqlbuild.QuoteQualifiedIdentifier(plan.Database.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()) + " (" + columnDDL(columns) + ")"
	if err := client.Exec(ctx, query); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck table", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
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
	liveColumns := readTableColumnTypes(ctx, client, state.Database.ValueString(), state.Schema.ValueString(), state.Name.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Columns.IsNull() || state.Columns.IsUnknown() {
		state.Columns = tableColumnsValue(ctx, liveColumns, &resp.Diagnostics)
	} else if !tableColumnsSemanticallyEqual(ctx, client, state.Columns, liveColumns, &resp.Diagnostics) {
		state.Columns = tableColumnsValue(ctx, liveColumns, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *tableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Table updates are not supported", "Change columns by replacing the table resource.")
}

func (r *tableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	dropRelation(ctx, r, "TABLE", req.State, &resp.Diagnostics)
}

func (r *tableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importThreePartID(ctx, req.ID, resp)
}

func readTableColumnTypes(ctx context.Context, client providerctx.SQLClient, database, schemaName, name string, diags *diag.Diagnostics) map[string]string {
	var rowsJSON string
	err := retry.SQL(ctx, func() error {
		var queryErr error
		rowsJSON, queryErr = client.QueryRowsJSON(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_catalog = ? AND table_schema = ? AND table_name = ? ORDER BY ordinal_position`, database, schemaName, name)
		return queryErr
	})
	if err != nil {
		diags.AddError("Unable to read MotherDuck table columns", err.Error())
		return nil
	}
	var rows []struct {
		ColumnName string `json:"column_name"`
		DataType   string `json:"data_type"`
	}
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		diags.AddError("Unable to decode MotherDuck table columns", err.Error())
		return nil
	}
	columns := make(map[string]string, len(rows))
	for _, row := range rows {
		columns[row.ColumnName] = row.DataType
	}
	return columns
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
	return strings.EqualFold(canonical, strings.TrimSpace(liveType)), nil
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

func validateTableColumns(ctx context.Context, columnsValue types.Map, diags *diag.Diagnostics) {
	if columnsValue.IsNull() || columnsValue.IsUnknown() {
		return
	}
	columns := map[string]string{}
	diags.Append(columnsValue.ElementsAs(ctx, &columns, false)...)
	if diags.HasError() {
		return
	}
	if len(columns) == 0 {
		diags.AddAttributeError(path.Root("columns"), "Invalid MotherDuck table columns", "A table must define at least one column.")
		return
	}
	for name, columnType := range columns {
		columnPath := path.Root("columns").AtMapKey(name)
		if strings.TrimSpace(name) == "" {
			diags.AddAttributeError(columnPath, "Invalid MotherDuck table column", "Column names must not be empty.")
		}
		if detail := columnTypeSyntaxError(columnType); detail != "" {
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
			diags.AddAttributeError(path.Root("columns").AtMapKey(name), "Invalid MotherDuck table column type", err.Error())
			continue
		}
		canonical[name] = columnType
	}
	return canonical
}
