package resources

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strings"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlcatalog"
)

type shareSecureViewResource struct{ baseResource }

type shareSecureViewModel struct {
	ID       types.String `tfsdk:"id"`
	Share    types.String `tfsdk:"share"`
	Schema   types.String `tfsdk:"schema"`
	Name     types.String `tfsdk:"name"`
	Replaces types.Bool   `tfsdk:"replaces"`
	Query    types.String `tfsdk:"query"`
	Columns  types.List   `tfsdk:"columns"`
}

// shareSecureView is one row of SHOW SECURE VIEWS ON SHARE.
type shareSecureView struct {
	Schema   string
	Name     string
	SQL      string
	Replaces bool
	Columns  []string
}

func NewShareSecureViewResource() resource.Resource { return &shareSecureViewResource{} }

func (r *shareSecureViewResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_share_secure_view"
}

func (r *shareSecureViewResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages one secure view on a MotherDuck share. A secure view filters rows, hides columns, or masks values for the consumers of the share, and MotherDuck enforces it on its servers. Only the share's owner can manage its secure views.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       identifierID("share", "schema", "name"),
				MarkdownDescription: "Secure view resource ID in `<share>.<schema>.<name>` form.",
			},
			"share": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the share that carries the secure view. The share must be owned by the identity of the SQL token. MotherDuck compares share names case-insensitively, so changing only the letter case updates state in place.",
				PlanModifiers:       identifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"schema": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("main"),
				MarkdownDescription: "Schema of the secure view in the share's source database. Defaults to `main`. It must exist in the source database. Changing only the case of ASCII letters updates state in place.",
				PlanModifiers:       catalogIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name that consumers query. With `replaces = true` it must name an existing table or view in the source database. Otherwise it must not exist there. Changing only the case of ASCII letters updates state in place.",
				PlanModifiers:       catalogIdentifierRequiresReplace(),
				Validators:          sqlIdentifierValidators(),
			},
			"replaces": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the secure view stands in for the table or view of the same name, so consumers query the policy output under the original name. Defaults to `false`, which adds a derived relation instead. Changing it replaces the secure view.",
				PlanModifiers:       boolRequiresReplace(),
			},
			"query": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Single SELECT body of the secure view, without surrounding parentheses. It reads only the share's source database and can call `current_user`, `current_email()`, `session_roles()`, and `is_role_in_session()`. Semicolons are rejected, including inside string literals. Use `chr(59)` for a literal semicolon. Leading and trailing whitespace is ignored when comparing with MotherDuck. Changes update the secure view in place with `ADD OR REPLACE`, so consumers never see the unfiltered table during the update.",
				Validators:          nonBlankStringValidators("query"),
			},
			"columns": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Columns the secure view returns to consumers, as reported by MotherDuck.",
			},
		},
	}
}

func (r *shareSecureViewResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config shareSecureViewModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Query.IsNull() && !config.Query.IsUnknown() {
		validateSecureViewQuery(config.Query.ValueString(), &resp.Diagnostics)
	}
}

// validateSecureViewQuery rejects a body that could close the surrounding
// parentheses and carry a second statement. It runs at plan time on known
// values and again before every write, because duckdb-go executes
// multi-statement strings and a value unknown at plan time would otherwise
// bypass the plan-time check.
func validateSecureViewQuery(query string, diags *diag.Diagnostics) {
	if strings.TrimSpace(query) == "" {
		diags.AddAttributeError(path.Root("query"), "Invalid MotherDuck secure view query", "Secure view queries must not be empty.")
		return
	}
	if strings.Contains(query, ";") {
		diags.AddAttributeError(path.Root("query"), "Invalid MotherDuck secure view query", "Secure view queries must be a single SELECT body and must not contain semicolons.")
	}
}

func (r *shareSecureViewResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan shareSecureViewModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &plan, false, &resp.Diagnostics)
	if plan.ID.IsUnknown() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *shareSecureViewResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareSecureViewModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Schema.IsNull() || state.Schema.IsUnknown() {
		state.Schema = types.StringValue("main")
	}
	view, found, err := readShareSecureView(ctx, client, state.Share.ValueString(), state.Schema.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read MotherDuck secure view", secureViewErrorDetail(err))
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(applyShareSecureView(ctx, &state, view)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *shareSecureViewResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state shareSecureViewModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Query.Equal(state.Query) {
		// Only the letter case of an identifier changed. MotherDuck resolves
		// these names case-insensitively, so record the new spelling only.
		plan.ID = types.StringValue(qualifiedObjectID(plan.Share.ValueString(), plan.Schema.ValueString(), plan.Name.ValueString()))
		plan.Columns = state.Columns
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	r.write(ctx, &plan, true, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *shareSecureViewResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	client := r.sql(ctx, &resp.Diagnostics)
	if client == nil {
		return
	}
	var state shareSecureViewModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Schema.IsNull() || state.Schema.IsUnknown() {
		state.Schema = types.StringValue("main")
	}
	query := "ALTER SHARE " + sqlbuild.QuoteIdentifier(state.Share.ValueString()) + " DROP SECURE VIEW IF EXISTS " + sqlbuild.QuoteQualifiedIdentifier(state.Schema.ValueString(), state.Name.ValueString())
	err := retry.SQL(ctx, func() error { return client.Exec(ctx, query) })
	if err != nil && !isShareMissingOrNotOwned(err, state.Share.ValueString()) {
		resp.Diagnostics.AddError("Unable to drop MotherDuck secure view", secureViewErrorDetail(err))
	}
}

func (r *shareSecureViewResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, ok := splitSQLImportID(req.ID, ".", 3, "`<share>.<schema>.<name>`", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("share"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("schema"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[2])...)
}

// write adds the secure view, or replaces its definition when replace is
// true. Create uses plain ADD so a secure view that Terraform does not manage
// is reported as a conflict instead of being silently overwritten. Update uses
// ADD OR REPLACE, which swaps the definition atomically, so consumers never
// see the source table without a policy.
func (r *shareSecureViewResource) write(ctx context.Context, plan *shareSecureViewModel, replace bool, diags *diag.Diagnostics) {
	client := r.sql(ctx, diags)
	if client == nil {
		return
	}
	validateSecureViewQuery(plan.Query.ValueString(), diags)
	if diags.HasError() {
		return
	}
	share := plan.Share.ValueString()
	schemaName := plan.Schema.ValueString()
	name := plan.Name.ValueString()
	id := qualifiedObjectID(share, schemaName, name)
	if !attachShareSourceDatabase(ctx, client, share, diags) {
		return
	}
	query := shareSecureViewStatement(share, schemaName, name, plan.Replaces.ValueBool(), plan.Query.ValueString(), replace)
	exec := func() error { return client.Exec(ctx, query) }
	var err error
	if replace {
		// ADD OR REPLACE is idempotent, so a transient failure can be retried.
		// A plain ADD is not retried because a repeat would report a conflict.
		err = retry.SQL(ctx, exec)
	} else {
		err = exec()
	}
	if err != nil {
		summary := "Unable to create MotherDuck secure view"
		if replace {
			summary = "Unable to update MotherDuck secure view"
		}
		diags.AddError(summary, createErrorDetail(err, secureViewErrorDetail(err), "secure view", id))
		return
	}
	plan.ID = types.StringValue(id)
	// The secure view exists from here on. A failed read is a warning, not an
	// error. An error would taint the resource, and replacing it would drop
	// the secure view and expose its table until the new one is added. The
	// next refresh fills in the columns.
	if plan.Columns.IsUnknown() {
		plan.Columns = types.ListNull(types.StringType)
	}
	view, found, err := readShareSecureView(ctx, client, share, schemaName, name)
	if err != nil {
		diags.AddWarning("Unable to read MotherDuck secure view", "The secure view was written, but reading it back failed. The next refresh reads it again.\n\n"+secureViewErrorDetail(err))
		return
	}
	if !found {
		diags.AddWarning("Unable to read MotherDuck secure view", "The secure view was written, but SHOW SECURE VIEWS ON SHARE did not list it. The next refresh reads it again.")
		return
	}
	diags.Append(applyShareSecureView(ctx, plan, view)...)
}

// attachShareSourceDatabase attaches the share's source database, which
// MotherDuck reads to validate a secure view body.
func attachShareSourceDatabase(ctx context.Context, client providerctx.SQLClient, share string, diags *diag.Diagnostics) bool {
	owned, err := sqlcatalog.ReadOwnedShare(ctx, client, share)
	if errors.Is(err, stdsql.ErrNoRows) {
		diags.AddError("MotherDuck share not found", fmt.Sprintf("The share %q does not exist or is not owned by the identity of the SQL token. Only the share's owner can manage its secure views. When a `motherduck_share` in the same configuration creates it, reference that resource (for example `motherduck_share.<name>.name`) so that Terraform creates the share first.", share))
		return false
	}
	if err != nil {
		diags.AddError("Unable to read MotherDuck share", err.Error())
		return false
	}
	if !owned.SourceDatabase.Valid || owned.SourceDatabase.String == "" {
		return true
	}
	if err := client.AttachDatabase(ctx, owned.SourceDatabase.String); err != nil {
		diags.AddError("Unable to attach MotherDuck source database", attachErrorDetail(err, owned.SourceDatabase.String))
		return false
	}
	return true
}

// shareSecureViewStatement builds the ALTER SHARE statement. The body goes on
// its own lines so a trailing line comment cannot hide the closing
// parenthesis. MotherDuck stores the body without the surrounding whitespace.
func shareSecureViewStatement(share, schemaName, name string, replaces bool, body string, orReplace bool) string {
	action := "ADD SECURE VIEW "
	if orReplace {
		action = "ADD OR REPLACE SECURE VIEW "
	}
	if replaces {
		action += "REPLACES "
	}
	return "ALTER SHARE " + sqlbuild.QuoteIdentifier(share) + " " + action + sqlbuild.QuoteQualifiedIdentifier(schemaName, name) + " AS (\n" + body + "\n)"
}

// applyShareSecureView copies a listed secure view into the model. The
// configured query spelling is kept when it differs from the stored body only
// by surrounding whitespace, which MotherDuck trims.
func applyShareSecureView(ctx context.Context, model *shareSecureViewModel, view shareSecureView) diag.Diagnostics {
	if model.Query.IsNull() || model.Query.IsUnknown() || strings.TrimSpace(model.Query.ValueString()) != view.SQL {
		model.Query = types.StringValue(view.SQL)
	}
	model.Replaces = types.BoolValue(view.Replaces)
	model.ID = types.StringValue(qualifiedObjectID(model.Share.ValueString(), model.Schema.ValueString(), model.Name.ValueString()))
	columns, diags := types.ListValueFrom(ctx, types.StringType, view.Columns)
	if diags.HasError() {
		model.Columns = types.ListNull(types.StringType)
		return diags
	}
	model.Columns = columns
	return diags
}

// readShareSecureView lists the secure views on a share and returns the one
// with the given schema and name. Names match when they differ only in the
// case of ASCII letters, as in the DuckDB catalog. A share that no longer
// exists, or that the token's identity does not own, has no secure views.
func readShareSecureView(ctx context.Context, client providerctx.SQLClient, share, schemaName, name string) (shareSecureView, bool, error) {
	views, err := listShareSecureViews(ctx, client, share)
	if err != nil {
		if isShareMissingOrNotOwned(err, share) {
			return shareSecureView{}, false, nil
		}
		return shareSecureView{}, false, err
	}
	for _, view := range views {
		if catalogNamesEqual(view.Schema, schemaName) && catalogNamesEqual(view.Name, name) {
			return view, true, nil
		}
	}
	return shareSecureView{}, false, nil
}

func listShareSecureViews(ctx context.Context, client providerctx.SQLClient, share string) ([]shareSecureView, error) {
	rows, err := showRows(ctx, client, "SHOW SECURE VIEWS ON SHARE "+sqlbuild.QuoteIdentifier(share))
	if err != nil {
		return nil, err
	}
	views := make([]shareSecureView, 0, len(rows))
	for _, row := range rows {
		view, err := parseShareSecureViewRow(row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func parseShareSecureViewRow(row map[string]any) (shareSecureView, error) {
	var view shareSecureView
	var ok bool
	if view.Schema, ok = row["schema_name"].(string); !ok {
		return view, fmt.Errorf("SHOW SECURE VIEWS returned a row without a schema_name string: %v", row["schema_name"])
	}
	if view.Name, ok = row["view_name"].(string); !ok {
		return view, fmt.Errorf("SHOW SECURE VIEWS returned a row without a view_name string: %v", row["view_name"])
	}
	if view.SQL, ok = row["sql"].(string); !ok {
		return view, fmt.Errorf("SHOW SECURE VIEWS returned secure view %s.%s without a sql string", view.Schema, view.Name)
	}
	switch replaces := row["replaces_table"].(type) {
	case bool:
		view.Replaces = replaces
	case nil:
	default:
		return view, fmt.Errorf("SHOW SECURE VIEWS returned secure view %s.%s with a non-boolean replaces_table value %v", view.Schema, view.Name, replaces)
	}
	switch columns := row["columns"].(type) {
	case []any:
		view.Columns = make([]string, 0, len(columns))
		for _, column := range columns {
			name, ok := column.(string)
			if !ok {
				return view, fmt.Errorf("SHOW SECURE VIEWS returned secure view %s.%s with a non-string column %v", view.Schema, view.Name, column)
			}
			view.Columns = append(view.Columns, name)
		}
	case nil:
		view.Columns = []string{}
	default:
		return view, fmt.Errorf("SHOW SECURE VIEWS returned secure view %s.%s with a columns value that is not a list", view.Schema, view.Name)
	}
	return view, nil
}

// isShareMissingOrNotOwned reports whether err says that the share does not
// exist for the token's identity. MotherDuck reports a missing share and a
// share owned by someone else with the same message. Secure views can treat
// both as gone because only the owner can read or change them, and a secure
// view left behind on a share still restricts its consumers. Do not reuse this
// for grants, where the same reading would hide access that still exists.
func isShareMissingOrNotOwned(err error, share string) bool {
	var duckErr *duckdb.Error
	if !errors.As(err, &duckErr) || duckErr.Type != duckdb.ErrorTypeCatalog {
		return false
	}
	msg := strings.ToLower(duckErr.Error())
	return strings.Contains(msg, "no share named") && containsIdentifierWord(msg, strings.ToLower(share))
}

// secureViewErrorDetail explains failures that the raw error does not, such
// as a MotherDuck plan or client that does not offer secure views.
func secureViewErrorDetail(err error) string {
	detail := err.Error()
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "syntax error at or near \"secure\"") || strings.Contains(lower, "syntax error at or near \"views\""):
		return detail + "\n\nThis MotherDuck SQL session does not support secure views. Secure views need DuckDB 1.5.6 or later and a Business or Enterprise plan, or an active free trial."
	case strings.Contains(lower, "replaces") && strings.Contains(lower, "no table or view"):
		return detail + "\n\nWith `replaces = true`, the name must match an existing table or view in the share's source database. Create the table first and reference it so Terraform orders the creates."
	}
	return detail
}
