package resources

import (
	"context"
	"errors"
	"slices"
	"strings"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// sensitiveWriteDiagnostic builds the detail text for a failed SQL write whose
// statement embedded sensitive literals, such as share URLs in Dive
// required_resources or Guide references.
//
// DuckDB parser and binder errors echo the offending statement, so structured
// DuckDB errors are reduced to a category and the raw message is dropped. Any
// other error keeps its message with every sensitive value redacted.
func sensitiveWriteDiagnostic(subject string, err error, sensitive []string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "The " + subject + " write was canceled or timed out."
	}
	var duckErr *duckdb.Error
	if errors.As(err, &duckErr) {
		detail := "MotherDuck rejected the " + subject + " write."
		switch duckErr.Type {
		case duckdb.ErrorTypeParser, duckdb.ErrorTypeSyntax:
			detail = "The " + subject + " statement contains invalid SQL syntax. Check quoting in the configured values."
		case duckdb.ErrorTypeBinder, duckdb.ErrorTypeInvalidConfiguration, duckdb.ErrorTypeInvalidInput, duckdb.ErrorTypeConversion:
			detail = "The " + subject + " statement contains invalid or unsupported arguments. Check the configured values and referenced URLs."
		case duckdb.ErrorTypeCatalog, duckdb.ErrorTypeMissingExtension, duckdb.ErrorTypeAutoLoad, duckdb.ErrorTypeNotImplemented:
			detail = "The " + subject + " function or a referenced object is unavailable in this connection."
		case duckdb.ErrorTypePermission:
			detail = "The current SQL identity does not have permission to write this " + subject + "."
		case duckdb.ErrorTypeConnection, duckdb.ErrorTypeNetwork, duckdb.ErrorTypeHTTP, duckdb.ErrorTypeIO:
			detail = "The " + subject + " could not be written because the connection or remote service request failed."
		}
		return detail + " Raw SQL error details are omitted because the statement contains sensitive values."
	}
	return redactSensitiveValues(err.Error(), sensitive)
}

// redactSensitiveValues replaces every sensitive value in message, including
// its SQL string-literal form, with a placeholder.
func redactSensitiveValues(message string, sensitive []string) string {
	// Redact longer values first so a value that is a prefix of another does
	// not split the longer one and leave its tail (for example a query string
	// carrying a token) in the message.
	ordered := slices.DeleteFunc(slices.Clone(sensitive), func(v string) bool { return strings.TrimSpace(v) == "" })
	slices.SortStableFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, value := range ordered {
		message = strings.ReplaceAll(message, sqlbuild.StringLiteral(value), "'[redacted]'")
		if escaped := strings.ReplaceAll(value, "'", "''"); escaped != value {
			message = strings.ReplaceAll(message, escaped, "[redacted]")
		}
		message = strings.ReplaceAll(message, value, "[redacted]")
	}
	return message
}

// diveSensitiveValues returns the share URLs configured in Dive
// required_resources so they can be redacted from write diagnostics.
func diveSensitiveValues(ctx context.Context, list types.List) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var resources []diveRequiredResourceModel
	if list.ElementsAs(ctx, &resources, false).HasError() {
		return nil
	}
	values := make([]string, 0, len(resources))
	for _, resource := range resources {
		if !resource.URL.IsNull() && !resource.URL.IsUnknown() {
			values = append(values, resource.URL.ValueString())
		}
	}
	return values
}

// guideSensitiveValues returns the catalog URLs configured in Guide
// references so they can be redacted from write diagnostics.
func guideSensitiveValues(ctx context.Context, list types.List) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var references []guideReferenceModel
	if list.ElementsAs(ctx, &references, false).HasError() {
		return nil
	}
	values := make([]string, 0, len(references))
	for _, reference := range references {
		if !reference.URL.IsNull() && !reference.URL.IsUnknown() {
			values = append(values, reference.URL.ValueString())
		}
	}
	return values
}
