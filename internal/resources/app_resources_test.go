package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"testing"

	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type recordingDiveStatusClient struct {
	query string
	err   error
}

func (c *recordingDiveStatusClient) QueryRowsJSON(_ context.Context, query string, _ ...any) (string, error) {
	c.query = query
	return "[]", c.err
}

func TestAppResourceUUIDValidator(t *testing.T) {
	ctx := context.Background()
	v := tfvalidators.UUID()

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"uuid":                {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000"), wantErr: false},
		"leading whitespace":  {value: types.StringValue(" 123e4567-e89b-42d3-a456-426614174000"), wantErr: true},
		"trailing whitespace": {value: types.StringValue("123e4567-e89b-42d3-a456-426614174000 "), wantErr: true},
		"bad":                 {value: types.StringValue("not-a-uuid"), wantErr: true},
		"unknown":             {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("id"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestAppResourceImportsRejectInvalidUUIDs(t *testing.T) {
	for name, res := range map[string]resource.Resource{
		"dive":   NewDiveResource(),
		"flight": NewFlightResource(),
	} {
		t.Run(name, func(t *testing.T) {
			for _, id := range []string{"not-a-uuid", " 123e4567-e89b-42d3-a456-426614174000", "123e4567-e89b-42d3-a456-426614174000 "} {
				var resp resource.ImportStateResponse
				res.(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatalf("expected import diagnostics for %q", id)
				}
			}
		})
	}
}

func TestFlightRunSchemaValidatesFlightID(t *testing.T) {
	var schemaResp resource.SchemaResponse
	NewFlightRunResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	flightID, ok := schemaResp.Schema.Attributes["flight_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("flight_id attribute = %T, want schema.StringAttribute", schemaResp.Schema.Attributes["flight_id"])
	}
	if len(flightID.Validators) == 0 {
		t.Fatal("flight_id should have UUID validators")
	}

	var validatorResp validator.StringResponse
	for _, v := range flightID.Validators {
		v.ValidateString(context.Background(), validator.StringRequest{
			Path:        path.Root("flight_id"),
			ConfigValue: types.StringValue("not-a-uuid"),
		}, &validatorResp)
	}
	if !validatorResp.Diagnostics.HasError() {
		t.Fatal("expected invalid flight_id diagnostics")
	}
}

func TestDiveRequiredResourcesAreSensitive(t *testing.T) {
	var schemaResp resource.SchemaResponse
	NewDiveResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	resources, ok := schemaResp.Schema.Attributes["required_resources"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("required_resources attribute = %T, want schema.ListNestedAttribute", schemaResp.Schema.Attributes["required_resources"])
	}
	if !resources.Sensitive {
		t.Fatal("required_resources should be sensitive because it contains share URLs")
	}
	url, ok := resources.NestedObject.Attributes["url"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("required_resources.url attribute = %T, want schema.StringAttribute", resources.NestedObject.Attributes["url"])
	}
	if !url.Sensitive {
		t.Fatal("required_resources.url should be sensitive so motherduck_share.url can flow into it")
	}
}

func TestDiveStatusSchemaValidatesLifecycleValues(t *testing.T) {
	var schemaResp resource.SchemaResponse
	NewDiveResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	status, ok := schemaResp.Schema.Attributes["status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("status attribute = %T, want schema.StringAttribute", schemaResp.Schema.Attributes["status"])
	}
	if !status.Optional || !status.Computed {
		t.Fatal("status should be optional and computed so omitted configuration uses the live default")
	}
	if len(status.PlanModifiers) == 0 {
		t.Fatal("status should preserve live state when the planned value is unknown")
	}

	for _, tc := range []struct {
		value   string
		wantErr bool
	}{
		{value: "draft"},
		{value: "ready"},
		{value: "endorsed"},
		{value: "archived"},
		{value: "published", wantErr: true},
	} {
		var validatorResp validator.StringResponse
		for _, v := range status.Validators {
			v.ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("status"),
				ConfigValue: types.StringValue(tc.value),
			}, &validatorResp)
		}
		if gotErr := validatorResp.Diagnostics.HasError(); gotErr != tc.wantErr {
			t.Fatalf("status %q diagnostics error = %t, want %t: %v", tc.value, gotErr, tc.wantErr, validatorResp.Diagnostics)
		}
	}
}

func TestDiveStatusChanged(t *testing.T) {
	if diveStatusChanged(types.StringNull(), types.StringValue("draft")) {
		t.Fatal("unconfigured status should not trigger an update")
	}
	if diveStatusChanged(types.StringUnknown(), types.StringValue("draft")) {
		t.Fatal("unknown status should not trigger an update")
	}
	if diveStatusChanged(types.StringValue("ready"), types.StringValue("ready")) {
		t.Fatal("equal status should not trigger an update")
	}
	if !diveStatusChanged(types.StringValue("archived"), types.StringValue("ready")) {
		t.Fatal("changed configured status should trigger an update")
	}
}

func TestUpdateDiveStatusBuildsPublicFunctionCall(t *testing.T) {
	client := &recordingDiveStatusClient{}
	var diags diag.Diagnostics
	resource := &diveResource{}
	if !resource.updateDiveStatus(context.Background(), client, "123e4567-e89b-42d3-a456-426614174000", "ready", &diags) {
		t.Fatalf("updateDiveStatus diagnostics: %v", diags)
	}
	want := "SELECT * FROM MD_UPDATE_DIVE_STATUS(id := '123e4567-e89b-42d3-a456-426614174000'::UUID, status := 'ready')"
	if client.query != want {
		t.Fatalf("updateDiveStatus query = %q, want %q", client.query, want)
	}

	client.err = errors.New("permission denied")
	if resource.updateDiveStatus(context.Background(), client, "123e4567-e89b-42d3-a456-426614174000", "endorsed", &diags) {
		t.Fatal("updateDiveStatus should report client errors")
	}
	if !diags.HasError() {
		t.Fatal("expected updateDiveStatus diagnostics")
	}
}

func TestDiveRequiredResourcesArg(t *testing.T) {
	ctx := context.Background()
	objectType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"alias": types.StringType,
		"url":   types.StringType,
	}}
	resourceValue, objectDiags := types.ObjectValue(objectType.AttrTypes, map[string]attr.Value{
		"alias": types.StringValue("wiki'pageviews"),
		"url":   types.StringValue("md:_share/abc'def"),
	})
	if objectDiags.HasError() {
		t.Fatalf("object diagnostics: %v", objectDiags)
	}
	listValue, listDiags := types.ListValue(objectType, []attr.Value{resourceValue})
	if listDiags.HasError() {
		t.Fatalf("list diagnostics: %v", listDiags)
	}

	var diags diag.Diagnostics
	got, ok := diveRequiredResourcesArg(ctx, listValue, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("diveRequiredResourcesArg diagnostics: %v", diags)
	}
	want := "[{alias: 'wiki''pageviews', url: 'md:_share/abc''def'}]"
	if got != want {
		t.Fatalf("diveRequiredResourcesArg() = %q, want %q", got, want)
	}
}

func TestDiveRequiredResourcesArgTypesEmptyList(t *testing.T) {
	ctx := context.Background()
	objectType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"alias": types.StringType,
		"url":   types.StringType,
	}}
	listValue, listDiags := types.ListValue(objectType, nil)
	if listDiags.HasError() {
		t.Fatalf("list diagnostics: %v", listDiags)
	}

	var diags diag.Diagnostics
	got, ok := diveRequiredResourcesArg(ctx, listValue, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("diveRequiredResourcesArg diagnostics: %v", diags)
	}
	want := "[]::STRUCT(alias VARCHAR, url VARCHAR)[]"
	if got != want {
		t.Fatalf("diveRequiredResourcesArg() = %q, want %q", got, want)
	}
}

func TestFlightRunSchemaValidatesWaitOptions(t *testing.T) {
	var schemaResp resource.SchemaResponse
	NewFlightRunResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	waitForStatus, ok := schemaResp.Schema.Attributes["wait_for_status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("wait_for_status attribute = %T, want schema.StringAttribute", schemaResp.Schema.Attributes["wait_for_status"])
	}
	pollInterval, ok := schemaResp.Schema.Attributes["poll_interval_seconds"].(schema.Int64Attribute)
	if !ok {
		t.Fatalf("poll_interval_seconds attribute = %T, want schema.Int64Attribute", schemaResp.Schema.Attributes["poll_interval_seconds"])
	}
	timeout, ok := schemaResp.Schema.Attributes["timeout_seconds"].(schema.Int64Attribute)
	if !ok {
		t.Fatalf("timeout_seconds attribute = %T, want schema.Int64Attribute", schemaResp.Schema.Attributes["timeout_seconds"])
	}

	var waitResp validator.StringResponse
	for _, v := range waitForStatus.Validators {
		v.ValidateString(context.Background(), validator.StringRequest{
			Path:        path.Root("wait_for_status"),
			ConfigValue: types.StringValue("done"),
		}, &waitResp)
	}
	if !waitResp.Diagnostics.HasError() {
		t.Fatal("expected invalid wait_for_status diagnostics")
	}

	for _, attr := range []struct {
		name       string
		validators []validator.Int64
	}{
		{name: "poll_interval_seconds", validators: pollInterval.Validators},
		{name: "timeout_seconds", validators: timeout.Validators},
	} {
		var intResp validator.Int64Response
		for _, v := range attr.validators {
			v.ValidateInt64(context.Background(), validator.Int64Request{
				Path:        path.Root(attr.name),
				ConfigValue: types.Int64Value(0),
			}, &intResp)
		}
		if !intResp.Diagnostics.HasError() {
			t.Fatalf("expected invalid %s diagnostics", attr.name)
		}
	}
}

func TestNormalizeFlightRunStatus(t *testing.T) {
	tests := map[string]string{
		"succeeded":              "succeeded",
		" RUN_STATUS_SUCCEEDED ": "succeeded",
		"RUN_STATUS_FAILED":      "failed",
	}
	for input, want := range tests {
		if got := normalizeFlightRunStatus(input); got != want {
			t.Fatalf("normalizeFlightRunStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFlightRunFailedAcceptsRemoteEnumStatus(t *testing.T) {
	if !flightRunFailed("RUN_STATUS_FAILED") {
		t.Fatal("RUN_STATUS_FAILED should be treated as failed")
	}
	if flightRunFailed("RUN_STATUS_SUCCEEDED") {
		t.Fatal("RUN_STATUS_SUCCEEDED should not be treated as failed")
	}
}

func TestFlightConfigSchemasHaveValidators(t *testing.T) {
	for name, res := range map[string]resource.Resource{
		"flight":     NewFlightResource(),
		"flight_run": NewFlightRunResource(),
	} {
		t.Run(name, func(t *testing.T) {
			var schemaResp resource.SchemaResponse
			res.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
			}

			config, ok := schemaResp.Schema.Attributes["config"].(schema.MapAttribute)
			if !ok {
				t.Fatalf("config attribute = %T, want schema.MapAttribute", schemaResp.Schema.Attributes["config"])
			}
			if len(config.Validators) == 0 {
				t.Fatal("config should validate Flight runtime parameter names")
			}
		})
	}
}

func TestFlightConfigMapValidator(t *testing.T) {
	ctx := context.Background()
	v := flightConfigMapValidator{}

	tests := map[string]struct {
		values  map[string]attr.Value
		wantErr bool
	}{
		"valid": {
			values:  map[string]attr.Value{"RUN_OVERRIDE": types.StringValue("2026-07-06")},
			wantErr: false,
		},
		"empty key": {
			values:  map[string]attr.Value{"": types.StringValue("value")},
			wantErr: true,
		},
		"reserved token": {
			values:  map[string]attr.Value{"MOTHERDUCK_TOKEN": types.StringValue("value")},
			wantErr: true,
		},
		"reserved flight marker": {
			values:  map[string]attr.Value{"MOTHERDUCK_FLIGHTS_RUN": types.StringValue("value")},
			wantErr: true,
		},
		"key with equals": {
			values:  map[string]attr.Value{"BAD=KEY": types.StringValue("value")},
			wantErr: true,
		},
		"key with null byte": {
			values:  map[string]attr.Value{"BAD\x00KEY": types.StringValue("value")},
			wantErr: true,
		},
		"value with null byte": {
			values:  map[string]attr.Value{"BAD_KEY": types.StringValue("bad\x00value")},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			config, configDiags := types.MapValue(types.StringType, tc.values)
			if configDiags.HasError() {
				t.Fatalf("map diagnostics: %v", configDiags)
			}
			var resp validator.MapResponse
			v.ValidateMap(ctx, validator.MapRequest{
				Path:        path.Root("config"),
				ConfigValue: config,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestOptionalConfigOwnedStringFromLiveKeepsNullWhenUnconfigured(t *testing.T) {
	got := optionalConfigOwnedStringFromLive(types.StringNull(), stdsql.NullString{
		String: "MotherDuck Flights",
		Valid:  true,
	})
	if !got.IsNull() {
		t.Fatalf("optionalConfigOwnedStringFromLive() = %q, want null", got.ValueString())
	}

	got = optionalConfigOwnedStringFromLive(types.StringValue("custom-token"), stdsql.NullString{
		String: "custom-token",
		Valid:  true,
	})
	if got.ValueString() != "custom-token" {
		t.Fatalf("optionalConfigOwnedStringFromLive() = %q, want custom-token", got.ValueString())
	}
}
