package diveembed

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func nullModel() Model {
	return Model{
		DiveID:            types.StringValue("dive-id"),
		Username:          types.StringValue("service-account"),
		SessionName:       types.StringNull(),
		SessionHint:       types.StringNull(),
		Version:           types.Int64Null(),
		RequiredResources: types.ListNull(types.ObjectType{AttrTypes: RequiredResourceAttrTypes}),
		InitialState:      types.StringNull(),
		Session:           types.StringNull(),
	}
}

func TestCreateMapsModelAndResponse(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := nullModel()
	model.SessionName = types.StringValue("session-name")

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if client.diveID != "dive-id" {
		t.Fatalf("dive ID = %q, want %q", client.diveID, "dive-id")
	}
	if client.request.Username != "service-account" {
		t.Fatalf("username = %q, want %q", client.request.Username, "service-account")
	}
	if client.request.SessionName != "session-name" {
		t.Fatalf("session name = %q, want %q", client.request.SessionName, "session-name")
	}
	if got := model.Session.ValueString(); got != "session-credential" {
		t.Fatalf("session = %q, want %q", got, "session-credential")
	}
}

func TestCreateSendsDeprecatedSessionHintAsSessionName(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := nullModel()
	model.SessionHint = types.StringValue("legacy-hint")

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	body := marshal(t, client.request)
	if !strings.Contains(body, `"session_name":"legacy-hint"`) || strings.Contains(body, "session_hint") {
		t.Fatalf("request body = %s, want session_name only", body)
	}
}

func TestCreateOmitsUnsetOptionalFields(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := nullModel()

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if body := marshal(t, client.request); body != `{"username":"service-account"}` {
		t.Fatalf("request body = %s, want username only", body)
	}
}

func TestCreateSendsVersionResourcesAndInitialState(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := nullModel()
	model.Version = types.Int64Value(3)
	model.RequiredResources = resourceList(t,
		resourceObject(t, "md:_share/sales/abc", types.StringValue("sales")),
		resourceObject(t, "md:_share/ops/def", types.StringNull()),
	)
	model.InitialState = types.StringValue("{\n  \"region\": \"emea\",\n  \"limit\": 10\n}")

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	want := `{"username":"service-account","version":3,"required_resources":[{"url":"md:_share/sales/abc","alias":"sales"},{"url":"md:_share/ops/def"}],"initial_state":{"region":"emea","limit":10}}`
	if body := marshal(t, client.request); body != want {
		t.Fatalf("request body = %s\nwant %s", body, want)
	}
}

func TestCreateKeepsExplicitlyEmptyRequiredResources(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := nullModel()
	model.RequiredResources = resourceList(t)

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if body := marshal(t, client.request); !strings.Contains(body, `"required_resources":[]`) {
		t.Fatalf("request body = %s, want an empty required_resources override", body)
	}
}

func TestCreateReturnsClientError(t *testing.T) {
	wantErr := errors.New("create failed")
	client := &fakeClient{err: wantErr}
	model := nullModel()

	if err := Create(context.Background(), client, &model); !errors.Is(err, wantErr) {
		t.Fatalf("Create() error = %v, want %v", err, wantErr)
	}
	if !model.Session.IsNull() {
		t.Fatalf("session = %#v, want null", model.Session)
	}
}

func TestValidateInitialStateValue(t *testing.T) {
	cases := map[string]bool{
		`{}`:                                     true,
		`{"region":"emea","nested":{"a":[1,2]}}`: true,
		`[]`:                                     false,
		`null`:                                   false,
		`"text"`:                                 false,
		`{"a":1} {"b":2}`:                        false,
		`{"a":1`:                                 false,
		`{"blob":"` + strings.Repeat("x", InitialStateMaxBytes) + `"}`:    false,
		`{"blob":"` + strings.Repeat("x", InitialStateMaxBytes-12) + `"}`: true,
	}
	for value, want := range cases {
		if _, ok := ValidateInitialStateValue(value); ok != want {
			name := value
			if len(name) > 40 {
				name = name[:40] + "..."
			}
			t.Errorf("ValidateInitialStateValue(%s) = %v, want %v", name, ok, want)
		}
	}
}

func TestRequiredResourcesSizeValidator(t *testing.T) {
	small := resourceList(t, resourceObject(t, "md:_share/sales/abc", types.StringNull()))
	large := resourceList(t, resourceObject(t, strings.Repeat("u", RequiredResourcesMaxBytes), types.StringNull()))
	unknownURL, diags := types.ObjectValue(RequiredResourceAttrTypes, map[string]attr.Value{
		"url":   types.StringUnknown(),
		"alias": types.StringNull(),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}

	for name, tc := range map[string]struct {
		value   types.List
		wantErr bool
	}{
		"small":       {value: small},
		"large":       {value: large, wantErr: true},
		"unknown url": {value: resourceList(t, unknownURL)},
		"null":        {value: types.ListNull(types.ObjectType{AttrTypes: RequiredResourceAttrTypes})},
	} {
		t.Run(name, func(t *testing.T) {
			var resp validator.ListResponse
			requiredResourcesSizeValidator{}.ValidateList(context.Background(), validator.ListRequest{
				Path:        path.Root("required_resources"),
				ConfigValue: tc.value,
			}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func resourceObject(t *testing.T, url string, alias types.String) attr.Value {
	t.Helper()
	value, diags := types.ObjectValue(RequiredResourceAttrTypes, map[string]attr.Value{
		"url":   types.StringValue(url),
		"alias": alias,
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	return value
}

func resourceList(t *testing.T, elements ...attr.Value) types.List {
	t.Helper()
	value, diags := types.ListValue(types.ObjectType{AttrTypes: RequiredResourceAttrTypes}, elements)
	if diags.HasError() {
		t.Fatal(diags)
	}
	return value
}

func marshal(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

type fakeClient struct {
	diveID   string
	request  mdrest.EmbedSessionRequest
	response *mdrest.EmbedSessionResponse
	err      error
}

func (c *fakeClient) CreateDiveEmbedSession(_ context.Context, diveID string, req mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error) {
	c.diveID = diveID
	c.request = req
	return c.response, c.err
}
