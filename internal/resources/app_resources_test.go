package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type deadlineFlightClient struct {
	providerctx.SQLClient
	t       *testing.T
	queried bool
}

func TestFlightRunWaitPolicyDoesNotReplaceExecution(t *testing.T) {
	var resp resource.SchemaResponse
	NewFlightRunResource().Schema(t.Context(), resource.SchemaRequest{}, &resp)
	if resp.Schema.DeprecationMessage == "" {
		t.Fatal("run resource must explain its deprecation")
	}
	if len(resp.Schema.Attributes["wait_for_status"].(schema.StringAttribute).PlanModifiers) != 0 {
		t.Fatal("wait status must not replace an execution")
	}
	for _, name := range []string{"poll_interval_seconds", "timeout_seconds"} {
		if len(resp.Schema.Attributes[name].(schema.Int64Attribute).PlanModifiers) != 0 {
			t.Fatalf("%s must not replace an execution", name)
		}
	}
	if len(resp.Schema.Attributes["flight_id"].(schema.StringAttribute).PlanModifiers) == 0 {
		t.Fatal("changing Flight identity must still replace the run")
	}
}

func (c *deadlineFlightClient) Available() bool { return true }
func (c *deadlineFlightClient) QueryRow(ctx context.Context, _ string, _ ...any) mdsql.RowScanner {
	c.queried = true
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Second {
		c.t.Error("Flight status query must inherit the configured two-second deadline")
	}
	return failedFlightRow{}
}

type failedFlightRow struct{}

func (failedFlightRow) Scan(...any) error { return context.DeadlineExceeded }

func TestFlightWaitBoundsStatusQuery(t *testing.T) {
	client := &deadlineFlightClient{t: t}
	r := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := flightRunModel{
		Status: types.StringValue("RUNNING"), WaitForStatus: types.StringValue("succeeded"),
		TimeoutSeconds: types.Int64Value(2), PollIntervalSeconds: types.Int64Value(1),
	}
	var diags diag.Diagnostics
	r.waitForFlightRun(context.Background(), &model, &diags)
	if !client.queried || !diags.HasError() {
		t.Fatal("expected failed status query")
	}
}

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

type diveReadbackClient struct{}

func (diveReadbackClient) Available() bool                                      { return true }
func (diveReadbackClient) AttachDatabase(context.Context, string) error         { return nil }
func (diveReadbackClient) Close() error                                         { return nil }
func (diveReadbackClient) Exists(context.Context, string, ...any) (bool, error) { return true, nil }
func (diveReadbackClient) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return "[]", nil
}
func (diveReadbackClient) Exec(context.Context, string, ...any) error { return nil }
func (diveReadbackClient) ScalarString(context.Context, string, ...any) (string, error) {
	return "", nil
}
func (diveReadbackClient) WithDatabaseUse(context.Context, string, func(func(string, ...any) error) error) error {
	return nil
}
func (diveReadbackClient) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return diveReadbackRow{}
}

type diveReadbackRow struct{}

func (diveReadbackRow) Scan(dest ...any) error {
	values := []any{"Audit Dive", "", int64(2), "created", "updated", "owner", "content", "ready", "changed", "00000000-0000-4000-8000-000000000000", int64(2)}
	for i, value := range values {
		switch target := dest[i].(type) {
		case *stdsql.NullString:
			target.Valid = true
			target.String = value.(string)
		case *stdsql.NullInt64:
			target.Valid = true
			target.Int64 = value.(int64)
		}
	}
	return nil
}

func TestReadDivePreservesConfiguredAPIVersionWhenPublicReadbackOmitsIt(t *testing.T) {
	model := diveModel{ID: types.StringValue("123e4567-e89b-42d3-a456-426614174000"), APIVersion: types.Int64Value(1)}
	resource := &diveResource{baseResource: baseResource{provider: &providerctx.Context{SQL: diveReadbackClient{}}}}
	var diags diag.Diagnostics
	if !resource.readDive(context.Background(), &model, &diags) || diags.HasError() {
		t.Fatalf("readDive diagnostics: %v", diags)
	}
	if got := model.APIVersion.ValueInt64(); got != 1 {
		t.Fatalf("readDive changed configured api_version to %d, want 1", got)
	}
}

func TestDiveImportedAPIVersionGetsOneCorrectiveUpdateThenConverges(t *testing.T) {
	plan := &diveModel{Content: types.StringValue("content"), APIVersion: types.Int64Value(1)}
	imported := &diveModel{Content: types.StringValue("content"), APIVersion: types.Int64Null()}
	args, update := diveContentArgs(context.Background(), plan, imported, &diag.Diagnostics{})
	if !update || args["api_version"] != "1" {
		t.Fatalf("imported api_version correction = %#v, update=%v. Want one update with api_version 1", args, update)
	}
	refreshed := &diveModel{Content: types.StringValue("content"), APIVersion: types.Int64Value(1)}
	if _, update = diveContentArgs(context.Background(), plan, refreshed, &diag.Diagnostics{}); update {
		t.Fatal("matching api_version after corrective update should converge to no update")
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

// diveCreateOrphanClient answers MD_CREATE_DIVE with an ID and then reports
// that MD_GET_DIVE returned no row, simulating an eventually consistent read.
type diveCreateOrphanClient struct{ diveReadbackClient }

func (diveCreateOrphanClient) QueryRow(_ context.Context, query string, _ ...any) mdsql.RowScanner {
	if strings.Contains(query, "MD_CREATE_DIVE") {
		return diveCreatedRow{}
	}
	return errRowScanner{err: stdsql.ErrNoRows}
}

type diveCreatedRow struct{}

func (diveCreatedRow) Scan(dest ...any) error {
	*(dest[0].(*string)) = "123e4567-e89b-42d3-a456-426614174000"
	return nil
}

func TestDiveCreatePersistsIDWhenReadbackFails(t *testing.T) {
	ctx := context.Background()
	res := &diveResource{baseResource: baseResource{provider: &providerctx.Context{SQL: diveCreateOrphanClient{}}}}
	var schemaResp resource.SchemaResponse
	res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema

	planModel := diveModel{
		ID:                types.StringUnknown(),
		Title:             types.StringValue("Audit Dive"),
		Content:           types.StringValue("content"),
		RequiredResources: types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"alias": types.StringType, "url": types.StringType}}),
		Status:            types.StringUnknown(),
		StatusChangedAt:   types.StringUnknown(),
		StatusSetBy:       types.StringUnknown(),
		StatusVersion:     types.Int64Unknown(),
		CurrentVersion:    types.Int64Unknown(),
		CreatedAt:         types.StringUnknown(),
		UpdatedAt:         types.StringUnknown(),
		OwnerName:         types.StringUnknown(),
	}
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("plan set: %v", diags)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	resp.State.Raw = tftypes.NewValue(s.Type().TerraformType(ctx), nil)
	res.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("create should fail when the Dive cannot be read back")
	}
	var id types.String
	if diags := resp.State.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		t.Fatalf("state id: %v", diags)
	}
	if id.ValueString() != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatalf("state id = %q, want the created Dive ID so the resource is tainted rather than orphaned", id.ValueString())
	}
}

// The live MD_GET_DIVE/MD_DELETE_DIVE surface reports absence as an Invalid
// Error, rather than DuckDB's Catalog Error.
type missingDiveClient struct {
	diveReadbackClient
	err error
}

func (c missingDiveClient) QueryRow(context.Context, string, ...any) mdsql.RowScanner {
	return errRowScanner{err: c.err}
}
func (c missingDiveClient) QueryRowsJSON(context.Context, string, ...any) (string, error) {
	return "", c.err
}
func TestDiveMissingRemoteLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		missing bool
	}{
		{"deleted", &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Could not find Dive"}, true},
		{"permission", &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Permission denied for Dive"}, false},
		{"other missing dependency", &duckdb.Error{Type: duckdb.ErrorTypeInvalid, Msg: "Invalid Error: MDExternalException: Could not find Dive dependency"}, false},
		{"untyped", errors.New("Invalid Error: MDExternalException: Could not find Dive"), false},
		{"transport", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			res := &diveResource{baseResource: baseResource{provider: &providerctx.Context{SQL: missingDiveClient{err: tc.err}}}}
			var schemaResp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			state := tfsdk.State{Schema: schemaResp.Schema}
			model := diveModel{ID: types.StringValue("123e4567-e89b-42d3-a456-426614174000"), Title: types.StringValue("test"), Content: types.StringValue("content"), RequiredResources: types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"alias": types.StringType, "url": types.StringType}})}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			read := resource.ReadResponse{State: state}
			res.Read(ctx, resource.ReadRequest{State: state}, &read)
			if read.Diagnostics.HasError() == tc.missing {
				t.Fatalf("read diagnostics: %v", read.Diagnostics)
			}
			if read.State.Raw.IsNull() != tc.missing {
				t.Fatalf("state removed = %t, want %t", read.State.Raw.IsNull(), tc.missing)
			}
			deleted := resource.DeleteResponse{State: state}
			res.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
			if deleted.Diagnostics.HasError() == tc.missing {
				t.Fatalf("delete diagnostics: %v", deleted.Diagnostics)
			}
		})
	}
}

func TestFlightWaitReportsConfiguredTimeout(t *testing.T) {
	res := &flightRunResource{}
	model := flightRunModel{
		Status: types.StringValue("RUNNING"), WaitForStatus: types.StringValue("succeeded"),
		TimeoutSeconds: types.Int64Value(1), PollIntervalSeconds: types.Int64Value(10),
		RunNumber: types.Int64Value(42),
	}
	var diags diag.Diagnostics
	res.waitForFlightRun(t.Context(), &model, &diags)
	if len(diags) != 1 || diags[0].Summary() != "Timed out waiting for MotherDuck Flight run" {
		t.Fatalf("expected configured timeout diagnostic, got %v", diags)
	}
	if !strings.Contains(diags[0].Detail(), "Flight run 42") || !strings.Contains(diags[0].Detail(), "RUNNING") {
		t.Fatalf("timeout must identify the run and last status: %v", diags)
	}
}

func TestFlightWaitPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := &flightRunResource{}
	model := flightRunModel{
		Status: types.StringValue("RUNNING"), WaitForStatus: types.StringValue("succeeded"),
		TimeoutSeconds: types.Int64Value(60), PollIntervalSeconds: types.Int64Value(10),
	}
	var diags diag.Diagnostics
	res.waitForFlightRun(ctx, &model, &diags)
	if len(diags) != 1 || diags[0].Summary() != "Interrupted while waiting for MotherDuck Flight run" {
		t.Fatalf("expected caller cancellation diagnostic, got %v", diags)
	}
}
