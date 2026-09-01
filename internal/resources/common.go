package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	duckdb "github.com/duckdb/duckdb-go/v2"
	resourceTimeouts "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	mdrest "github.com/motherduckdb/terraform-provider-motherduck/internal/client/rest"
	mdsql "github.com/motherduckdb/terraform-provider-motherduck/internal/client/sql"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/providerctx"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/retry"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/tfvalidators"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type baseResource struct {
	provider *providerctx.Context
}

func (r *baseResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	providerData, ok := req.ProviderData.(*providerctx.Context)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerctx.Context, got %T", req.ProviderData))
		return
	}
	r.provider = providerData
}

func (r *baseResource) rest(resp *diag.Diagnostics) *mdrest.Client {
	if r.provider == nil || r.provider.REST == nil || !r.provider.REST.Available() {
		resp.AddError("MotherDuck admin token required", mdrest.ErrMissingAdminToken.Error())
		return nil
	}
	return r.provider.REST
}

func (r *baseResource) sql(ctx context.Context, resp *diag.Diagnostics) providerctx.SQLClient {
	if r.provider == nil {
		resp.AddError("MotherDuck token required", mdsql.ErrMissingToken.Error())
		return nil
	}
	client, err := r.provider.SQLClient(ctx)
	if err != nil || client == nil || !client.Available() {
		if err == nil {
			err = mdsql.ErrMissingToken
		}
		resp.AddError("MotherDuck token required", err.Error())
		return nil
	}
	return client
}

func (r *baseResource) sqlFunctionAvailable(ctx context.Context, client interface {
	Exists(context.Context, string, ...any) (bool, error)
}, diags *diag.Diagnostics, functionName string, resourceName string) bool {
	var available bool
	err := retry.SQL(ctx, func() error {
		var existsErr error
		available, existsErr = client.Exists(ctx, "SELECT count(*) FROM duckdb_functions() WHERE lower(function_name) = lower(?)", functionName)
		return existsErr
	})
	if err != nil {
		diags.AddError("Unable to inspect MotherDuck SQL functions", err.Error())
		return false
	}
	if !available {
		diags.AddError(
			"MotherDuck SQL function unavailable",
			fmt.Sprintf("%s is not exposed by the current MotherDuck SQL session. Confirm the account, region, and client support this feature before using the %s resource.", functionName, resourceName),
		)
		return false
	}
	return true
}

func showRows(ctx context.Context, client interface {
	QueryRowsJSON(context.Context, string, ...any) (string, error)
}, query string) ([]map[string]any, error) {
	var rowsJSON string
	err := retry.SQL(ctx, func() error {
		var queryErr error
		rowsJSON, queryErr = client.QueryRowsJSON(ctx, query)
		return queryErr
	})
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsJSON), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *mdrest.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsEntityNotFound()
	}
	var duckErr *duckdb.Error
	if errors.As(err, &duckErr) && duckErr.Type == duckdb.ErrorTypeCatalog {
		msg := strings.ToLower(duckErr.Error())
		return strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "not found") ||
			strings.Contains(msg, "no database/share named")
	}
	return false
}

func knownInt64(value types.Int64) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func resourceTimeoutsAttribute(ctx context.Context, opts resourceTimeouts.Opts) resourceschema.Attribute {
	attr := resourceTimeouts.Attributes(ctx, opts)
	nested, ok := attr.(resourceschema.SingleNestedAttribute)
	if !ok {
		return attr
	}
	nested.MarkdownDescription = "Optional operation timeouts. Values use Go duration syntax such as `30s`, `10m`, or `1h`."
	return nested
}

func timeoutContext(ctx context.Context, value resourceTimeouts.Value, operation string, defaultTimeout time.Duration, diags *diag.Diagnostics) (context.Context, context.CancelFunc) {
	var (
		duration     time.Duration
		timeoutDiags diag.Diagnostics
	)
	switch operation {
	case "create":
		duration, timeoutDiags = value.Create(ctx, defaultTimeout)
	case "read":
		duration, timeoutDiags = value.Read(ctx, defaultTimeout)
	case "update":
		duration, timeoutDiags = value.Update(ctx, defaultTimeout)
	case "delete":
		duration, timeoutDiags = value.Delete(ctx, defaultTimeout)
	default:
		duration = defaultTimeout
	}
	diags.Append(timeoutDiags...)
	if timeoutDiags.HasError() {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, duration)
}

func stringRequiresReplace() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplace()}
}

func stringRequiresReplaceIfConfigured() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured()}
}

func stringUseStateForUnknown() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
}

func stringOptionalComputedRequiresReplaceIfConfigured() []planmodifier.String {
	return []planmodifier.String{
		stringplanmodifier.UseStateForUnknown(),
		stringplanmodifier.RequiresReplaceIfConfigured(),
	}
}

func int64RequiresReplace() []planmodifier.Int64 {
	return []planmodifier.Int64{int64planmodifier.RequiresReplace()}
}

func int64UseStateForUnknown() []planmodifier.Int64 {
	return []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
}

func boolRequiresReplace() []planmodifier.Bool {
	return []planmodifier.Bool{boolplanmodifier.RequiresReplace()}
}

func boolRequiresReplaceIfConfigured() []planmodifier.Bool {
	return []planmodifier.Bool{boolplanmodifier.RequiresReplaceIfConfigured()}
}

func mapRequiresReplace() []planmodifier.Map {
	return []planmodifier.Map{mapplanmodifier.RequiresReplace()}
}

func sqlIdentifierValidators() []validator.String {
	return []validator.String{sqlIdentifierValidator{}}
}

func databaseTypeValidators() []validator.String {
	return []validator.String{databaseTypeValidator{}}
}

func sqlBareWordValidators() []validator.String {
	return []validator.String{sqlBareWordValidator{}}
}

func shareAccessValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck share access",
		values: []string{"organization", "restricted", "unrestricted"},
	}}
}

func shareVisibilityValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck share visibility",
		values: []string{"hidden", "discoverable"},
	}}
}

func shareUpdateModeValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck share update mode",
		values: []string{"automatic", "manual"},
	}}
}

func serviceAccountUsernameValidators() []validator.String {
	return []validator.String{serviceAccountUsernameValidator{}}
}

func shareGrantPrincipalValidators() []validator.String {
	return []validator.String{shareGrantPrincipalValidator{}}
}

func roleNameValidators() []validator.String {
	return []validator.String{roleNameValidator{}}
}

func roleGranteeTypeValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck role grantee type",
		values: []string{"user", "role"},
	}}
}

func restUsernameValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck REST username", 1, 255)}
}

func accessTokenNameValidators() []validator.String {
	return []validator.String{tfvalidators.StringLength("MotherDuck access token name", 1, 255)}
}

func accessTokenTypeValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck access token type",
		values: []string{"read_write", "read_scaling"},
	}}
}

func accessTokenTTLValidators() []validator.Int64 {
	return []validator.Int64{tfvalidators.Int64Range("MotherDuck access token TTL", 300, 31536000)}
}

func snapshotRetentionValidators() []validator.Int64 {
	return []validator.Int64{tfvalidators.Int64Min("MotherDuck snapshot retention days", 0)}
}

func ducklingInstanceSizeValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck Duckling instance size",
		values: []string{"pulse", "standard", "jumbo", "mega", "giga"},
	}}
}

func uuidValidators() []validator.String {
	return []validator.String{tfvalidators.UUID()}
}

func nonBlankStringValidators(name string) []validator.String {
	return []validator.String{tfvalidators.StringLength(name, 1, 0)}
}

func flightRunWaitStatusValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck Flight run wait status",
		values: []string{"succeeded"},
	}}
}

func diveStatusValidators() []validator.String {
	return []validator.String{stringEnumValidator{
		name:   "MotherDuck Dive status",
		values: []string{"draft", "ready", "endorsed", "archived"},
	}}
}

func validateUUIDString(value, subject string, diags *diag.Diagnostics) bool {
	if detail, ok := tfvalidators.ValidateUUIDValue(value, subject); !ok {
		diags.AddError("Invalid MotherDuck UUID", detail)
		return false
	}
	return true
}

func ducklingCooldownValidators() []validator.Int64 {
	return []validator.Int64{tfvalidators.Int64Range("MotherDuck Duckling cooldown seconds", 60, 86400)}
}

func ducklingFlockSizeValidators() []validator.Float64 {
	return []validator.Float64{float64RangeValidator{name: "MotherDuck read-scaling flock size", min: 0, max: 64}}
}

type sqlIdentifierValidator struct{}

func (sqlIdentifierValidator) Description(context.Context) string {
	return "must be a non-empty SQL identifier without dots or leading/trailing whitespace"
}

func (sqlIdentifierValidator) MarkdownDescription(context.Context) string {
	return "must be a non-empty SQL identifier without dots or leading/trailing whitespace"
}

func (sqlIdentifierValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck SQL identifier", "SQL resource names must not be empty.")
		return
	}
	if value != trimmed {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck SQL identifier", "SQL resource names must not have leading or trailing whitespace.")
		return
	}
	if strings.Contains(value, ".") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck SQL identifier", "SQL resource names must not contain dots because Terraform state and import IDs use dots as separators.")
	}
}

type databaseTypeValidator struct{}

func (databaseTypeValidator) Description(context.Context) string {
	return "must be default or ducklake"
}

func (databaseTypeValidator) MarkdownDescription(context.Context) string {
	return "must be `default` or `ducklake`"
}

func (databaseTypeValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	canonical := strings.ToLower(strings.TrimSpace(value))
	if canonical == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck database type", "Database type must not be empty.")
		return
	}
	if canonical == "transient" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck database type", "Use `transient = true` instead of `database_type = \"transient\"`.")
		return
	}
	if value != canonical {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck database type", "Database type must use lowercase canonical value `default` or `ducklake`.")
		return
	}
	switch canonical {
	case "default", "ducklake":
		return
	default:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck database type", "Database type must be `default` or `ducklake`.")
	}
}

type sqlBareWordValidator struct{}

func (sqlBareWordValidator) Description(context.Context) string {
	return "must be a bare SQL option word"
}

func (sqlBareWordValidator) MarkdownDescription(context.Context) string {
	return "must be a bare SQL option word"
}

func (sqlBareWordValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	canonical := strings.ToLower(strings.TrimSpace(value))
	if !isBareSQLWord(value) || value != canonical {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck SQL option", "Value must be a lowercase bare SQL option word containing only letters, numbers, and underscores, starting with a letter or underscore.")
	}
}

type stringEnumValidator struct {
	name   string
	values []string
}

func (v stringEnumValidator) Description(context.Context) string {
	return "must be one of: " + strings.Join(v.values, ", ")
}

func (v stringEnumValidator) MarkdownDescription(context.Context) string {
	return "must be one of: `" + strings.Join(v.values, "`, `") + "`"
}

func (v stringEnumValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	canonical := strings.ToLower(strings.TrimSpace(value))
	for _, allowed := range v.values {
		if canonical == allowed && value == canonical {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, "Value must be a lowercase canonical value, one of: "+strings.Join(v.values, ", ")+".")
}

type serviceAccountUsernameValidator struct{}

func (serviceAccountUsernameValidator) Description(context.Context) string {
	return "must start with an ASCII letter and contain only ASCII letters, digits, and underscores"
}

func (serviceAccountUsernameValidator) MarkdownDescription(context.Context) string {
	return "must start with an ASCII letter and contain only ASCII letters, digits, and underscores"
}

func (serviceAccountUsernameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	detail, ok := validateServiceAccountUsernameValue(value)
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck service account username", detail)
	}
}

func validateServiceAccountUsernameValue(value string) (string, bool) {
	if value != strings.TrimSpace(value) || value == "" || len([]rune(value)) > 255 {
		return "Username must be between 1 and 255 characters, start with an ASCII letter, and contain only ASCII letters, digits, and underscores.", false
	}
	for i, r := range value {
		if i == 0 {
			if !isASCIILetter(r) {
				return "Username must start with an ASCII letter.", false
			}
			continue
		}
		if !isASCIILetter(r) && !isASCIIDigit(r) && r != '_' {
			return "Username must contain only ASCII letters, digits, and underscores.", false
		}
	}
	return "", true
}

type shareGrantPrincipalValidator struct{}

func (shareGrantPrincipalValidator) Description(context.Context) string {
	return "must be non-blank and must not include leading or trailing whitespace"
}

func (shareGrantPrincipalValidator) MarkdownDescription(context.Context) string {
	return "must be non-blank and must not include leading or trailing whitespace"
}

func (shareGrantPrincipalValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	detail, ok := validateShareGrantPrincipalValue(req.ConfigValue.ValueString())
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck share grant username", detail)
	}
}

func validateShareGrantPrincipalValue(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "Share grant username must be non-blank.", false
	}
	if value != trimmed {
		return "Share grant username must not include leading or trailing whitespace.", false
	}
	return "", true
}

type roleNameValidator struct{}

func (roleNameValidator) Description(context.Context) string {
	return "must be 3 to 255 lowercase characters, start with a letter, contain only letters, digits, hyphens, and underscores, and must not be the reserved role name admin, builder, or explorer"
}

func (roleNameValidator) MarkdownDescription(context.Context) string {
	return "must be 3 to 255 lowercase characters, start with a letter, contain only letters, digits, hyphens, and underscores, and must not be the reserved role name `admin`, `builder`, or `explorer`"
}

func (roleNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if detail, ok := validateRoleNameValue(req.ConfigValue.ValueString()); !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MotherDuck role name", detail)
	}
}

func validateRoleNameValue(value string) (string, bool) {
	runes := []rune(value)
	if value != strings.TrimSpace(value) || len(runes) < 3 || len(runes) > 255 {
		return "Role name must be between 3 and 255 characters with no leading or trailing whitespace.", false
	}
	if value != strings.ToLower(value) {
		return "Role name must use the lowercase canonical form because MotherDuck stores role names in lowercase.", false
	}
	if !unicode.IsLetter(runes[0]) {
		return "Role name must start with a letter.", false
	}
	for _, r := range runes[1:] {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return "Role name must contain only letters, digits, hyphens, and underscores.", false
		}
	}
	switch value {
	case "admin", "builder", "explorer":
		return "Role name must not be one of the reserved MotherDuck role names admin, builder, or explorer.", false
	}
	return "", true
}

func isASCIILetter(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
}

func isASCIIDigit(r rune) bool {
	return '0' <= r && r <= '9'
}

type float64RangeValidator struct {
	name string
	min  float64
	max  float64
}

func (v float64RangeValidator) Description(context.Context) string {
	return fmt.Sprintf("must be between %.0f and %.0f", v.min, v.max)
}

func (v float64RangeValidator) MarkdownDescription(context.Context) string {
	return fmt.Sprintf("must be between `%.0f` and `%.0f`", v.min, v.max)
}

func (v float64RangeValidator) ValidateFloat64(ctx context.Context, req validator.Float64Request, resp *validator.Float64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueFloat64()
	if value < v.min || value > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid "+v.name, fmt.Sprintf("Value must be between %.0f and %.0f.", v.min, v.max))
	}
}

func isBareSQLWord(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		valid := r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9')
		if !valid {
			return false
		}
	}
	return true
}
