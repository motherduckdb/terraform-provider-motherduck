package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAccessTokenTokenTypeSchema(t *testing.T) {
	var resp resource.SchemaResponse
	NewAccessTokenResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attr, ok := resp.Schema.Attributes["token_type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("token_type attribute = %T, want schema.StringAttribute", resp.Schema.Attributes["token_type"])
	}
	if !attr.Optional || !attr.Computed {
		t.Fatalf("token_type Optional=%v Computed=%v, want both true", attr.Optional, attr.Computed)
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("token_type should keep replacement plan modifiers")
	}
}

func TestAccessTokenImportRejectsEmptySegments(t *testing.T) {
	var resp resource.ImportStateResponse
	NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
		ID: "svc_reader/",
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import diagnostics for empty token ID segment")
	}
}

func TestAccessTokenImportRejectsInvalidUsername(t *testing.T) {
	var resp resource.ImportStateResponse
	NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
		ID: strings.Repeat("a", 256) + "/token-id",
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected import diagnostics for oversized username segment")
	}
}

func TestAccessTokenImportRejectsWhitespace(t *testing.T) {
	// #nosec G101 -- import IDs are non-secret test fixtures.
	tests := map[string]string{
		"leading username":  " svc_reader/token-id",
		"leading token id":  "svc_reader/ token-id",
		"trailing token id": "svc_reader/token-id ",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewAccessTokenResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for whitespace in access token import ID")
			}
		})
	}
}

func TestServiceAccountImportRejectsInvalidUsername(t *testing.T) {
	tests := map[string]string{
		"blank":         " ",
		"leading space": " svc_reader",
		"hyphen":        "svc-reader",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewServiceAccountResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for invalid service account username")
			}
		})
	}
}

// serviceAccountListingWithProbe serves GET /v1/users with the given body and
// status and records every request, so tests can assert that nothing else was
// called. probeStatus sets the GET /v1/users/{username}/instances answer. A
// 404 means the user is gone, and a 200 simulates a live account that an
// offset page shift hid from the listing. The listing honors the
// is_service_account filter like MotherDuck.
func serviceAccountListingWithProbe(t *testing.T, status int, body string, probeStatus int) (*serviceAccountResource, *[]string) {
	t.Helper()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/users":
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}
			var listing mdrest.ListUsersResponse
			if err := json.Unmarshal([]byte(body), &listing); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if filter := r.URL.Query().Get("is_service_account"); filter != "" {
				listing.Users = slices.DeleteFunc(listing.Users, func(u mdrest.User) bool {
					return strconv.FormatBool(u.IsServiceAccount) != filter
				})
			}
			_ = json.NewEncoder(w).Encode(listing)
		case "GET /v1/users/svc_reader/instances", "GET /v1/users/SVC_Reader/instances":
			if probeStatus == http.StatusNotFound {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"User not found"}`))
				return
			}
			w.WriteHeader(probeStatus)
			_, _ = w.Write([]byte(`{}`))
		case "DELETE /v1/users/svc_reader", "DELETE /v1/users/SVC_Reader":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	client, err := mdrest.New(server.URL, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	return &serviceAccountResource{baseResource: baseResource{provider: &providerctx.Context{REST: client}}}, &calls
}

func serviceAccountState(t *testing.T, username string) tfsdk.State {
	t.Helper()
	var schemaResp resource.SchemaResponse
	NewServiceAccountResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), serviceAccountModel{
		ID:          types.StringValue(username),
		Username:    types.StringValue(username),
		Description: types.StringNull(),
	}); diags.HasError() {
		t.Fatalf("seeding prior state: %v", diags)
	}
	return state
}

const (
	listedServiceAccount = `{"users":[{"id":"u1","username":"svc_reader","is_service_account":true,"roles":[]}],"total_count":1,"next_page_token":null}`
	listedHuman          = `{"users":[{"id":"u1","username":"svc_reader","first_name":"Ada","is_service_account":false,"roles":[]}],"total_count":1,"next_page_token":null}`
	listedNobody         = `{"users":[{"id":"u2","username":"other_svc","is_service_account":true,"roles":[]}],"total_count":1,"next_page_token":null}`
)

// TestServiceAccountReadRequiresServiceAccount pins how refresh treats the
// organization listing. MotherDuck has no per-user GET, and the old Duckling
// config probe succeeded for human users too, so a human could be imported
// and later deleted with all of its data.
func TestServiceAccountReadRequiresServiceAccount(t *testing.T) {
	const (
		serviceAccounts = "GET /v1/users"
		bothListings    = "GET /v1/users,GET /v1/users"
		withProbe       = "GET /v1/users,GET /v1/users,GET /v1/users/svc_reader/instances"
	)
	tests := map[string]struct {
		username    string
		status      int
		body        string
		probeStatus int
		wantRemoved bool
		wantErr     string
		wantCalls   string
	}{
		"service account keeps state":         {username: "svc_reader", status: http.StatusOK, body: listedServiceAccount, wantCalls: serviceAccounts},
		"case-insensitive match keeps state":  {username: "SVC_Reader", status: http.StatusOK, body: listedServiceAccount, wantCalls: serviceAccounts},
		"human user errors and keeps state":   {username: "svc_reader", status: http.StatusOK, body: listedHuman, wantErr: "not a service account", wantCalls: bothListings},
		"absent account drops state":          {username: "svc_reader", status: http.StatusOK, body: listedNobody, wantRemoved: true, wantCalls: withProbe},
		"page shift errors and keeps state":   {username: "svc_reader", status: http.StatusOK, body: listedNobody, probeStatus: http.StatusOK, wantErr: "could not confirm", wantCalls: withProbe},
		"probe failure errors, keeps state":   {username: "svc_reader", status: http.StatusOK, body: listedNobody, probeStatus: http.StatusForbidden, wantErr: "403", wantCalls: withProbe},
		"listing failure errors, keeps state": {username: "svc_reader", status: http.StatusForbidden, body: `{"code":"FORBIDDEN","message":"denied"}`, wantErr: "denied", wantCalls: serviceAccounts},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			probeStatus := tc.probeStatus
			if probeStatus == 0 {
				probeStatus = http.StatusNotFound
			}
			res, calls := serviceAccountListingWithProbe(t, tc.status, tc.body, probeStatus)
			prior := serviceAccountState(t, tc.username)
			resp := resource.ReadResponse{State: prior}
			res.Read(context.Background(), resource.ReadRequest{State: prior}, &resp)
			if got := strings.Join(*calls, ","); got != tc.wantCalls {
				t.Fatalf("requests = %s, want %s", got, tc.wantCalls)
			}
			if tc.wantErr == "" && resp.Diagnostics.HasError() {
				t.Fatalf("read diagnostics: %v", resp.Diagnostics)
			}
			if tc.wantErr != "" && !diagnosticsContain(resp.Diagnostics, tc.wantErr) {
				t.Fatalf("read diagnostics = %v, want %q", resp.Diagnostics, tc.wantErr)
			}
			if gotRemoved := resp.State.Raw.IsNull(); gotRemoved != tc.wantRemoved {
				t.Fatalf("state removed = %t, want %t", gotRemoved, tc.wantRemoved)
			}
		})
	}
}

func TestServiceAccountDeleteRefusesHumanUsers(t *testing.T) {
	tests := map[string]struct {
		username    string
		status      int
		body        string
		probeStatus int
		wantDelete  bool
		wantErr     string
	}{
		"service account is deleted":       {username: "svc_reader", status: http.StatusOK, body: listedServiceAccount, wantDelete: true},
		"case-insensitive match deleted":   {username: "SVC_Reader", status: http.StatusOK, body: listedServiceAccount, wantDelete: true},
		"human user is never deleted":      {username: "svc_reader", status: http.StatusOK, body: listedHuman, wantErr: "not a service account"},
		"absent account needs no delete":   {username: "svc_reader", status: http.StatusOK, body: listedNobody},
		"page shift is not deleted":        {username: "svc_reader", status: http.StatusOK, body: listedNobody, probeStatus: http.StatusOK, wantErr: "could not confirm"},
		"failed probe is not deleted":      {username: "svc_reader", status: http.StatusOK, body: listedNobody, probeStatus: http.StatusForbidden, wantErr: "verify"},
		"unverifiable account not deleted": {username: "svc_reader", status: http.StatusBadGateway, body: "bad gateway", wantErr: "verify"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			probeStatus := tc.probeStatus
			if probeStatus == 0 {
				probeStatus = http.StatusNotFound
			}
			res, calls := serviceAccountListingWithProbe(t, tc.status, tc.body, probeStatus)
			prior := serviceAccountState(t, tc.username)
			resp := resource.DeleteResponse{State: prior}
			res.Delete(context.Background(), resource.DeleteRequest{State: prior}, &resp)
			deleted := slices.Contains(*calls, "DELETE /v1/users/"+tc.username)
			if deleted != tc.wantDelete {
				t.Fatalf("requests = %v, want delete %t", *calls, tc.wantDelete)
			}
			if tc.wantErr == "" && resp.Diagnostics.HasError() {
				t.Fatalf("delete diagnostics: %v", resp.Diagnostics)
			}
			if tc.wantErr != "" && !diagnosticsContain(resp.Diagnostics, tc.wantErr) {
				t.Fatalf("delete diagnostics = %v, want %q", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func diagnosticsContain(diags diag.Diagnostics, text string) bool {
	for _, d := range diags {
		if d.Severity() == diag.SeverityError && (strings.Contains(d.Summary(), text) || strings.Contains(d.Detail(), text)) {
			return true
		}
	}
	return false
}

func TestDucklingConfigImportRejectsInvalidUsername(t *testing.T) {
	tests := map[string]string{
		"blank":         " ",
		"leading space": " svc_reader",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			var resp resource.ImportStateResponse
			NewDucklingConfigResource().(resource.ResourceWithImportState).ImportState(context.Background(), resource.ImportStateRequest{
				ID: id,
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected import diagnostics for invalid username")
			}
		})
	}
}

func TestServiceAccountUsernameValidator(t *testing.T) {
	ctx := context.Background()
	v := serviceAccountUsernameValidator{}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"simple": {value: types.StringValue("svc_reader_1"), wantErr: false},
		// MotherDuck accepts any Unicode letter, as /\p{L}/u does.
		"unicode letters":      {value: types.StringValue("équipe_1"), wantErr: false},
		"non-latin letters":    {value: types.StringValue("データ_reader")},
		"starts unicode digit": {value: types.StringValue("١reader"), wantErr: true},
		// JavaScript \d matches only ASCII digits, even with the u flag.
		"non-ascii digit": {value: types.StringValue("reader١"), wantErr: true},
		"starts digit":    {value: types.StringValue("1_reader"), wantErr: true},
		"starts _":        {value: types.StringValue("_reader"), wantErr: true},
		"hyphen":          {value: types.StringValue("svc-reader"), wantErr: true},
		"space":           {value: types.StringValue("svc reader"), wantErr: true},
		"emoji":           {value: types.StringValue("svc🦆reader"), wantErr: true},
		"blank":           {value: types.StringValue(" "), wantErr: true},
		"unknown":         {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("username"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRESTStringEnumValidators(t *testing.T) {
	ctx := context.Background()
	v := stringEnumValidator{name: "MotherDuck access token type", values: []string{"read_write", "read_scaling"}}

	tests := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"read write": {value: types.StringValue("read_write"), wantErr: false},
		"case":       {value: types.StringValue("READ_SCALING"), wantErr: true},
		"invalid":    {value: types.StringValue("read_only"), wantErr: true},
		"empty":      {value: types.StringValue(""), wantErr: true},
		"unknown":    {value: types.StringUnknown(), wantErr: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("token_type"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestRESTRangeValidators(t *testing.T) {
	ctx := context.Background()

	intValidator := accessTokenTTLValidators()[0]
	for name, tc := range map[string]struct {
		value   types.Int64
		wantErr bool
	}{
		"min":     {value: types.Int64Value(300), wantErr: false},
		"max":     {value: types.Int64Value(31536000), wantErr: false},
		"low":     {value: types.Int64Value(299), wantErr: true},
		"high":    {value: types.Int64Value(31536001), wantErr: true},
		"unknown": {value: types.Int64Unknown(), wantErr: false},
	} {
		t.Run("int64 "+name, func(t *testing.T) {
			var resp validator.Int64Response
			intValidator.ValidateInt64(ctx, validator.Int64Request{
				Path:        path.Root("ttl"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}

	floatValidator := float64RangeValidator{name: "MotherDuck read-scaling flock size", min: 0, max: 64}
	for name, tc := range map[string]struct {
		value   types.Float64
		wantErr bool
	}{
		"zero":    {value: types.Float64Value(0), wantErr: false},
		"max":     {value: types.Float64Value(64), wantErr: false},
		"low":     {value: types.Float64Value(-0.1), wantErr: true},
		"high":    {value: types.Float64Value(64.1), wantErr: true},
		"unknown": {value: types.Float64Unknown(), wantErr: false},
	} {
		t.Run("float64 "+name, func(t *testing.T) {
			var resp validator.Float64Response
			floatValidator.ValidateFloat64(ctx, validator.Float64Request{
				Path:        path.Root("read_scaling_flock_size"),
				ConfigValue: tc.value,
			}, &resp)
			if gotErr := resp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("diagnostics error = %t, want %t: %v", gotErr, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestValidateDucklingCooldownsDefersUnknownValues(t *testing.T) {
	model := ducklingConfigModel{
		ReadWriteInstanceSize:      types.StringValue("pulse"),
		ReadWriteCooldownSeconds:   types.Int64Unknown(),
		ReadScalingInstanceSize:    types.StringUnknown(),
		ReadScalingCooldownSeconds: types.Int64Value(300),
	}
	var diags diag.Diagnostics
	if !validateDucklingCooldowns(&model, &diags) {
		t.Fatalf("unknown cross-field values should defer validation: %v", diags)
	}
}

func TestAccessTokenReadDeletionInference(t *testing.T) {
	const username = "svc_reader"
	const tokenID = "tok_123"
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	tests := map[string]struct {
		status      int
		body        string
		expireAt    types.String
		wantRemoved bool
		wantWarning bool
		wantErr     bool
	}{
		"present token refreshes state": {
			status:   http.StatusOK,
			body:     `{"tokens":[{"id":"` + tokenID + `","name":"reader","token_type":"read_write","created_ts":"2026-01-01T00:00:00Z","expire_at":"` + future + `"}]}`,
			expireAt: types.StringValue(future),
		},
		"absent expired token drops state quietly": {
			status:      http.StatusOK,
			body:        `{"tokens":[]}`,
			expireAt:    types.StringValue(past),
			wantRemoved: true,
		},
		"absent live token drops state with warning": {
			status:      http.StatusOK,
			body:        `{"tokens":[{"id":"other","token_type":"read_write","created_ts":"2026-01-01T00:00:00Z"}]}`,
			expireAt:    types.StringValue(future),
			wantRemoved: true,
			wantWarning: true,
		},
		"absent token without expiry drops state with warning": {
			status:      http.StatusOK,
			body:        `{"tokens":[]}`,
			expireAt:    types.StringNull(),
			wantRemoved: true,
			wantWarning: true,
		},
		"list error preserves state": {
			status:   http.StatusBadGateway,
			body:     `<html>upstream unavailable</html>`,
			expireAt: types.StringValue(future),
			wantErr:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if want := "/v1/users/" + username + "/tokens"; r.URL.Path != want {
					t.Errorf("path = %q, want %q", r.URL.Path, want)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := mdrest.New(server.URL, "admin-token", mdrest.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
			if err != nil {
				t.Fatal(err)
			}
			res := NewAccessTokenResource().(*accessTokenResource)
			var configureResp resource.ConfigureResponse
			res.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerctx.Context{REST: client}}, &configureResp)
			if configureResp.Diagnostics.HasError() {
				t.Fatalf("configure diagnostics: %v", configureResp.Diagnostics)
			}
			var schemaResp resource.SchemaResponse
			res.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
			}
			priorState := tfsdk.State{Schema: schemaResp.Schema}
			if diags := priorState.Set(ctx, accessTokenModel{
				ID:        types.StringValue(tokenID),
				Username:  types.StringValue(username),
				Name:      types.StringValue("reader"),
				TTL:       types.Int64Null(),
				TokenType: types.StringValue("read_write"),
				Token:     types.StringValue("secret-value"),
				ExpireAt:  tc.expireAt,
				CreatedTS: types.StringValue("2026-01-01T00:00:00Z"),
				ReadOnly:  types.BoolValue(false),
			}); diags.HasError() {
				t.Fatalf("seeding prior state: %v", diags)
			}

			readResp := resource.ReadResponse{State: priorState}
			res.Read(ctx, resource.ReadRequest{State: priorState}, &readResp)

			if gotErr := readResp.Diagnostics.HasError(); gotErr != tc.wantErr {
				t.Fatalf("read error = %t, want %t: %v", gotErr, tc.wantErr, readResp.Diagnostics)
			}
			if gotRemoved := readResp.State.Raw.IsNull(); gotRemoved != tc.wantRemoved {
				t.Fatalf("state removed = %t, want %t", gotRemoved, tc.wantRemoved)
			}
			gotWarning := readResp.Diagnostics.WarningsCount() > 0
			if gotWarning != tc.wantWarning {
				t.Fatalf("warning present = %t, want %t: %v", gotWarning, tc.wantWarning, readResp.Diagnostics)
			}
			if tc.wantWarning {
				text := readResp.Diagnostics.Warnings()[0].Detail()
				if !strings.Contains(text, tokenID) || strings.Contains(text, "secret-value") {
					t.Fatalf("warning should name the token ID and never the secret: %q", text)
				}
			}
			if !tc.wantRemoved && !tc.wantErr {
				var got accessTokenModel
				if diags := readResp.State.Get(ctx, &got); diags.HasError() {
					t.Fatalf("reading state: %v", diags)
				}
				if got.Token.ValueString() != "secret-value" {
					t.Fatalf("secret was not preserved across Read: %q", got.Token.ValueString())
				}
			}
		})
	}
}

func TestAccessTokenExpired(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		value types.String
		want  bool
	}{
		"null":                    {types.StringNull(), false},
		"unknown":                 {types.StringUnknown(), false},
		"empty":                   {types.StringValue(""), false},
		"garbage":                 {types.StringValue("soon"), false},
		"rfc3339 past":            {types.StringValue("2026-09-01T00:00:00Z"), true},
		"rfc3339 future":          {types.StringValue("2026-12-01T00:00:00Z"), false},
		"fractional past":         {types.StringValue("2026-09-01T00:00:00.123456Z"), true},
		"non rfc3339 is cautious": {types.StringValue("2026-09-01 00:00:00"), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := accessTokenExpired(tc.value, now); got != tc.want {
				t.Fatalf("accessTokenExpired(%v) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}
