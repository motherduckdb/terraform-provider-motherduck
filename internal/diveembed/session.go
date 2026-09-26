package diveembed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Size caps enforced by the embed-session endpoint. MotherDuck measures the
// UTF-8 bytes of the JSON encoding of each field.
const (
	RequiredResourcesMaxBytes = 8192
	InitialStateMaxBytes      = 64 * 1024
)

const SessionHintDeprecationMessage = "Use session_name instead. MotherDuck treats session_hint as a deprecated alias for session_name."

type Model struct {
	DiveID            types.String `tfsdk:"dive_id"`
	Username          types.String `tfsdk:"username"`
	SessionName       types.String `tfsdk:"session_name"`
	SessionHint       types.String `tfsdk:"session_hint"`
	Version           types.Int64  `tfsdk:"version"`
	RequiredResources types.List   `tfsdk:"required_resources"`
	InitialState      types.String `tfsdk:"initial_state"`
	Session           types.String `tfsdk:"session"`
}

type RequiredResourceModel struct {
	URL   types.String `tfsdk:"url"`
	Alias types.String `tfsdk:"alias"`
}

// RequiredResourceAttrTypes is the object type of one required_resources
// element, shared by the ephemeral resource and the data source schemas.
var RequiredResourceAttrTypes = map[string]attr.Type{
	"url":   types.StringType,
	"alias": types.StringType,
}

type Client interface {
	CreateDiveEmbedSession(context.Context, string, mdrest.EmbedSessionRequest) (*mdrest.EmbedSessionResponse, error)
}

func Create(ctx context.Context, client Client, model *Model) error {
	req, err := request(ctx, model)
	if err != nil {
		return err
	}
	session, err := client.CreateDiveEmbedSession(ctx, model.DiveID.ValueString(), req)
	if err != nil {
		return err
	}
	model.Session = types.StringValue(session.Session)
	return nil
}

func request(ctx context.Context, model *Model) (mdrest.EmbedSessionRequest, error) {
	req := mdrest.EmbedSessionRequest{Username: model.Username.ValueString()}
	switch {
	case !model.SessionName.IsNull():
		req.SessionName = model.SessionName.ValueString()
	case !model.SessionHint.IsNull():
		req.SessionName = model.SessionHint.ValueString()
	}
	if !model.Version.IsNull() {
		version := model.Version.ValueInt64()
		req.Version = &version
	}
	if !model.RequiredResources.IsNull() {
		resources, err := requiredResources(ctx, model.RequiredResources)
		if err != nil {
			return req, err
		}
		req.RequiredResources = &resources
	}
	if !model.InitialState.IsNull() {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(model.InitialState.ValueString())); err != nil {
			return req, fmt.Errorf("initial_state must be a JSON object: %w", err)
		}
		req.InitialState = json.RawMessage(compact.Bytes())
	}
	return req, nil
}

func requiredResources(ctx context.Context, list types.List) ([]mdrest.EmbedSessionResource, error) {
	var elements []RequiredResourceModel
	if diags := list.ElementsAs(ctx, &elements, false); diags.HasError() {
		return nil, fmt.Errorf("unable to read required_resources: %v", diags)
	}
	resources := make([]mdrest.EmbedSessionResource, 0, len(elements))
	for _, element := range elements {
		resource := mdrest.EmbedSessionResource{URL: element.URL.ValueString()}
		if !element.Alias.IsNull() {
			alias := element.Alias.ValueString()
			resource.Alias = &alias
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func DiveIDValidators() []validator.String {
	return []validator.String{tfvalidators.UUID()}
}

func UsernameValidators() []validator.String {
	return []validator.String{
		tfvalidators.StringLength("MotherDuck REST username", 1, 255),
		tfvalidators.RESTPathSegment("MotherDuck REST username"),
	}
}

func SessionNameValidators() []validator.String {
	return []validator.String{
		tfvalidators.StringLength("MotherDuck Dive embed session name", 1, 0),
		conflictsWithValidator{other: "session_hint"},
	}
}

func SessionHintValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck Dive embed session hint", 1, 0)}
}

func VersionValidators() []validator.Int64 {
	return []validator.Int64{tfvalidators.Int64Min("MotherDuck Dive embed session version", 1)}
}

func RequiredResourceURLValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck Dive embed required resource URL", 1, 0)}
}

func RequiredResourcesValidators() []validator.List {
	return []validator.List{requiredResourcesSizeValidator{}}
}

func InitialStateValidators() []validator.String {
	return []validator.String{initialStateValidator{}}
}

// encodedSize returns the byte length of value encoded the way the MotherDuck
// API measures it: compact JSON with UTF-8 text and no HTML escaping.
func encodedSize(value any) (int, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return 0, err
	}
	return len(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

type conflictsWithValidator struct {
	other string
}

func (v conflictsWithValidator) Description(context.Context) string {
	return "conflicts with " + v.other
}

func (v conflictsWithValidator) MarkdownDescription(context.Context) string {
	return "conflicts with `" + v.other + "`"
}

func (v conflictsWithValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var other types.String
	diags := req.Config.GetAttribute(ctx, path.Root(v.other), &other)
	if diags.HasError() || other.IsNull() || other.IsUnknown() {
		return
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		"Conflicting MotherDuck Dive embed session arguments",
		fmt.Sprintf("Set only one of %s and %s. %s is a deprecated alias for %s.", req.Path, v.other, v.other, req.Path),
	)
}

type requiredResourcesSizeValidator struct{}

func (requiredResourcesSizeValidator) Description(context.Context) string {
	return fmt.Sprintf("must encode to at most %d bytes of JSON", RequiredResourcesMaxBytes)
}

func (v requiredResourcesSizeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (requiredResourcesSizeValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		if element.IsUnknown() {
			return
		}
		object, ok := element.(types.Object)
		if !ok {
			return
		}
		for _, value := range object.Attributes() {
			if value.IsUnknown() {
				return
			}
		}
	}
	resources, err := requiredResources(ctx, req.ConfigValue)
	if err != nil {
		return
	}
	size, err := encodedSize(resources)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Dive embed required resources", err.Error())
		return
	}
	if size > RequiredResourcesMaxBytes {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid MotherDuck Dive embed required resources",
			fmt.Sprintf("required_resources encodes to %d bytes of JSON, which exceeds the MotherDuck limit of %d bytes.", size, RequiredResourcesMaxBytes),
		)
	}
}

type initialStateValidator struct{}

func (initialStateValidator) Description(context.Context) string {
	return fmt.Sprintf("must be a JSON object that encodes to at most %d bytes", InitialStateMaxBytes)
}

func (v initialStateValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (initialStateValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	detail, ok := ValidateInitialStateValue(req.ConfigValue.ValueString())
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Dive embed initial state", detail)
	}
}

// ValidateInitialStateValue reports whether value is a JSON object within the
// MotherDuck size cap. Use jsonencode() to build it in configuration.
func ValidateInitialStateValue(value string) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	decoder.UseNumber()
	var state map[string]any
	if err := decoder.Decode(&state); err != nil || state == nil {
		return "initial_state must be a JSON object, for example jsonencode({ region = \"emea\" }).", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "initial_state must contain exactly one JSON object.", false
	}
	size, err := encodedSize(state)
	if err != nil {
		return "initial_state could not be encoded: " + err.Error(), false
	}
	if size > InitialStateMaxBytes {
		return fmt.Sprintf("initial_state encodes to %d bytes of JSON, which exceeds the MotherDuck limit of %d bytes.", size, InitialStateMaxBytes), false
	}
	return "", true
}
