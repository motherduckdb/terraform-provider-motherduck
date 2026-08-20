package diveembed

import (
	"context"
	"errors"
	"testing"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCreateMapsModelAndResponse(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := Model{
		DiveID:      types.StringValue("dive-id"),
		Username:    types.StringValue("service-account"),
		SessionHint: types.StringValue("session-hint"),
		Session:     types.StringNull(),
	}

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if client.diveID != "dive-id" {
		t.Fatalf("dive ID = %q, want %q", client.diveID, "dive-id")
	}
	if client.request.Username != "service-account" {
		t.Fatalf("username = %q, want %q", client.request.Username, "service-account")
	}
	if client.request.SessionHint != "session-hint" {
		t.Fatalf("session hint = %q, want %q", client.request.SessionHint, "session-hint")
	}
	if got := model.Session.ValueString(); got != "session-credential" {
		t.Fatalf("session = %q, want %q", got, "session-credential")
	}
}

func TestCreateOmitsNullSessionHint(t *testing.T) {
	client := &fakeClient{response: &mdrest.EmbedSessionResponse{Session: "session-credential"}}
	model := Model{
		DiveID:      types.StringValue("dive-id"),
		Username:    types.StringValue("service-account"),
		SessionHint: types.StringNull(),
	}

	if err := Create(context.Background(), client, &model); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if client.request.SessionHint != "" {
		t.Fatalf("session hint = %q, want empty", client.request.SessionHint)
	}
}

func TestCreateReturnsClientError(t *testing.T) {
	wantErr := errors.New("create failed")
	client := &fakeClient{err: wantErr}
	model := Model{
		DiveID:      types.StringValue("dive-id"),
		Username:    types.StringValue("service-account"),
		SessionHint: types.StringNull(),
		Session:     types.StringNull(),
	}

	if err := Create(context.Background(), client, &model); !errors.Is(err, wantErr) {
		t.Fatalf("Create() error = %v, want %v", err, wantErr)
	}
	if !model.Session.IsNull() {
		t.Fatalf("session = %#v, want null", model.Session)
	}
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
