package datasources

import (
	"bytes"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

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

	var rawRows []map[string]json.RawMessage
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

func typedRowStringValue(value json.RawMessage) types.String {
	if len(value) == 0 || string(value) == "null" {
		return types.StringNull()
	}
	// Raw row values have already passed JSON validation. Only strings need
	// unescaping. Keep null handling and the fallback representation unchanged.
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 && trimmed[0] != '"' && trimmed[0] != 'n' {
		return types.StringValue(string(value))
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return types.StringValue(text)
	}
	return types.StringValue(string(value))
}
