package diveembed

import (
	"context"
	"errors"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tokencontext"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ContextDescription documents the context argument of both schemas.
const ContextDescription = "Optional key/value context that the session's token carries. Secure view policies read a value with `session_context('key')`, which returns NULL for a key the context does not set. " +
	tokencontext.Description +
	" The key `name` sets the session name and routes the session's requests, so it conflicts with `session_name` and `session_hint`. Its value must be 1 to 64 characters of letters, digits, `.`, `_` and `-`, starting with a letter or digit."

const sessionNameConflict = "the context key name sets the session name, and MotherDuck ignores session_name and session_hint when it is set. Set the session name in only one place"

// ContextValidators returns the plan-time validators for the context map.
func ContextValidators() []validator.Map {
	return append(tokencontext.Validators(), contextSessionNameValidator{})
}

// applyContext checks the context again at apply time, because values that
// are unknown during planning skip the plan-time validators.
func applyContext(model *Model, req *mdrest.EmbedSessionRequest) error {
	values, err := tokencontext.FromMap(model.Context)
	if err != nil {
		return err
	}
	if tokencontext.HasSessionName(model.Context) && (!model.SessionName.IsNull() || !model.SessionHint.IsNull()) {
		return errors.New(sessionNameConflict)
	}
	req.Context = values
	return nil
}

type contextSessionNameValidator struct{}

func (contextSessionNameValidator) Description(context.Context) string {
	return "the key name conflicts with session_name and session_hint"
}

func (contextSessionNameValidator) MarkdownDescription(context.Context) string {
	return "the key `name` conflicts with `session_name` and `session_hint`"
}

func (contextSessionNameValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if !tokencontext.HasSessionName(req.ConfigValue) {
		return
	}
	for _, attribute := range []string{"session_name", "session_hint"} {
		var value types.String
		diags := req.Config.GetAttribute(ctx, path.Root(attribute), &value)
		if diags.HasError() || value.IsNull() || value.IsUnknown() {
			continue
		}
		resp.Diagnostics.AddAttributeError(req.Path, "Conflicting MotherDuck Dive embed session arguments", "The context key name sets the session name, and MotherDuck ignores session_name and session_hint when it is set. Set the session name in only one place.")
		return
	}
}
