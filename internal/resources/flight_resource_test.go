package resources

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFlightArgs(t *testing.T) {
	ctx := context.Background()
	config, configDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"warehouse": "analytics"})
	if configDiags.HasError() {
		t.Fatalf("config diagnostics: %v", configDiags)
	}
	secrets, secretDiags := types.ListValueFrom(ctx, types.StringType, []string{"aws", "github"})
	if secretDiags.HasError() {
		t.Fatalf("secret diagnostics: %v", secretDiags)
	}
	model := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringValue("0 5 * * *"),
		RequirementsTxt:   types.StringValue("requests==2.32.0"),
		Config:            config,
		AccessTokenName:   types.StringValue("flight-token"),
		FlightSecretNames: secrets,
		MaxRuntimeSec:     types.Int64Value(900),
	}

	var diags diag.Diagnostics
	got, ok := flightCreateArgs(ctx, model, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("flightCreateArgs failed: %v", diags)
	}
	// #nosec G101 -- expected SQL literals are not credentials.
	want := map[string]string{
		"access_token_name":   "'flight-token'",
		"config":              "MAP {'warehouse': 'analytics'}",
		"flight_secret_names": "['aws', 'github']",
		"name":                "'daily'",
		"max_runtime_sec":     "900",
		"requirements_txt":    "'requests==2.32.0'",
		"schedule_cron":       "'0 5 * * *'",
		"source_code":         "'print(''ok'')'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flightCreateArgs() = %#v, want %#v", got, want)
	}
}

func TestFlightUpdateArgsClearsOptionalFields(t *testing.T) {
	ctx := context.Background()
	stateConfig, configDiags := types.MapValueFrom(ctx, types.StringType, map[string]string{"warehouse": "analytics"})
	if configDiags.HasError() {
		t.Fatalf("config diagnostics: %v", configDiags)
	}
	stateSecrets, secretDiags := types.ListValueFrom(ctx, types.StringType, []string{"aws", "github"})
	if secretDiags.HasError() {
		t.Fatalf("secret diagnostics: %v", secretDiags)
	}
	plan := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringNull(),
		RequirementsTxt:   types.StringNull(),
		Config:            types.MapNull(types.StringType),
		AccessTokenName:   types.StringNull(),
		FlightSecretNames: types.ListNull(types.StringType),
		MaxRuntimeSec:     types.Int64Value(60),
	}
	state := &flightModel{
		Name:              types.StringValue("daily"),
		SourceCode:        types.StringValue("print('ok')"),
		ScheduleCron:      types.StringValue("0 5 * * *"),
		RequirementsTxt:   types.StringValue("requests==2.32.0"),
		Config:            stateConfig,
		AccessTokenName:   types.StringNull(),
		FlightSecretNames: stateSecrets,
		MaxRuntimeSec:     types.Int64Value(900),
	}

	var diags diag.Diagnostics
	got, ok := flightUpdateArgs(ctx, plan, state, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("flightUpdateArgs failed: %v", diags)
	}
	want := map[string]string{
		"config":              "NULL",
		"flight_secret_names": "NULL",
		"max_runtime_sec":     "60",
		"requirements_txt":    "NULL",
		"schedule_cron":       "''",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flightUpdateArgs() = %#v, want %#v", got, want)
	}
}

func TestFlightUpdateArgsRejectsAccessTokenClear(t *testing.T) {
	ctx := context.Background()
	plan := &flightModel{
		Name:            types.StringValue("daily"),
		SourceCode:      types.StringValue("print('ok')"),
		AccessTokenName: types.StringNull(),
	}
	state := &flightModel{
		Name:            types.StringValue("daily"),
		SourceCode:      types.StringValue("print('ok')"),
		AccessTokenName: types.StringValue("flight-token"),
	}

	var diags diag.Diagnostics
	if _, ok := flightUpdateArgs(ctx, plan, state, &diags); ok {
		t.Fatal("expected access token clear to fail")
	}
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), "Replace the Flight resource") {
		t.Fatalf("expected replacement diagnostic, got %v", diags)
	}
}

func TestOptionalFlightVersionValuesFromJSON(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics

	nullList := optionalStringListFromJSON(ctx, types.ListNull(types.StringType), sql.NullString{String: "[]", Valid: true}, "flight_secret_names", &diags)
	if diags.HasError() {
		t.Fatalf("list diagnostics: %v", diags)
	}
	if !nullList.IsNull() {
		t.Fatalf("empty live list should preserve null config state, got %#v", nullList)
	}

	currentList, listDiags := types.ListValueFrom(ctx, types.StringType, []string{"old"})
	if listDiags.HasError() {
		t.Fatalf("current list diagnostics: %v", listDiags)
	}
	emptyList := optionalStringListFromJSON(ctx, currentList, sql.NullString{String: "[]", Valid: true}, "flight_secret_names", &diags)
	if diags.HasError() {
		t.Fatalf("list diagnostics: %v", diags)
	}
	if emptyList.IsNull() {
		t.Fatal("empty live list should be set when prior state was configured")
	}

	liveMap := optionalStringMapFromJSON(ctx, types.MapNull(types.StringType), sql.NullString{String: `{"region":"us"}`, Valid: true}, "config", &diags)
	if diags.HasError() {
		t.Fatalf("map diagnostics: %v", diags)
	}
	if liveMap.IsNull() {
		t.Fatal("non-empty live map should be recorded")
	}

	if got := optionalStringFromLive(types.StringNull(), sql.NullString{String: "", Valid: true}); !got.IsNull() {
		t.Fatalf("empty live optional string should preserve null config state, got %#v", got)
	}
	if got := optionalStringFromLive(types.StringValue("old"), sql.NullString{String: "", Valid: true}); got.IsNull() || got.ValueString() != "" {
		t.Fatalf("empty live optional string should be recorded when prior state was configured, got %#v", got)
	}
}
