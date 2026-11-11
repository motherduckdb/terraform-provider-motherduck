package resources

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// writeOnlyConfig answers configuration lookups for the write-only
// arguments, which the plan never carries.
type writeOnlyConfig map[string]types.Map

func (c writeOnlyConfig) GetAttribute(_ context.Context, attribute path.Path, target any) diag.Diagnostics {
	value, ok := c[attribute.String()]
	if !ok {
		value = types.MapNull(types.StringType)
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

// captureSetter records every model the resource saves.
type captureSetter struct{ saved []secretModel }

func (s *captureSetter) Set(_ context.Context, value any) diag.Diagnostics {
	s.saved = append(s.saved, *value.(*secretModel))
	return nil
}

func stringMap(values map[string]string) types.Map {
	elements := map[string]attr.Value{}
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

// A write-only value must reach MotherDuck and must not be saved, because the
// purpose of the argument is to keep the value out of Terraform state.
func TestSecretWriteOnlyParamsAreSentAndNotSaved(t *testing.T) {
	null := types.MapNull(types.StringType)
	for name, tc := range map[string]struct {
		plan   secretModel
		config writeOnlyConfig
		want   string
	}{
		// #nosec G101 -- fixture values and expected SQL literals are not credentials.
		"params_wo": {
			plan:   secretModel{Name: types.StringValue("s"), Type: types.StringValue("s3"), Params: null, FlightParams: null, ParamsWO: null, FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)},
			config: writeOnlyConfig{"params_wo": stringMap(map[string]string{"key_id": "write-only-key", "secret": "write-only-secret"})},
			want:   `CREATE SECRET "s" IN MOTHERDUCK (TYPE S3, KEY_ID 'write-only-key', SECRET 'write-only-secret')`,
		},
		"flight_params_wo": {
			plan:   secretModel{Name: types.StringValue("f"), Type: types.StringValue("flights"), Params: null, FlightParams: null, ParamsWO: null, FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)},
			config: writeOnlyConfig{"flight_params_wo": stringMap(map[string]string{"API_KEY": "write-only-key"})},
			want:   `CREATE SECRET "f" IN MOTHERDUCK (TYPE FLIGHTS, PARAMS MAP {'API_KEY': 'write-only-key'})`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := &recordingSQLClient{}
			r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			setter := &captureSetter{}
			var diags diag.Diagnostics
			r.writeSecret(t.Context(), modelGetter{model: tc.plan}, tc.config, setter, false, &diags)
			if len(client.execs) != 1 || client.execs[0] != tc.want {
				t.Fatalf("statements = %q, want %q (diagnostics %v)", client.execs, tc.want, diags)
			}
			if len(setter.saved) == 0 {
				t.Fatal("the secret was not saved to state")
			}
			for _, saved := range setter.saved {
				if !saved.Params.IsNull() || !saved.FlightParams.IsNull() || !saved.ParamsWO.IsNull() || !saved.FlightParamsWO.IsNull() {
					t.Fatalf("saved model carries secret values: %+v", saved)
				}
			}
		})
	}
}

func TestSecretWriteOnlyValidation(t *testing.T) {
	null := types.MapNull(types.StringType)
	values := stringMap(map[string]string{"key_id": "k"})
	for name, tc := range map[string]struct {
		model secretModel
		want  string // empty means valid
	}{
		"write-only with version":   {secretModel{Type: types.StringValue("s3"), Params: null, FlightParams: null, ParamsWO: values, FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)}, ""},
		"unknown version":           {secretModel{Type: types.StringValue("s3"), Params: null, FlightParams: null, ParamsWO: values, FlightParamsWO: null, ParamsWOVersion: types.Int64Unknown()}, ""},
		"undecided forms":           {secretModel{Type: types.StringValue("s3"), Params: types.MapUnknown(types.StringType), FlightParams: null, ParamsWO: types.MapUnknown(types.StringType), FlightParamsWO: null, ParamsWOVersion: types.Int64Unknown()}, ""},
		"stored form only":          {secretModel{Type: types.StringValue("s3"), Params: values, FlightParams: null, ParamsWO: null, FlightParamsWO: null, ParamsWOVersion: types.Int64Null()}, ""},
		"both forms":                {secretModel{Type: types.StringValue("s3"), Params: values, FlightParams: null, ParamsWO: values, FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)}, "Set either `params` or `params_wo`"},
		"both flight forms":         {secretModel{Type: types.StringValue("flights"), Params: null, FlightParams: values, ParamsWO: null, FlightParamsWO: values, ParamsWOVersion: types.Int64Value(1)}, "Set either `flight_params` or `flight_params_wo`"},
		"missing version":           {secretModel{Type: types.StringValue("s3"), Params: null, FlightParams: null, ParamsWO: values, FlightParamsWO: null, ParamsWOVersion: types.Int64Null()}, "require `params_wo_version`"},
		"version without values":    {secretModel{Type: types.StringValue("s3"), Params: values, FlightParams: null, ParamsWO: null, FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)}, "has an effect only with"},
		"write-only key is checked": {secretModel{Type: types.StringValue("s3"), Params: null, FlightParams: null, ParamsWO: stringMap(map[string]string{"bad key": "v"}), FlightParamsWO: null, ParamsWOVersion: types.Int64Value(1)}, "bare SQL option words"},
		"flights takes write-only":  {secretModel{Type: types.StringValue("flights"), Params: null, FlightParams: null, ParamsWO: null, FlightParamsWO: values, ParamsWOVersion: types.Int64Value(1)}, ""},
		"flights rejects params_wo": {secretModel{Type: types.StringValue("flights"), Params: null, FlightParams: null, ParamsWO: values, FlightParamsWO: values, ParamsWOVersion: types.Int64Value(1)}, "takes its values from `flight_params`"},
	} {
		var diags diag.Diagnostics
		validateSecretConfig(tc.model, &diags)
		switch {
		case tc.want == "" && diags.HasError():
			t.Errorf("%s diagnostics = %v, want none", name, diags)
		case tc.want != "" && (!diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), tc.want)):
			t.Errorf("%s diagnostics = %v, want %q", name, diags, tc.want)
		}
	}
}
