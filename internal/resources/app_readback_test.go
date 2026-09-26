package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
)

// scriptedAppSQL is a small SQL fake for app resources. Each handler receives
// the query and returns the row or JSON result, and every call is recorded.
type scriptedAppSQL struct {
	providerctx.SQLClient

	mu         sync.Mutex
	queries    []string
	probes     []string
	queryRow   func(query string) mdsql.RowScanner
	queryJSON  func(query string) (string, error)
	exec       func(query string) error
	functionOK func(name string) bool
}

func (c *scriptedAppSQL) Available() bool { return true }

func (c *scriptedAppSQL) Exists(_ context.Context, _ string, args ...any) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name := fmt.Sprint(args...)
	c.probes = append(c.probes, name)
	if c.functionOK == nil {
		return true, nil
	}
	return c.functionOK(name), nil
}

func (c *scriptedAppSQL) QueryRow(_ context.Context, query string, _ ...any) mdsql.RowScanner {
	c.record(query)
	if c.queryRow == nil {
		return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
	}
	return c.queryRow(query)
}

func (c *scriptedAppSQL) QueryRowsJSON(_ context.Context, query string, _ ...any) (string, error) {
	c.record(query)
	if c.queryJSON == nil {
		return "", fmt.Errorf("unexpected JSON query %q", query)
	}
	return c.queryJSON(query)
}

func (c *scriptedAppSQL) Exec(_ context.Context, query string, _ ...any) error {
	c.record(query)
	if c.exec == nil {
		return fmt.Errorf("unexpected exec %q", query)
	}
	return c.exec(query)
}

func (c *scriptedAppSQL) record(query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, query)
}

func (c *scriptedAppSQL) count(fragment string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, query := range c.queries {
		if strings.Contains(query, fragment) {
			n++
		}
	}
	return n
}

// scannedRow assigns values positionally into the scan destinations used by
// the app resources. A nil value leaves a nullable destination invalid.
type scannedRow struct {
	values []any
	err    error
}

func (r scannedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(r.values))
	}
	for i, value := range r.values {
		switch target := dest[i].(type) {
		case *string:
			*target = value.(string)
		case *int64:
			*target = value.(int64)
		case *stdsql.NullString:
			if value == nil {
				*target = stdsql.NullString{}
			} else {
				*target = stdsql.NullString{String: value.(string), Valid: true}
			}
		case *stdsql.NullInt64:
			if value == nil {
				*target = stdsql.NullInt64{}
			} else {
				*target = stdsql.NullInt64{Int64: value.(int64), Valid: true}
			}
		default:
			return fmt.Errorf("unsupported scan destination %T", dest[i])
		}
	}
	return nil
}

const testAppID = "123e4567-e89b-42d3-a456-426614174000"

func TestClearableStringFromLive(t *testing.T) {
	cases := []struct {
		name    string
		current types.String
		live    stdsql.NullString
		want    types.String
	}{
		{"unconfigured empty", types.StringNull(), stdsql.NullString{String: "", Valid: true}, types.StringNull()},
		{"unconfigured null", types.StringNull(), stdsql.NullString{}, types.StringNull()},
		{"configured empty reads null", types.StringValue(""), stdsql.NullString{}, types.StringValue("")},
		{"configured empty reads empty", types.StringValue(""), stdsql.NullString{String: "", Valid: true}, types.StringValue("")},
		{"value", types.StringNull(), stdsql.NullString{String: "x", Valid: true}, types.StringValue("x")},
		{"drift", types.StringValue("a"), stdsql.NullString{String: "b", Valid: true}, types.StringValue("b")},
		{"cleared remotely", types.StringValue("a"), stdsql.NullString{}, types.StringNull()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clearableStringFromLive(tc.current, tc.live); !got.Equal(tc.want) {
				t.Fatalf("clearableStringFromLive() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestGuideCreateDoesNotRetryNonIdempotentCreate(t *testing.T) {
	ctx := t.Context()
	client := &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		if strings.Contains(query, "MD_CREATE_GUIDE") {
			return scannedRow{err: errors.New("rpc error: code = Unavailable desc = request timed out")}
		}
		return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
	}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	s := resourceSchema(t, res)
	plan := tfsdk.Plan{Schema: s}
	model := guideModel{
		ID:               types.StringUnknown(),
		Topic:            types.StringNull(),
		Title:            types.StringValue("Guide"),
		Description:      types.StringNull(),
		Content:          types.StringValue("content"),
		ChangeComment:    types.StringNull(),
		ExternalID:       types.StringNull(),
		References:       types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()}),
		Access:           types.StringUnknown(),
		RoleNames:        types.SetUnknown(types.StringType),
		OwnerID:          types.StringUnknown(),
		OwnerName:        types.StringUnknown(),
		CurrentVersion:   types.Int64Unknown(),
		CreatedAt:        types.StringUnknown(),
		UpdatedAt:        types.StringUnknown(),
		VersionCreatedAt: types.StringUnknown(),
	}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	res.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("create should report the transient failure")
	}
	if got := client.count("MD_CREATE_GUIDE"); got != 1 {
		t.Fatalf("MD_CREATE_GUIDE attempts = %d, want 1", got)
	}
}

func TestReadGuideNormalizesClearedOptionalStrings(t *testing.T) {
	client := &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		if !strings.Contains(query, "MD_GET_GUIDE") {
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		}
		// topic is reported as an empty string and description as NULL.
		return scannedRow{values: []any{"", "Guide", nil, "content", nil, nil, "user", "owner", "owner name", int64(2), "c", "u", "v", nil}}
	}}
	res := &guideResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	for _, tc := range []struct {
		name                    string
		topic, description      types.String
		wantTopic, wantDescript types.String
	}{
		{"removed", types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()},
		{"set empty", types.StringValue(""), types.StringValue(""), types.StringValue(""), types.StringValue("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := guideModel{
				ID:          types.StringValue(testAppID),
				Topic:       tc.topic,
				Description: tc.description,
				References:  types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()}),
			}
			var diags diag.Diagnostics
			if !res.readGuide(t.Context(), &model, &diags) || diags.HasError() {
				t.Fatalf("readGuide diagnostics: %v", diags)
			}
			if !model.Topic.Equal(tc.wantTopic) || !model.Description.Equal(tc.wantDescript) {
				t.Fatalf("topic = %s, description = %s, want %s and %s", model.Topic, model.Description, tc.wantTopic, tc.wantDescript)
			}
		})
	}
}

func TestReadDiveKeepsUnsetDescriptionNull(t *testing.T) {
	client := &scriptedAppSQL{
		functionOK: func(name string) bool { return name != "md_update_dive_status" },
		queryRow: func(query string) mdsql.RowScanner {
			return scannedRow{values: []any{"Dive", "", int64(1), "c", "u", "owner", "content"}}
		},
	}
	res := &diveResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	model := diveModel{ID: types.StringValue(testAppID), Description: types.StringNull()}
	var diags diag.Diagnostics
	if !res.readDive(t.Context(), &model, &diags) || diags.HasError() {
		t.Fatalf("readDive diagnostics: %v", diags)
	}
	if !model.Description.IsNull() {
		t.Fatalf("description = %s, want null for an unset description reported as an empty string", model.Description)
	}
}

func flightReadbackClient(schedule any) *scriptedAppSQL {
	return &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		switch {
		case strings.Contains(query, "MD_GET_FLIGHT_VERSION"):
			return scannedRow{values: []any{"print(1)", nil, nil, nil, nil, int64(900)}}
		case strings.Contains(query, "MD_GET_FLIGHT("):
			return scannedRow{values: []any{"flight", schedule, "ACTIVE", int64(1), "c", "u"}}
		default:
			return scannedRow{err: fmt.Errorf("unexpected query %q", query)}
		}
	}}
}

func TestReadFlightNormalizesClearedSchedule(t *testing.T) {
	for _, tc := range []struct {
		name     string
		schedule any
		current  types.String
		want     types.String
	}{
		{"removed reads empty", "", types.StringNull(), types.StringNull()},
		{"set empty reads null", nil, types.StringValue(""), types.StringValue("")},
		{"value", "0 * * * *", types.StringNull(), types.StringValue("0 * * * *")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := &flightResource{baseResource: baseResource{provider: &providerctx.Context{SQL: flightReadbackClient(tc.schedule)}}}
			model := flightModel{
				ID:                types.StringValue(testAppID),
				ScheduleCron:      tc.current,
				Config:            types.MapNull(types.StringType),
				FlightSecretNames: types.ListNull(types.StringType),
			}
			var diags diag.Diagnostics
			if !res.readFlight(t.Context(), &model, &diags) || diags.HasError() {
				t.Fatalf("readFlight diagnostics: %v", diags)
			}
			if !model.ScheduleCron.Equal(tc.want) {
				t.Fatalf("schedule_cron = %s, want %s", model.ScheduleCron, tc.want)
			}
		})
	}
}

func TestFlightCreateProbesEachFunctionOncePerOperation(t *testing.T) {
	ctx := t.Context()
	client := flightReadbackClient(nil)
	readback := client.queryRow
	client.queryRow = func(query string) mdsql.RowScanner {
		if strings.Contains(query, "MD_CREATE_FLIGHT") {
			return scannedRow{values: []any{testAppID}}
		}
		return readback(query)
	}
	res := &flightResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	s := resourceSchema(t, res)
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, plannedFlight()); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	res.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", resp.Diagnostics)
	}
	seen := map[string]int{}
	for _, probe := range client.probes {
		seen[probe]++
	}
	for name, count := range seen {
		if count != 1 {
			t.Fatalf("%s probed %d times in one operation, want 1 (probes: %v)", name, count, client.probes)
		}
	}
}

func plannedFlight() *flightModel {
	return &flightModel{
		ID:                types.StringUnknown(),
		Name:              types.StringValue("flight"),
		SourceCode:        types.StringValue("print(1)"),
		ScheduleCron:      types.StringNull(),
		RequirementsTxt:   types.StringNull(),
		Config:            types.MapNull(types.StringType),
		AccessTokenName:   types.StringNull(),
		FlightSecretNames: types.ListNull(types.StringType),
		MaxRuntimeSec:     types.Int64Unknown(),
		Status:            types.StringUnknown(),
		CurrentVersion:    types.Int64Unknown(),
		CreatedAt:         types.StringUnknown(),
		UpdatedAt:         types.StringUnknown(),
	}
}

func TestFlightCreatePersistsIDWhenReadbackFails(t *testing.T) {
	ctx := t.Context()
	client := &scriptedAppSQL{queryRow: func(query string) mdsql.RowScanner {
		if strings.Contains(query, "MD_CREATE_FLIGHT") {
			return scannedRow{values: []any{testAppID}}
		}
		return scannedRow{err: stdsql.ErrNoRows}
	}}
	res := &flightResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	s := resourceSchema(t, res)
	plan := tfsdk.Plan{Schema: s}
	model := plannedFlight()
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	res.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("create should fail when the Flight cannot be read back")
	}
	var id types.String
	if d := resp.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() {
		t.Fatal(d)
	}
	if id.ValueString() != testAppID {
		t.Fatalf("state id = %q, want the created Flight ID so the resource is tainted rather than orphaned", id.ValueString())
	}
	var status types.String
	if d := resp.State.GetAttribute(ctx, path.Root("status"), &status); d.HasError() {
		t.Fatal(d)
	}
	if status.IsUnknown() {
		t.Fatal("create must not write unknown computed values to state")
	}
}

const testFlightID = "11111111-1111-4111-8111-111111111111"

func flightRunState(t *testing.T, res *flightRunResource) tfsdk.State {
	t.Helper()
	s := resourceSchema(t, res)
	state := tfsdk.State{Schema: s}
	model := flightRunModel{
		ID:            types.StringValue("run-7"),
		FlightID:      types.StringValue(testFlightID),
		Config:        types.MapNull(types.StringType),
		RunNumber:     types.Int64Value(7),
		Status:        types.StringValue("RUNNING"),
		FlightVersion: types.Int64Value(1),
		CreatedAt:     types.StringValue("2026-09-01T00:00:00Z"),
	}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}

func runPage(first, last int) string {
	rows := make([]string, 0)
	for n := first; n >= last; n-- {
		rows = append(rows, fmt.Sprintf(`{"run_id":"run-%d","status":"SUCCEEDED","run_number":%d,"flight_version":1,"created_at":"2026-09-01T00:00:00Z"}`, n, n))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func TestFlightRunReadFindsRunBeyondFirstListingPage(t *testing.T) {
	client := &scriptedAppSQL{
		queryRow: func(query string) mdsql.RowScanner {
			// The default page only contains the newest runs.
			return scannedRow{err: stdsql.ErrNoRows}
		},
		queryJSON: func(query string) (string, error) {
			switch {
			case strings.Contains(query, `"OFFSET" := 0)`):
				return runPage(57, 8), nil
			case strings.Contains(query, `"OFFSET" := 50)`):
				return runPage(7, 1), nil
			default:
				return "", fmt.Errorf("unexpected page query %q", query)
			}
		},
	}
	res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
	state := flightRunState(t, res)
	resp := resource.ReadResponse{State: state}
	res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}
	var status types.String
	if d := resp.State.GetAttribute(t.Context(), path.Root("status"), &status); d.HasError() {
		t.Fatal(d)
	}
	if status.ValueString() != "SUCCEEDED" {
		t.Fatalf("status = %s, want the status from the second listing page", status)
	}
}

func TestFlightRunReadKeepsStateWhileParentFlightExists(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flightFound bool
		wantRemoved bool
	}{
		{"parent exists", true, false},
		{"parent deleted", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &scriptedAppSQL{
				queryRow: func(query string) mdsql.RowScanner {
					if strings.Contains(query, "MD_GET_FLIGHT(") {
						if tc.flightFound {
							return scannedRow{values: []any{testFlightID}}
						}
						return scannedRow{err: stdsql.ErrNoRows}
					}
					return scannedRow{err: stdsql.ErrNoRows}
				},
				queryJSON: func(string) (string, error) { return runPage(12, 8), nil },
			}
			res := &flightRunResource{baseResource: baseResource{provider: &providerctx.Context{SQL: client}}}
			state := flightRunState(t, res)
			resp := resource.ReadResponse{State: state}
			res.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("read diagnostics: %v", resp.Diagnostics)
			}
			if removed := resp.State.Raw.IsNull(); removed != tc.wantRemoved {
				t.Fatalf("state removed = %t, want %t", removed, tc.wantRemoved)
			}
			if !tc.wantRemoved && resp.Diagnostics.WarningsCount() != 1 {
				t.Fatalf("want one warning when the run is kept, got %v", resp.Diagnostics)
			}
		})
	}
}

func resourceSchema(t *testing.T, res resource.Resource) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	res.Schema(t.Context(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}
