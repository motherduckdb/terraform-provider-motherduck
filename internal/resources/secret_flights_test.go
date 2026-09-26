package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

type flightsSecretClient struct {
	providerctx.SQLClient
	execs []string
}

func (c *flightsSecretClient) Available() bool { return true }
func (c *flightsSecretClient) Exec(_ context.Context, query string, _ ...any) error {
	c.execs = append(c.execs, query)
	return nil
}
func (c *flightsSecretClient) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return scannedRow{values: []any{"flights", "config", "motherduck", "[]"}}
}

func TestCreateFlightsSecretRendersParamsMap(t *testing.T) {
	ctx := t.Context()
	flightParams, d := types.MapValueFrom(ctx, types.StringType, map[string]string{"api_key": "it's secret", "REGION": "eu"})
	if d.HasError() {
		t.Fatal(d)
	}
	for _, replace := range []bool{false, true} {
		client := &flightsSecretClient{}
		r := &secretResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
		state := &secretDiagnosticState{model: secretModel{
			Name:           types.StringValue("flight_creds"),
			Type:           types.StringValue("flights"),
			SecretProvider: types.StringNull(),
			Params:         types.MapNull(types.StringType),
			FlightParams:   flightParams,
			SecretSQL:      types.StringNull(),
		}}
		var diags diag.Diagnostics
		r.createSecret(ctx, state, state, replace, &diags)
		if diags.HasError() {
			t.Fatal(diags)
		}
		keyword := "CREATE SECRET"
		if replace {
			keyword = "CREATE OR REPLACE SECRET"
		}
		want := keyword + ` "flight_creds" IN MOTHERDUCK (TYPE FLIGHTS, PARAMS MAP {'REGION': 'eu', 'api_key': 'it''s secret'})`
		if len(client.execs) != 1 || client.execs[0] != want {
			t.Fatalf("CREATE SECRET = %v, want %s", client.execs, want)
		}
	}
}

func TestValidateFlightsSecretConfig(t *testing.T) {
	ctx := t.Context()
	flightMap := func(values map[string]string) types.Map {
		m, d := types.MapValueFrom(ctx, types.StringType, values)
		if d.HasError() {
			t.Fatal(d)
		}
		return m
	}
	nullValue := types.MapValueMust(types.StringType, map[string]attr.Value{"api_key": types.StringNull()})
	tests := map[string]struct {
		model       secretModel
		wantErrors  []string
		wantWarning string
	}{
		"flights with flight_params": {
			model: secretModel{Type: types.StringValue("flights"), FlightParams: flightMap(map[string]string{"api_key": "x"})},
		},
		"uppercase type": {
			model: secretModel{Type: types.StringValue("FLIGHTS"), FlightParams: flightMap(map[string]string{"api_key": "x"})},
		},
		"flights with raw PARAMS": {
			model: secretModel{Type: types.StringValue("flights"), SecretSQL: types.StringValue("PARAMS MAP {'api_key': 'x'}")},
		},
		"flight_params on another type": {
			model:      secretModel{Type: types.StringValue("s3"), FlightParams: flightMap(map[string]string{"api_key": "x"})},
			wantErrors: []string{"flight_params"},
		},
		"flights with params": {
			model:      secretModel{Type: types.StringValue("flights"), Params: flightMap(map[string]string{"api_key": "x"}), FlightParams: flightMap(map[string]string{"api_key": "x"})},
			wantErrors: []string{"params"},
		},
		"flights without values": {
			model:      secretModel{Type: types.StringValue("flights")},
			wantErrors: []string{"flight_params"},
		},
		"empty flight_params": {
			model:      secretModel{Type: types.StringValue("flights"), FlightParams: flightMap(map[string]string{})},
			wantErrors: []string{"flight_params"},
		},
		"empty key": {
			model:      secretModel{Type: types.StringValue("flights"), FlightParams: flightMap(map[string]string{"": "x"})},
			wantErrors: []string{"flight_params"},
		},
		"null value": {
			model:      secretModel{Type: types.StringValue("flights"), FlightParams: nullValue},
			wantErrors: []string{`flight_params["api_key"]`},
		},
		"reserved key": {
			model:       secretModel{Type: types.StringValue("flights"), FlightParams: flightMap(map[string]string{"MOTHERDUCK_TOKEN": "x"})},
			wantWarning: `flight_params["MOTHERDUCK_TOKEN"]`,
		},
		"unknown type": {
			model: secretModel{Type: types.StringUnknown(), FlightParams: flightMap(map[string]string{"api_key": "x"})},
		},
		"unknown flight_params": {
			model: secretModel{Type: types.StringValue("flights"), FlightParams: types.MapUnknown(types.StringType)},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateSecretConfig(tc.model, &diags)
			var gotErrors []string
			for _, d := range diags.Errors() {
				if withPath, ok := d.(diag.DiagnosticWithPath); ok {
					gotErrors = append(gotErrors, withPath.Path().String())
				}
			}
			if strings.Join(gotErrors, ",") != strings.Join(tc.wantErrors, ",") {
				t.Fatalf("error paths = %v, want %v: %v", gotErrors, tc.wantErrors, diags)
			}
			var gotWarning string
			for _, d := range diags.Warnings() {
				if withPath, ok := d.(diag.DiagnosticWithPath); ok {
					gotWarning = withPath.Path().String()
				}
			}
			if gotWarning != tc.wantWarning {
				t.Fatalf("warning path = %q, want %q: %v", gotWarning, tc.wantWarning, diags)
			}
		})
	}
}
