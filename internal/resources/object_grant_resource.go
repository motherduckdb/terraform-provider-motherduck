package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

// objectGrantKind describes one kind of MotherDuck object whose access is
// granted with GRANT <privilege> ON <keyword> '<id>'.
type objectGrantKind struct {
	// name is the resource type suffix and the lowercase object name.
	name string
	// label is the object name in prose.
	label string
	// keyword is the SQL object keyword.
	keyword string
	// privilege is the only privilege MotherDuck supports on the object.
	privilege string
	// listFunction lists the grants on one object.
	listFunction string
	// effect says what the grant allows, for the resource description.
	effect string
	// granters says which principals MotherDuck lets add a grant.
	granters string
}

// gone reports whether an error means the object no longer exists, or is one
// the current principal cannot manage. The grant statements and grantee
// listings report both as a Catalog Error that starts "Could not find Dive" or
// "Could not find Flight".
func (k objectGrantKind) gone(err error) bool {
	var duckErr *duckdb.Error
	if !errors.As(err, &duckErr) || duckErr.Type != duckdb.ErrorTypeCatalog {
		return false
	}
	return strings.Contains(strings.ToLower(duckErr.Msg), "could not find "+strings.ToLower(k.label))
}

var (
	diveGrantKind = objectGrantKind{
		name:         "dive",
		label:        "Dive",
		keyword:      "DIVE",
		privilege:    "read",
		listFunction: "md_list_dive_grantees",
		effect:       "The grantee can then open the Dive. Reading the data behind it still needs access to the databases and shares the Dive queries.",
		granters:     "The owner of a Dive and organization admins can grant access to it",
	}
	flightGrantKind = objectGrantKind{
		name:         "flight",
		label:        "Flight",
		keyword:      "FLIGHT",
		privilege:    "run",
		listFunction: "md_list_flight_grantees",
		effect:       "The grantee can then start runs of the Flight. Runs use the access token and secrets of the Flight owner, and a run grant does not let the grantee see or edit the Flight.",
		granters:     "Only the owner of a Flight can grant access to it",
	}
)

type objectGrantResource struct {
	baseResource
	kind objectGrantKind
}

// objectGrantModel is the state of one grant. The object ID attribute is named
// after the object kind, so the model is loaded attribute by attribute.
type objectGrantModel struct {
	ObjectID    types.String
	GranteeType types.String
	GranteeName types.String
}

func NewDiveGrantResource() resource.Resource {
	return &objectGrantResource{kind: diveGrantKind}
}

func NewFlightGrantResource() resource.Resource {
	return &objectGrantResource{kind: flightGrantKind}
}

func (r *objectGrantResource) idAttribute() string { return r.kind.name + "_id" }

func (r *objectGrantResource) typeName() string { return "motherduck_" + r.kind.name + "_grant" }

func (r *objectGrantResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind.name + "_grant"
}

func (r *objectGrantResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	kind := r.kind
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: fmt.Sprintf("Grants `%s` on one MotherDuck %s to a user, a role, or the whole organization. %s", kind.privilege, kind.label, kind.effect),
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: fmt.Sprintf("Grant ID in `<%s>/<grantee_type>/<grantee_name>` form, or `<%s>/organization` for the organization grant.", r.idAttribute(), r.idAttribute()),
			},
			r.idAttribute(): schema.StringAttribute{
				Required:            true,
				MarkdownDescription: fmt.Sprintf("ID of the %s to grant `%s` on. Must be a UUID. Changing it replaces the grant.", kind.label, kind.privilege),
				PlanModifiers:       stringRequiresReplace(),
				Validators:          uuidValidators(),
			},
			"grantee_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("user"),
				MarkdownDescription: "Principal type: `user`, `role`, or `organization`. Defaults to `user`. `organization` grants to every member of the organization. Changing it replaces the grant.",
				PlanModifiers:       stringRequiresReplace(),
				Validators:          objectGranteeTypeValidators(),
			},
			"grantee_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "User, service account, or role that receives the grant. Required unless `grantee_type` is `organization`, and must be omitted for the organization grant. Must be non-blank and must not include leading or trailing whitespace. MotherDuck resolves principals case-insensitively, so changing only the letter case updates state in place instead of revoking and granting the same principal.",
				PlanModifiers:       identifierRequiresReplace(),
				Validators:          shareGrantPrincipalValidators(),
			},
		},
	}
}

func objectGranteeTypeValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck grantee type",
		values: []string{"user", "role", "organization"},
	}}
}

func (r *objectGrantResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var granteeType, granteeName types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("grantee_type"), &granteeType)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("grantee_name"), &granteeName)...)
	if resp.Diagnostics.HasError() || granteeType.IsUnknown() || granteeName.IsUnknown() {
		return
	}
	effectiveType := "user"
	if !granteeType.IsNull() {
		effectiveType = granteeType.ValueString()
	}
	if detail := objectGranteeProblem(effectiveType, granteeName); detail != "" {
		resp.Diagnostics.AddAttributeError(path.Root("grantee_name"), "Invalid MotherDuck grantee", detail)
	}
}

// objectGranteeProblem says why a grantee type and name do not fit together,
// or returns an empty string when they do.
func objectGranteeProblem(granteeType string, granteeName types.String) string {
	if granteeType == "organization" {
		if !granteeName.IsNull() {
			return "Omit `grantee_name` when `grantee_type` is `organization`. The organization grant covers every member of the organization."
		}
		return ""
	}
	if granteeName.IsNull() {
		return fmt.Sprintf("Set `grantee_name` to the %s that receives the grant, or set `grantee_type` to `organization` to grant to every member of the organization.", granteeType)
	}
	return ""
}

func (r *objectGrantResource) load(ctx context.Context, source interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, diags *diag.Diagnostics) objectGrantModel {
	var model objectGrantModel
	diags.Append(source.GetAttribute(ctx, path.Root(r.idAttribute()), &model.ObjectID)...)
	diags.Append(source.GetAttribute(ctx, path.Root("grantee_type"), &model.GranteeType)...)
	diags.Append(source.GetAttribute(ctx, path.Root("grantee_name"), &model.GranteeName)...)
	if model.GranteeType.IsNull() || model.GranteeType.IsUnknown() {
		model.GranteeType = types.StringValue("user")
	}
	return model
}

func (r *objectGrantResource) store(ctx context.Context, target interface {
	SetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, model objectGrantModel, diags *diag.Diagnostics) {
	diags.Append(target.SetAttribute(ctx, path.Root("id"), objectGrantID(model))...)
	diags.Append(target.SetAttribute(ctx, path.Root(r.idAttribute()), model.ObjectID)...)
	diags.Append(target.SetAttribute(ctx, path.Root("grantee_type"), model.GranteeType)...)
	diags.Append(target.SetAttribute(ctx, path.Root("grantee_name"), model.GranteeName)...)
}

// validate repeats the plan-time checks, because values that are unknown
// during planning skip the plan-time validators and are spliced into SQL.
func (r *objectGrantResource) validate(model objectGrantModel, diags *diag.Diagnostics) bool {
	if !validateUUIDString(model.ObjectID.ValueString(), r.kind.label+" ID", diags) {
		return false
	}
	granteeType := model.GranteeType.ValueString()
	if granteeType != "user" && granteeType != "role" && granteeType != "organization" {
		diags.AddError("Invalid MotherDuck grantee", "Grantee type must be `user`, `role`, or `organization`.")
		return false
	}
	if detail := objectGranteeProblem(granteeType, model.GranteeName); detail != "" {
		diags.AddError("Invalid MotherDuck grantee", detail)
		return false
	}
	if granteeType != "organization" {
		if detail, ok := validateShareGrantPrincipalValue(model.GranteeName.ValueString()); !ok {
			diags.AddError("Invalid MotherDuck grantee", detail)
			return false
		}
	}
	return true
}

func (r *objectGrantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	plan := r.load(ctx, req.Plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || !r.validate(plan, &resp.Diagnostics) {
		return
	}
	if err := client.Exec(ctx, r.statement("GRANT", "TO", plan)); err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Unable to grant MotherDuck %s access", r.kind.label), r.grantErrorDetail(err))
		return
	}
	r.store(ctx, &resp.State, plan, &resp.Diagnostics)
}

func (r *objectGrantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	state := r.load(ctx, req.State, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || !r.validate(state, &resp.Diagnostics) {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, r.kind.listFunction, r.typeName()) {
		return
	}
	query := "SELECT grantee_name, grantee_type, privilege FROM " + strings.ToUpper(r.kind.listFunction) + "(" + sqlbuild.StringLiteral(state.ObjectID.ValueString()) + "::UUID)"
	rows, err := showRows(ctx, client, query)
	if err != nil {
		// The object was deleted out of band, so its grants are gone too.
		if r.kind.gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(fmt.Sprintf("Unable to read MotherDuck %s grant", r.kind.label), err.Error())
		return
	}
	if !objectGrantListed(rows, state, r.kind.privilege) {
		resp.State.RemoveResource(ctx)
		return
	}
	r.store(ctx, &resp.State, state, &resp.Diagnostics)
}

// objectGrantListed reports whether the grantee listing holds the grant. The
// organization row carries the organization name, so it matches on type alone.
func objectGrantListed(rows []map[string]any, model objectGrantModel, privilege string) bool {
	for _, row := range rows {
		granteeType, _ := row["grantee_type"].(string)
		granteeName, _ := row["grantee_name"].(string)
		rowPrivilege, _ := row["privilege"].(string)
		if !strings.EqualFold(granteeType, model.GranteeType.ValueString()) || !strings.EqualFold(rowPrivilege, privilege) {
			continue
		}
		if strings.EqualFold(granteeType, "organization") || strings.EqualFold(granteeName, model.GranteeName.ValueString()) {
			return true
		}
	}
	return false
}

// Update runs only when the principal changes letter case, because any other
// change replaces the grant. MotherDuck resolves principals case-insensitively,
// so the grant is unchanged and only state is updated.
func (r *objectGrantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan := r.load(ctx, req.Plan, &resp.Diagnostics)
	state := r.load(ctx, req.State, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.ObjectID.ValueString() != state.ObjectID.ValueString() || plan.GranteeType.ValueString() != state.GranteeType.ValueString() || !strings.EqualFold(plan.GranteeName.ValueString(), state.GranteeName.ValueString()) {
		resp.Diagnostics.AddError(fmt.Sprintf("%s grant updates are not supported", r.kind.label), "Replace the grant to change its object or principal.")
		return
	}
	r.store(ctx, &resp.State, plan, &resp.Diagnostics)
}

func (r *objectGrantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	state := r.load(ctx, req.State, &resp.Diagnostics)
	if resp.Diagnostics.HasError() || !r.validate(state, &resp.Diagnostics) {
		return
	}
	query := r.statement("REVOKE", "FROM", state)
	if err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) }); err != nil && !r.kind.gone(err) {
		resp.Diagnostics.AddError(fmt.Sprintf("Unable to revoke MotherDuck %s access", r.kind.label), err.Error())
	}
}

func (r *objectGrantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	usage := fmt.Sprintf("`<%s>/<grantee_type>/<grantee_name>` or `<%s>/organization`", r.idAttribute(), r.idAttribute())
	// A principal name can contain a slash, so only the first two separators split.
	parts := strings.SplitN(req.ID, "/", 3)
	if len(parts) < 2 {
		resp.Diagnostics.AddError("Invalid import ID", "Use "+usage+".")
		return
	}
	model := objectGrantModel{
		ObjectID:    types.StringValue(parts[0]),
		GranteeType: types.StringValue(strings.ToLower(parts[1])),
		GranteeName: types.StringNull(),
	}
	if len(parts) == 3 {
		model.GranteeName = types.StringValue(parts[2])
	}
	var diags diag.Diagnostics
	if !r.validate(model, &diags) {
		for _, d := range diags {
			resp.Diagnostics.AddError("Invalid import ID", d.Detail()+" Use "+usage+".")
		}
		return
	}
	r.store(ctx, &resp.State, model, &resp.Diagnostics)
}

func objectGrantID(model objectGrantModel) types.String {
	id := model.ObjectID.ValueString() + "/" + model.GranteeType.ValueString()
	if model.GranteeType.ValueString() != "organization" {
		id += "/" + model.GranteeName.ValueString()
	}
	return types.StringValue(id)
}

func (r *objectGrantResource) statement(action, preposition string, model objectGrantModel) string {
	grantee := "ORGANIZATION"
	if model.GranteeType.ValueString() != "organization" {
		grantee = strings.ToUpper(model.GranteeType.ValueString()) + " " + sqlbuild.QuoteIdentifier(model.GranteeName.ValueString())
	}
	return fmt.Sprintf("%s %s ON %s %s %s %s", action, strings.ToUpper(r.kind.privilege), r.kind.keyword, sqlbuild.StringLiteral(model.ObjectID.ValueString()), preposition, grantee)
}

// grantErrorDetail adds what to do next to the grant errors users can act on.
func (r *objectGrantResource) grantErrorDetail(err error) string {
	detail := err.Error()
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "unable to find user"):
		return detail + "\n\nUse the MotherDuck user name of a member or service account of the organization, as `motherduck_users` lists it. An email address or a token name is not a user name."
	case strings.Contains(lower, "unable to find role"):
		return detail + "\n\nThe role must exist before it can receive a grant. Reference the `motherduck_role` resource so Terraform creates the role first."
	case r.kind.gone(err):
		return detail + fmt.Sprintf("\n\nMotherDuck reports a missing %s and a %s the current principal cannot manage in the same way. %s, so use a MotherDuck token for such a principal.", r.kind.label, r.kind.label, r.kind.granters)
	case strings.Contains(lower, "can add grants"):
		return detail + "\n\n" + r.kind.granters + ". Use a MotherDuck token for such a principal in the provider `token` argument or MOTHERDUCK_TOKEN."
	case strings.Contains(lower, "syntax error"):
		return detail + fmt.Sprintf("\n\nThe connected MotherDuck client does not support GRANT ON %s. Confirm the account, region, and client support this feature before using the %s resource.", r.kind.keyword, r.typeName())
	}
	return detail
}
