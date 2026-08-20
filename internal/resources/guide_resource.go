package resources

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &guideResource{}
	_ resource.ResourceWithConfigure      = &guideResource{}
	_ resource.ResourceWithImportState    = &guideResource{}
	_ resource.ResourceWithValidateConfig = &guideResource{}
)

const (
	maxGuideContentBytes               = 1024 * 1024
	maxGuideTopicLength                = 1024
	maxGuideTitleLength                = 255
	maxGuideChangeCommentLength        = 1024
	maxGuideExternalIDLength           = 255
	maxGuideReferenceDescriptionLength = 1024
)

var guideTopicSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type guideResource struct{ baseResource }

type guideModel struct {
	ID               types.String `tfsdk:"id"`
	Topic            types.String `tfsdk:"topic"`
	Title            types.String `tfsdk:"title"`
	Description      types.String `tfsdk:"description"`
	Content          types.String `tfsdk:"content"`
	ChangeComment    types.String `tfsdk:"change_comment"`
	ExternalID       types.String `tfsdk:"external_id"`
	References       types.List   `tfsdk:"references"`
	Access           types.String `tfsdk:"access"`
	RoleNames        types.Set    `tfsdk:"role_names"`
	OwnerID          types.String `tfsdk:"owner_id"`
	OwnerName        types.String `tfsdk:"owner_name"`
	CurrentVersion   types.Int64  `tfsdk:"current_version"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	VersionCreatedAt types.String `tfsdk:"version_created_at"`
}

type guideReferenceModel struct {
	Type        types.String `tfsdk:"type"`
	URL         types.String `tfsdk:"url"`
	Schema      types.String `tfsdk:"schema"`
	Table       types.String `tfsdk:"table"`
	Column      types.String `tfsdk:"column"`
	View        types.String `tfsdk:"view"`
	Macro       types.String `tfsdk:"macro"`
	UUID        types.String `tfsdk:"uuid"`
	Description types.String `tfsdk:"description"`
}

func NewGuideResource() resource.Resource { return &guideResource{} }

func (r *guideResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guide"
}

func (r *guideResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages a versioned MotherDuck Guide through the public Guide SQL functions.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Guide ID assigned by MotherDuck.",
			},
			"topic": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional slash-separated grouping topic. Set to an empty string or remove the attribute to clear the topic.",
				Validators:          []validator.String{guideTopicValidator{}},
			},
			"title": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable Guide title.",
				Validators:          []validator.String{guideLengthValidator{name: "MotherDuck Guide title", min: 1, max: maxGuideTitleLength}},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional Guide description. Set to an empty string or remove the attribute to clear it.",
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Guide Markdown content.",
				Validators:          []validator.String{guideContentValidator{}},
			},
			"change_comment": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Audit comment attached to the managed Guide version. Changing it appends a version.",
				Validators:          []validator.String{guideLengthValidator{name: "MotherDuck Guide change comment", max: maxGuideChangeCommentLength}},
			},
			"external_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "External identifier attached to the managed Guide version. Changing it appends a version.",
				Validators:          []validator.String{guideLengthValidator{name: "MotherDuck Guide external ID", max: maxGuideExternalIDLength}},
			},
			"references": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Catalog, Dive, Flight, or Guide references attached to the managed Guide version. A supplied list replaces the previous version's references.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Reference type: `catalog`, `dive`, `flight`, or `guide`.",
							Validators: []validator.String{stringEnumValidator{
								name:   "MotherDuck Guide reference type",
								values: []string{"catalog", "dive", "flight", "guide"},
							}},
						},
						"url": schema.StringAttribute{
							Optional:            true,
							Sensitive:           true,
							MarkdownDescription: "MotherDuck database or share URL for a catalog reference.",
						},
						"schema": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional schema narrowing for a catalog reference.",
						},
						"table": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional table narrowing for a catalog reference.",
						},
						"column": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional column narrowing for a catalog table reference.",
						},
						"view": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional view narrowing for a catalog reference.",
						},
						"macro": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional macro narrowing for a catalog reference.",
						},
						"uuid": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Referenced Dive, Flight, or Guide UUID.",
							Validators:          uuidValidators(),
						},
						"description": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "Optional explanation of why the referenced object is relevant.",
							Validators:          []validator.String{guideLengthValidator{name: "MotherDuck Guide reference description", max: maxGuideReferenceDescriptionLength}},
						},
					},
				},
			},
			"access": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Guide access: `user`, `role`, or `organization`. Organization access requires administrator permission.",
				PlanModifiers:       stringUseStateForUnknown(),
				Validators: []validator.String{stringEnumValidator{
					name:   "MotherDuck Guide access",
					values: []string{"user", "role", "organization"},
				}},
			},
			"role_names": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Roles that can read the Guide when `access = \"role\"`. The configured set replaces the previous role audience.",
			},
			"owner_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Guide owner ID reported by MotherDuck.",
			},
			"owner_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Guide owner name reported by MotherDuck.",
			},
			"current_version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Current Guide version number.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Guide creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Guide update timestamp.",
			},
			"version_created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current managed version creation timestamp.",
			},
		},
	}
}

func (r *guideResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config guideModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateGuideRoleAccess(ctx, config.Access, config.RoleNames, &resp.Diagnostics)
}

func (r *guideResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	for _, fn := range []string{"md_create_guide", "md_get_guide"} {
		if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, fn, "motherduck_guide") {
			return
		}
	}
	var plan guideModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roleAccess := !plan.Access.IsNull() && !plan.Access.IsUnknown() && plan.Access.ValueString() == "role"
	if roleAccess {
		for _, fn := range []string{"md_set_guide_access", "md_list_guide_grantees"} {
			if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, fn, "motherduck_guide") {
				return
			}
		}
	}
	args := map[string]string{
		"title":   sqlbuild.StringLiteral(plan.Title.ValueString()),
		"content": sqlbuild.StringLiteral(plan.Content.ValueString()),
	}
	addConfiguredGuideString(args, "topic", plan.Topic)
	addConfiguredGuideString(args, "description", plan.Description)
	addConfiguredGuideString(args, "change_comment", plan.ChangeComment)
	addConfiguredGuideString(args, "external_id", plan.ExternalID)
	if !plan.Access.IsNull() && !plan.Access.IsUnknown() && !roleAccess {
		args["access"] = sqlbuild.StringLiteral(plan.Access.ValueString())
	}
	if !plan.References.IsNull() {
		references, ok := guideReferencesArg(ctx, plan.References, &resp.Diagnostics)
		if !ok {
			return
		}
		args[`"references"`] = references
	}
	query := "SELECT id::VARCHAR FROM MD_CREATE_GUIDE" + sqlbuild.NamedArgs(args)
	var id string
	if err := retry.SQL(ctx, func() error { return client.QueryRow(ctx, query).Scan(&id) }); err != nil {
		resp.Diagnostics.AddError("Unable to create MotherDuck Guide", err.Error())
		return
	}
	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if roleAccess && !r.setGuideAccess(ctx, client, plan.ID, plan.Access, plan.RoleNames, &resp.Diagnostics) {
		return
	}
	if !r.readGuide(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck Guide", "Guide was created but could not be read through MD_GET_GUIDE.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *guideResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state guideModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.readGuide(ctx, &state, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *guideResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var plan, state guideModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plannedRoleNames := plan.RoleNames
	if plannedRoleNames.IsUnknown() {
		plannedRoleNames = state.RoleNames
	}

	metadataArgs := map[string]string{}
	if !plan.Title.Equal(state.Title) {
		metadataArgs["title"] = sqlbuild.StringLiteral(plan.Title.ValueString())
	}
	addGuideMetadataUpdate(metadataArgs, "topic", plan.Topic, state.Topic)
	addGuideMetadataUpdate(metadataArgs, "description", plan.Description, state.Description)
	if len(metadataArgs) > 0 {
		if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_guide_metadata", "motherduck_guide") {
			return
		}
		metadataArgs["id"] = guideUUIDArg(state.ID)
		if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_UPDATE_GUIDE_METADATA"+sqlbuild.NamedArgs(metadataArgs)); err != nil {
			resp.Diagnostics.AddError("Unable to update MotherDuck Guide metadata", err.Error())
			return
		}
	}

	versionChanged := !plan.Content.Equal(state.Content) ||
		!optionalListValuesEqual(plan.References, state.References) ||
		!plan.ChangeComment.Equal(state.ChangeComment) ||
		!plan.ExternalID.Equal(state.ExternalID)
	if versionChanged {
		if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_update_guide", "motherduck_guide") {
			return
		}
		versionArgs := map[string]string{
			"id":      guideUUIDArg(state.ID),
			"content": sqlbuild.StringLiteral(plan.Content.ValueString()),
		}
		addConfiguredGuideString(versionArgs, "change_comment", plan.ChangeComment)
		addConfiguredGuideString(versionArgs, "external_id", plan.ExternalID)
		if !plan.References.IsNull() {
			references, ok := guideReferencesArg(ctx, plan.References, &resp.Diagnostics)
			if !ok {
				return
			}
			versionArgs[`"references"`] = references
		} else if !state.References.IsNull() {
			versionArgs[`"references"`] = emptyGuideReferencesSQL()
		}
		if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_UPDATE_GUIDE"+sqlbuild.NamedArgs(versionArgs)); err != nil {
			resp.Diagnostics.AddError("Unable to append MotherDuck Guide version", err.Error())
			return
		}
	}

	if (!plan.Access.Equal(state.Access) || !plannedRoleNames.Equal(state.RoleNames)) &&
		!plan.Access.IsNull() && !plan.Access.IsUnknown() &&
		!r.setGuideAccess(ctx, client, state.ID, plan.Access, plannedRoleNames, &resp.Diagnostics) {
		return
	}

	if !r.readGuide(ctx, &plan, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to read MotherDuck Guide", "Guide was updated but could not be read through MD_GET_GUIDE.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *guideResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	if !r.sqlFunctionAvailable(ctx, client, &resp.Diagnostics, "md_delete_guide", "motherduck_guide") {
		return
	}
	var state guideModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := "SELECT * FROM MD_DELETE_GUIDE(id := " + guideUUIDArg(state.ID) + ")"
	if err := retry.SQL(ctx, func() error {
		_, err := client.QueryRowsJSON(ctx, query)
		return err
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete MotherDuck Guide", err.Error())
	}
}

func (r *guideResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importUUIDID(ctx, req.ID, resp)
}

func (r *guideResource) readGuide(ctx context.Context, model *guideModel, diags *diag.Diagnostics) bool {
	client := r.sql(ctx, diags)
	if client == nil {
		return false
	}
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_get_guide", "motherduck_guide") {
		return false
	}
	var topic, title, description, content, changeComment, externalID, access stdsql.NullString
	var ownerID, ownerName, createdAt, updatedAt, versionCreatedAt, referencesJSON stdsql.NullString
	var currentVersion stdsql.NullInt64
	query := `SELECT topic, title, description, content, version_change_comment, version_external_id, access, owner_id::VARCHAR, owner_name, current_version, created_at::VARCHAR, updated_at::VARCHAR, version_created_at::VARCHAR, to_json("references")::VARCHAR FROM MD_GET_GUIDE(id := ` + guideUUIDArg(model.ID) + ")"
	err := retry.SQL(ctx, func() error {
		return client.QueryRow(ctx, query).Scan(
			&topic, &title, &description, &content, &changeComment, &externalID, &access,
			&ownerID, &ownerName, &currentVersion, &createdAt, &updatedAt, &versionCreatedAt, &referencesJSON,
		)
	})
	if err == stdsql.ErrNoRows || isNotFound(err) {
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck Guide", err.Error())
		return false
	}
	model.Topic = nullString(topic)
	model.Title = nullString(title)
	model.Description = nullString(description)
	model.Content = nullString(content)
	model.ChangeComment = nullString(changeComment)
	model.ExternalID = nullString(externalID)
	model.Access = lowerNullString(access)
	model.OwnerID = nullString(ownerID)
	model.OwnerName = nullString(ownerName)
	if currentVersion.Valid {
		model.CurrentVersion = types.Int64Value(currentVersion.Int64)
	} else {
		model.CurrentVersion = types.Int64Null()
	}
	model.CreatedAt = nullString(createdAt)
	model.UpdatedAt = nullString(updatedAt)
	model.VersionCreatedAt = nullString(versionCreatedAt)
	model.References = guideReferencesFromJSON(ctx, model.References, referencesJSON, diags)
	if model.Access.ValueString() == "role" {
		model.RoleNames = r.readGuideRoleNames(ctx, client, model.ID, diags)
	} else {
		model.RoleNames = types.SetNull(types.StringType)
	}
	return !diags.HasError()
}

func (r *guideResource) setGuideAccess(
	ctx context.Context,
	client *mdsql.Client,
	id, access types.String,
	roleNames types.Set,
	diags *diag.Diagnostics,
) bool {
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_set_guide_access", "motherduck_guide") {
		return false
	}
	args := map[string]string{
		"id":     guideUUIDArg(id),
		"access": sqlbuild.StringLiteral(access.ValueString()),
	}
	if access.ValueString() == "role" {
		names, ok := guideRoleNamesArg(ctx, roleNames, diags)
		if !ok {
			return false
		}
		args["role_names"] = names
	}
	if _, err := client.QueryRowsJSON(ctx, "SELECT * FROM MD_SET_GUIDE_ACCESS"+sqlbuild.NamedArgs(args)); err != nil {
		diags.AddError("Unable to update MotherDuck Guide access", err.Error())
		return false
	}
	return true
}

func (r *guideResource) readGuideRoleNames(
	ctx context.Context,
	client *mdsql.Client,
	id types.String,
	diags *diag.Diagnostics,
) types.Set {
	if !r.sqlFunctionAvailable(ctx, client, diags, "md_list_guide_grantees", "motherduck_guide") {
		return types.SetNull(types.StringType)
	}
	var raw stdsql.NullString
	query := `SELECT to_json(list(grantee_name ORDER BY lower(grantee_name)))::VARCHAR
		FROM MD_LIST_GUIDE_GRANTEES(id := ` + guideUUIDArg(id) + `)
		WHERE lower(grantee_type) = 'role' AND lower(privilege) = 'read'`
	if err := retry.SQL(ctx, func() error { return client.QueryRow(ctx, query).Scan(&raw) }); err != nil {
		diags.AddError("Unable to read MotherDuck Guide role audience", err.Error())
		return types.SetNull(types.StringType)
	}
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return types.SetValueMust(types.StringType, nil)
	}
	var names []string
	if err := json.Unmarshal([]byte(raw.String), &names); err != nil {
		diags.AddError("Unable to parse MotherDuck Guide role audience", err.Error())
		return types.SetNull(types.StringType)
	}
	value, valueDiags := types.SetValueFrom(ctx, types.StringType, names)
	diags.Append(valueDiags...)
	return value
}

func validateGuideRoleAccess(ctx context.Context, access types.String, roleNames types.Set, diags *diag.Diagnostics) {
	if access.IsUnknown() || roleNames.IsUnknown() {
		return
	}
	if access.IsNull() || access.ValueString() != "role" {
		if !roleNames.IsNull() {
			diags.AddAttributeError(
				path.Root("role_names"),
				"Invalid MotherDuck Guide role audience",
				"`role_names` is only valid when `access = \"role\"`.",
			)
		}
		return
	}
	if roleNames.IsNull() || len(roleNames.Elements()) == 0 {
		diags.AddAttributeError(
			path.Root("role_names"),
			"Invalid MotherDuck Guide role audience",
			"`role_names` must contain at least one role when `access = \"role\"`.",
		)
		return
	}
	for _, element := range roleNames.Elements() {
		roleName, ok := element.(types.String)
		if !ok || roleName.IsUnknown() {
			return
		}
	}
	var names []string
	diags.Append(roleNames.ElementsAs(ctx, &names, false)...)
	if diags.HasError() {
		return
	}
	for _, name := range names {
		if detail, ok := validateRoleNameValue(name); !ok {
			diags.AddAttributeError(path.Root("role_names"), "Invalid MotherDuck Guide role name", detail)
		}
	}
}

func guideRoleNamesArg(ctx context.Context, value types.Set, diags *diag.Diagnostics) (string, bool) {
	if value.IsNull() || value.IsUnknown() {
		diags.AddError("Invalid MotherDuck Guide role audience", "Role-scoped Guide access requires at least one configured role.")
		return "", false
	}
	var names []string
	diags.Append(value.ElementsAs(ctx, &names, false)...)
	if diags.HasError() {
		return "", false
	}
	if len(names) == 0 {
		diags.AddError("Invalid MotherDuck Guide role audience", "Role-scoped Guide access requires at least one configured role.")
		return "", false
	}
	return sqlbuild.ListLiteral(names), true
}

type guideLengthValidator struct {
	name string
	min  int
	max  int
}

func (v guideLengthValidator) Description(context.Context) string {
	if v.min > 0 {
		return fmt.Sprintf("must be between %d and %d characters", v.min, v.max)
	}
	return fmt.Sprintf("must be at most %d characters", v.max)
}

func (v guideLengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v guideLengthValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	length := utf8.RuneCountInString(req.ConfigValue.ValueString())
	if length < v.min || length > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, v.Description(ctx)+".")
	}
}

type guideContentValidator struct{}

func (guideContentValidator) Description(context.Context) string {
	return "must be non-empty and at most 1048576 bytes"
}

func (guideContentValidator) MarkdownDescription(ctx context.Context) string {
	return guideContentValidator{}.Description(ctx)
}

func (guideContentValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	length := len(req.ConfigValue.ValueString())
	if length == 0 || length > maxGuideContentBytes {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Guide content", guideContentValidator{}.Description(ctx)+".")
	}
}

type guideTopicValidator struct{}

func (guideTopicValidator) Description(context.Context) string {
	return "must be empty or a slash-separated topic of letters, digits, dots, underscores, and hyphens with no empty, dot, or dot-dot segments"
}

func (guideTopicValidator) MarkdownDescription(ctx context.Context) string {
	return guideTopicValidator{}.Description(ctx)
}

func (guideTopicValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	topic := req.ConfigValue.ValueString()
	if topic == "" {
		return
	}
	if len(topic) > maxGuideTopicLength {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Guide topic", fmt.Sprintf("Guide topic must be at most %d characters.", maxGuideTopicLength))
		return
	}
	for _, segment := range strings.Split(topic, "/") {
		if segment == "" || segment == "." || segment == ".." || !guideTopicSegmentPattern.MatchString(segment) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck Guide topic", guideTopicValidator{}.Description(ctx)+".")
			return
		}
	}
}

func addConfiguredGuideString(args map[string]string, name string, value types.String) {
	if !value.IsNull() && !value.IsUnknown() {
		args[name] = sqlbuild.StringLiteral(value.ValueString())
	}
}

func addGuideMetadataUpdate(args map[string]string, name string, plan, state types.String) {
	if plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		args[name] = "''"
		return
	}
	args[name] = sqlbuild.StringLiteral(plan.ValueString())
}

func guideUUIDArg(value types.String) string {
	return sqlbuild.StringLiteral(value.ValueString()) + "::UUID"
}

func guideReferenceAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"type": types.StringType, "url": types.StringType, "schema": types.StringType,
		"table": types.StringType, "column": types.StringType, "view": types.StringType,
		"macro": types.StringType, "uuid": types.StringType, "description": types.StringType,
	}
}

func guideReferencesArg(ctx context.Context, value types.List, diags *diag.Diagnostics) (string, bool) {
	var references []guideReferenceModel
	diags.Append(value.ElementsAs(ctx, &references, false)...)
	if diags.HasError() {
		return "", false
	}
	if len(references) == 0 {
		return emptyGuideReferencesSQL(), true
	}
	parts := make([]string, 0, len(references))
	for i, reference := range references {
		if !validateGuideReference(reference, i, diags) {
			continue
		}
		fields := []string{
			"'type': " + guideReferenceString(reference.Type, "VARCHAR"),
			"'url': " + guideReferenceString(reference.URL, "VARCHAR"),
			"'schema': " + guideReferenceString(reference.Schema, "VARCHAR"),
			"'table': " + guideReferenceString(reference.Table, "VARCHAR"),
			"'column': " + guideReferenceString(reference.Column, "VARCHAR"),
			"'view': " + guideReferenceString(reference.View, "VARCHAR"),
			"'macro': " + guideReferenceString(reference.Macro, "VARCHAR"),
			"'uuid': " + guideReferenceString(reference.UUID, "UUID"),
			"'description': " + guideReferenceString(reference.Description, "VARCHAR"),
		}
		parts = append(parts, "{"+strings.Join(fields, ", ")+"}")
	}
	if diags.HasError() {
		return "", false
	}
	return "[" + strings.Join(parts, ", ") + "]", true
}

func guideReferenceString(value types.String, sqlType string) string {
	if value.IsNull() || value.IsUnknown() {
		return "NULL::" + sqlType
	}
	literal := sqlbuild.StringLiteral(value.ValueString())
	if sqlType == "UUID" {
		return literal + "::UUID"
	}
	return literal
}

func validateGuideReference(reference guideReferenceModel, index int, diags *diag.Diagnostics) bool {
	refType := reference.Type.ValueString()
	prefix := fmt.Sprintf("Guide reference %d", index)
	if refType == "catalog" {
		if reference.URL.IsNull() || !strings.HasPrefix(reference.URL.ValueString(), "md:") {
			diags.AddError("Invalid MotherDuck Guide reference", prefix+" is a catalog reference and requires a `url` beginning with `md:`.")
			return false
		}
		narrowings := 0
		for _, value := range []types.String{reference.Table, reference.View, reference.Macro} {
			if !value.IsNull() && value.ValueString() != "" {
				narrowings++
			}
		}
		if narrowings > 1 {
			diags.AddError("Invalid MotherDuck Guide reference", prefix+" may set at most one of `table`, `view`, or `macro`.")
			return false
		}
		if !reference.Column.IsNull() && reference.Table.IsNull() {
			diags.AddError("Invalid MotherDuck Guide reference", prefix+" sets `column` without `table`.")
			return false
		}
		if narrowings > 0 && reference.Schema.IsNull() {
			diags.AddError("Invalid MotherDuck Guide reference", prefix+" requires `schema` when narrowing to a table, view, or macro.")
			return false
		}
		return true
	}
	if refType == "dive" || refType == "flight" || refType == "guide" {
		if reference.UUID.IsNull() {
			diags.AddError("Invalid MotherDuck Guide reference", prefix+" is a "+refType+" reference and requires `uuid`.")
			return false
		}
		return true
	}
	diags.AddError("Invalid MotherDuck Guide reference", prefix+" has unsupported type "+refType+".")
	return false
}

func emptyGuideReferencesSQL() string {
	return `[]::STRUCT("type" VARCHAR, url VARCHAR, "schema" VARCHAR, "table" VARCHAR, "column" VARCHAR, "view" VARCHAR, macro VARCHAR, uuid UUID, description VARCHAR)[]`
}

type guideReferenceJSON struct {
	Type        string  `json:"type"`
	URL         *string `json:"url"`
	Schema      *string `json:"schema"`
	Table       *string `json:"table"`
	Column      *string `json:"column"`
	View        *string `json:"view"`
	Macro       *string `json:"macro"`
	GuideID     *string `json:"guide_id"`
	DiveID      *string `json:"dive_id"`
	FlightID    *string `json:"flight_id"`
	Description *string `json:"description"`
}

func guideReferencesFromJSON(ctx context.Context, current types.List, raw stdsql.NullString, diags *diag.Diagnostics) types.List {
	var references []guideReferenceJSON
	if raw.Valid && raw.String != "" && raw.String != "null" {
		if err := json.Unmarshal([]byte(raw.String), &references); err != nil {
			diags.AddError("Unable to parse MotherDuck Guide references", err.Error())
			return current
		}
	}
	if len(references) == 0 && current.IsNull() {
		return types.ListNull(types.ObjectType{AttrTypes: guideReferenceAttrTypes()})
	}
	models := make([]guideReferenceModel, 0, len(references))
	for _, reference := range references {
		uuid := reference.GuideID
		switch reference.Type {
		case "dive":
			uuid = reference.DiveID
		case "flight":
			uuid = reference.FlightID
		}
		models = append(models, guideReferenceModel{
			Type:        types.StringValue(reference.Type),
			URL:         nullableGuideString(reference.URL),
			Schema:      nullableGuideString(reference.Schema),
			Table:       nullableGuideString(reference.Table),
			Column:      nullableGuideString(reference.Column),
			View:        nullableGuideString(reference.View),
			Macro:       nullableGuideString(reference.Macro),
			UUID:        nullableGuideString(uuid),
			Description: nullableGuideString(reference.Description),
		})
	}
	value, valueDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: guideReferenceAttrTypes()}, models)
	diags.Append(valueDiags...)
	if valueDiags.HasError() {
		return current
	}
	return value
}

func nullableGuideString(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}
