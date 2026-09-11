package datasources

import (
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestCatalogConfigurationRoundTrip(t *testing.T) {
	for _, spec := range rowSpecs() {
		for _, mode := range []string{"known", "null", "unknown"} {
			t.Run(spec.name+"/"+mode, func(t *testing.T) {
				ctx := t.Context()
				d := &rowsDataSource{spec: spec}
				var schemaResponse datasource.SchemaResponse
				d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
				schema := schemaResponse.Schema
				values := map[string]attr.Value{}
				for name, typ := range schema.Type().(types.ObjectType).AttrTypes {
					switch {
					case typ.Equal(types.StringType):
						values[name] = types.StringValue("value for " + name)
					case typ.Equal(types.Int64Type):
						values[name] = types.Int64Value(int64(len(name)))
					case typ.Equal(types.BoolType):
						values[name] = types.BoolValue(true)
					default:
						values[name] = types.ListNull(typ.(types.ListType).ElemType)
					}
					if mode != "known" {
						raw := tftypes.NewValue(typ.TerraformType(ctx), nil)
						if mode == "unknown" {
							raw = tftypes.NewValue(typ.TerraformType(ctx), tftypes.UnknownValue)
						}
						value, err := typ.ValueFromTerraform(ctx, raw)
						if err != nil {
							t.Fatal(err)
						}
						values[name] = value
					}
				}
				obj, diags := types.ObjectValue(schema.Type().(types.ObjectType).AttrTypes, values)
				if diags.HasError() {
					t.Fatal(diags)
				}
				raw, err := obj.ToTerraformValue(ctx)
				if err != nil {
					t.Fatal(err)
				}
				req := datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: raw}}
				resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema, Raw: raw}}
				model := d.readConfig(ctx, req, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				// Check destinations against model tags independently of the shared mapping.
				modelValue := reflect.ValueOf(model)
				modelType := modelValue.Type()
				for i := 0; i < modelValue.NumField(); i++ {
					name := modelType.Field(i).Tag.Get("tfsdk")
					if name == "rows_json" {
						continue
					}
					want, configured := values[name]
					if !configured {
						continue
					}
					got := modelValue.Field(i).Interface().(attr.Value)
					if !got.Equal(want) {
						t.Fatalf("%s decoded into the wrong field: got %v, want %v", name, got, want)
					}
				}
				typedRows, rowDiags := spec.typedRowsValue("[]")
				if rowDiags.HasError() {
					t.Fatal(rowDiags)
				}
				d.setState(ctx, &resp.State, model, types.StringValue("[]"), typedRows, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				for _, name := range spec.attrs {
					var actual attr.Value
					if diags := resp.State.GetAttribute(ctx, path.Root(name), &actual); diags.HasError() {
						t.Fatal(diags)
					}
					if !actual.Equal(values[name]) {
						t.Fatalf("%s changed: got %v, want %v", name, actual, values[name])
					}
				}
			})
		}
	}
}
