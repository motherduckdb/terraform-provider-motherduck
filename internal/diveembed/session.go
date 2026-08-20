package diveembed

import (
	"context"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type Model struct {
	DiveID      types.String `tfsdk:"dive_id"`
	Username    types.String `tfsdk:"username"`
	SessionHint types.String `tfsdk:"session_hint"`
	Session     types.String `tfsdk:"session"`
}

type Client interface {
	CreateDiveEmbedSession(context.Context, string, mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error)
}

func Create(ctx context.Context, client Client, model *Model) error {
	req := mdrest.EmbedSessionRequest{Username: model.Username.ValueString()}
	if !model.SessionHint.IsNull() {
		req.SessionHint = model.SessionHint.ValueString()
	}
	session, err := client.CreateDiveEmbedSession(ctx, model.DiveID.ValueString(), req)
	if err != nil {
		return err
	}
	model.Session = types.StringValue(session.Session)
	return nil
}

func DiveIDValidators() []validator.String {
	return []validator.String{tfvalidators.UUID()}
}

func UsernameValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck REST username", 1, 255)}
}

func SessionHintValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck Dive embed session hint", 1, 0)}
}
