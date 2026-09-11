package datasources

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type rowSpec struct {
	name             string
	description      string
	requiredFunction string
	attrs            []string
	requiredAttrs    []string
	typedRows        []typedRowAttribute
	build            func(rowsModel) (string, error)
	postProcess      func([]map[string]any) []map[string]any
}

type typedRowAttribute struct {
	name        string
	source      string
	description string
	sensitive   bool
}

func rowSpecs() []rowSpec {
	return []rowSpec{
		{name: "databases", description: "Reads MotherDuck database metadata from MD_INFORMATION_SCHEMA.DATABASES.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Database name."},
			{name: "uuid", description: "Database UUID."},
			{name: "created_ts", description: "Database creation timestamp."},
			{name: "database_type", source: "type", description: "Database type reported by MotherDuck."},
			{name: "transient", description: "Whether the database is transient."},
			{name: "historical_snapshot_retention", description: "Historical snapshot retention reported by MotherDuck."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.DATABASES"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "attached_databases", description: "Reads databases attached to the current MotherDuck SQL session.", requiredFunction: "md_attached_databases", build: func(m rowsModel) (string, error) {
			return "SELECT * FROM md_attached_databases()", nil
		}},
		{name: "database_snapshots", description: "Reads MotherDuck database snapshot metadata from public SQL catalog tables.", attrs: []string{"database_name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "database_name", description: "Database that owns the snapshot."},
			{name: "snapshot_id", description: "MotherDuck snapshot ID."},
			{name: "snapshot_name", description: "Snapshot name when the snapshot is named."},
			{name: "created_ts", description: "Snapshot creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS"
			if !m.DatabaseName.IsNull() {
				query += " WHERE database_name = " + sqlbuild.StringLiteral(m.DatabaseName.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY database_name, created_ts, snapshot_id", m), nil
		}},
		{name: "owned_shares", description: "Reads metadata for shares owned by the current MotherDuck account.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Share name."},
			{name: "source_database", source: "source_db_name", description: "Database backing the share."},
			{name: "access", description: "Share access mode."},
			{name: "visibility", description: "Share visibility mode."},
			{name: "update_mode", source: "update", description: "Share update mode."},
			{name: "include_pattern", description: "Catalog include patterns applied to the share."},
			{name: "url", description: "Share URL. This is sensitive because unrestricted share URLs can grant access.", sensitive: true},
			{name: "created_ts", description: "Share creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.OWNED_SHARES"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "shared_with_me", description: "Reads metadata for shares discoverable by the current MotherDuck account. Hidden shares can be accessed by URL but are not listed.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Share name."},
			{name: "source_database", source: "source_db_name", description: "Database backing the share."},
			{name: "owner", description: "Share owner when exposed by MotherDuck."},
			{name: "url", description: "Share URL. This is sensitive because unrestricted share URLs can grant access.", sensitive: true},
			{name: "created_ts", description: "Share creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.SHARED_WITH_ME"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "secrets", description: "Reads MotherDuck-managed secret metadata without exposing secret values.", attrs: []string{"name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Secret name."},
			{name: "type", description: "Secret type."},
			{name: "provider", description: "Secret provider."},
			{name: "persistent", description: "Whether the secret is persistent."},
			{name: "storage", description: "Secret storage backend."},
			{name: "scope", description: "Secret scope."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT name, type, provider, persistent, storage, scope::VARCHAR FROM duckdb_secrets() WHERE storage = 'motherduck'"
			if !m.Name.IsNull() {
				query += " AND name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "buckets_for_secret", description: "Lists object-storage buckets accessible through a MotherDuck secret.", requiredFunction: "md_list_buckets_for_secret", attrs: []string{"secret_name"}, requiredAttrs: []string{"secret_name"}, build: func(m rowsModel) (string, error) {
			if m.SecretName.IsNull() {
				return "", fmt.Errorf("secret_name is required")
			}
			return "SELECT * FROM md_list_buckets_for_secret(" + sqlbuild.StringLiteral(m.SecretName.ValueString()) + ")", nil
		}},
		{name: "files", description: "Lists files for an object-storage path through MotherDuck SQL.", requiredFunction: "md_list_files", attrs: []string{"path"}, requiredAttrs: []string{"path"}, build: func(m rowsModel) (string, error) {
			if m.Path.IsNull() {
				return "", fmt.Errorf("path is required")
			}
			return "SELECT * FROM md_list_files(" + sqlbuild.StringLiteral(m.Path.ValueString()) + ")", nil
		}},
		{name: "roles", description: "Lists MotherDuck roles visible to the current account.", typedRows: []typedRowAttribute{
			{name: "role_name", description: "Role name."},
			{name: "role_type", description: "Role type."},
			{name: "included_roles", description: "Roles directly included in this role."},
			{name: "created_at", description: "Role creation timestamp."},
		}, postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			return "SHOW ALL ROLES", nil
		}},
		{name: "role_members", description: "Lists users and roles directly granted to one MotherDuck role using SHOW USERS OF ROLE and SHOW ROLES OF ROLE.", attrs: []string{"role_name"}, requiredAttrs: []string{"role_name"}, typedRows: []typedRowAttribute{
			{name: "member_name", description: "User or role principal name."},
			{name: "member_type", description: "Principal type: user or role."},
			{name: "email", description: "User email when the member is a user."},
			{name: "is_service_account", description: "Whether the user member is a service account."},
			{name: "granted_at", description: "Grant creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			if m.RoleName.IsNull() {
				return "", fmt.Errorf("role_name is required")
			}
			role := sqlbuild.QuoteIdentifier(m.RoleName.ValueString())
			return "SHOW USERS OF ROLE " + role, nil
		}},
		{name: "roles_for_user", description: "Lists roles granted to one MotherDuck user, including inherited membership.", attrs: []string{"username"}, requiredAttrs: []string{"username"}, typedRows: roleMembershipRows(), postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			if m.Username.IsNull() {
				return "", fmt.Errorf("username is required")
			}
			return "SHOW ROLES TO USER " + sqlbuild.QuoteIdentifier(m.Username.ValueString()), nil
		}},
		{name: "roles_for_role", description: "Lists roles granted to one MotherDuck role, including inherited membership.", attrs: []string{"role_name"}, requiredAttrs: []string{"role_name"}, typedRows: roleMembershipRows(), postProcess: sortRowsBy("role_name"), build: func(m rowsModel) (string, error) {
			if m.RoleName.IsNull() {
				return "", fmt.Errorf("role_name is required")
			}
			return "SHOW ROLES TO ROLE " + sqlbuild.QuoteIdentifier(m.RoleName.ValueString()), nil
		}},
		{name: "dives", description: "Lists MotherDuck Dives available to the current account.", requiredFunction: "md_list_dives", attrs: []string{"limit", "offset", "include_org_shares"}, build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			if !m.IncludeOrgShares.IsNull() {
				args["include_org_shares"] = sqlbuild.BoolLiteral(m.IncludeOrgShares.ValueBool())
			}
			return "SELECT * FROM MD_LIST_DIVES" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "dive", description: "Reads metadata for one MotherDuck Dive.", requiredFunction: "md_get_dive", attrs: []string{"dive_id"}, requiredAttrs: []string{"dive_id"}, build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			return "SELECT * FROM MD_GET_DIVE(id := " + sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID)", nil
		}},
		{name: "dive_versions", description: "Lists versions for one MotherDuck Dive.", requiredFunction: "md_list_dive_versions", attrs: []string{"dive_id"}, requiredAttrs: []string{"dive_id"}, build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			return "SELECT * FROM MD_LIST_DIVE_VERSIONS(id := " + sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID)", nil
		}},
		{name: "flights", description: "Lists MotherDuck Flights available to the current account. Callers with organization-wide Flight visibility can restrict results to their own Flights.", requiredFunction: "md_list_flights", attrs: []string{"limit", "offset", "owner_only"}, typedRows: flightSummaryRows(), build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			if !m.OwnerOnly.IsNull() {
				args["owner_only"] = fmt.Sprintf("%t", m.OwnerOnly.ValueBool())
			}
			return "SELECT * FROM MD_LIST_FLIGHTS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight", description: "Reads metadata for one MotherDuck Flight.", requiredFunction: "md_get_flight", attrs: []string{"flight_id"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			return "SELECT * FROM MD_GET_FLIGHT(flight_id := " + sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID)", nil
		}},
		{name: "flight_versions", description: "Lists versions for one MotherDuck Flight.", requiredFunction: "md_list_flight_versions", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			args := map[string]string{"flight_id": sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_FLIGHT_VERSIONS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight_runs", description: "Lists runs for one MotherDuck Flight.", requiredFunction: "md_list_flight_runs", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			args := map[string]string{"flight_id": sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"LIMIT"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"OFFSET"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_FLIGHT_RUNS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flight_logs", description: "Reads line-oriented logs for one MotherDuck Flight run, in the order returned by MotherDuck.", requiredFunction: "md_get_flight_logs", attrs: []string{"flight_id", "run_number"}, requiredAttrs: []string{"flight_id", "run_number"}, typedRows: []typedRowAttribute{
			{name: "line_number", description: "Absolute zero-based position of the line in the Flight run log."},
			{name: "reported_at", description: "Timestamp reported for the Flight log line."},
			{name: "line", description: "Flight stdout or stderr line.", sensitive: true},
		}, build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() || m.RunNumber.IsNull() {
				return "", fmt.Errorf("flight_id and run_number are required")
			}
			return fmt.Sprintf("SELECT * FROM MD_GET_FLIGHT_LOGS(flight_id := %s::UUID, run_number := %d)", sqlbuild.StringLiteral(m.FlightID.ValueString()), m.RunNumber.ValueInt64()), nil
		}},
		{name: "guides", description: "Lists MotherDuck Guides visible to the current account, optionally filtered by topic or referenced object.", requiredFunction: "md_list_guides", attrs: []string{"topic", "reference_type", "reference_url", "reference_schema", "reference_table", "reference_column", "reference_view", "reference_macro", "reference_uuid", "limit", "offset"}, typedRows: guideSummaryRows(), build: func(m rowsModel) (string, error) {
			args := map[string]string{}
			if !m.Topic.IsNull() {
				args["topic"] = sqlbuild.StringLiteral(m.Topic.ValueString())
			}
			reference, hasReference, err := guideReferenceFilterArg(m)
			if err != nil {
				return "", err
			}
			if hasReference {
				args["reference"] = reference
			}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_GUIDES" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "guide", description: "Reads one MotherDuck Guide, including its current content and references.", requiredFunction: "md_get_guide", attrs: []string{"guide_id"}, requiredAttrs: []string{"guide_id"}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			return "SELECT * FROM MD_GET_GUIDE(id := " + sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID)", nil
		}},
		{name: "guide_versions", description: "Lists version history for one MotherDuck Guide without returning content.", requiredFunction: "md_list_guide_versions", attrs: []string{"guide_id", "limit", "offset"}, requiredAttrs: []string{"guide_id"}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			args := map[string]string{"id": sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_GUIDE_VERSIONS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "guide_grantees", description: "Lists the direct roles or organization configured to read one MotherDuck Guide.", requiredFunction: "md_list_guide_grantees", attrs: []string{"guide_id"}, requiredAttrs: []string{"guide_id"}, typedRows: []typedRowAttribute{
			{name: "grantee_name", description: "Role or organization name."},
			{name: "grantee_type", description: "Grantee type: role or organization."},
			{name: "privilege", description: "Granted Guide privilege."},
			{name: "granted_at", description: "Grant creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			return "SELECT * FROM MD_LIST_GUIDE_GRANTEES(id := " + sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID) ORDER BY grantee_type, grantee_name", nil
		}},
	}
}

func roleMembershipRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "role_name", description: "Role name."},
		{name: "role_type", description: "Role type."},
		{name: "is_direct", description: "Whether the membership was granted directly."},
		{name: "granted_at", description: "Direct grant creation timestamp when available."},
	}
}

func guideSummaryRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "id", description: "Guide ID."},
		{name: "topic", description: "Optional slash-separated guide topic."},
		{name: "title", description: "Guide title."},
		{name: "description", description: "Guide description."},
		{name: "owner_id", description: "Guide owner ID."},
		{name: "owner_name", description: "Guide owner name."},
		{name: "access", description: "Guide access mode."},
		{name: "current_version", description: "Current guide version."},
		{name: "created_at", description: "Guide creation timestamp."},
		{name: "updated_at", description: "Guide update timestamp."},
	}
}

func flightSummaryRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "flight_id", description: "Flight ID."},
		{name: "flight_name", description: "Flight name."},
		{name: "schedule_cron", description: "Flight cron schedule."},
		{name: "schedule_status", description: "Flight schedule status."},
		{name: "status", description: "Flight status."},
		{name: "current_version", description: "Current Flight version."},
		{name: "created_at", description: "Flight creation timestamp."},
		{name: "updated_at", description: "Flight update timestamp."},
		{name: "owner_name", description: "Flight owner name when exposed to the caller."},
	}
}

func guideReferenceFilterArg(model rowsModel) (string, bool, error) {
	values := []types.String{
		model.ReferenceType, model.ReferenceURL, model.ReferenceSchema, model.ReferenceTable,
		model.ReferenceColumn, model.ReferenceView, model.ReferenceMacro, model.ReferenceUUID,
	}
	hasValue := false
	for _, value := range values {
		if !value.IsNull() && !value.IsUnknown() {
			hasValue = true
			break
		}
	}
	if !hasValue {
		return "", false, nil
	}
	if model.ReferenceType.IsNull() || model.ReferenceType.IsUnknown() {
		return "", false, fmt.Errorf("reference_type is required when filtering Guides by a reference")
	}
	refType := model.ReferenceType.ValueString()
	switch refType {
	case "catalog":
		if model.ReferenceURL.IsNull() || !strings.HasPrefix(model.ReferenceURL.ValueString(), "md:") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_url beginning with the MotherDuck md scheme")
		}
		narrowings := 0
		for _, value := range []types.String{model.ReferenceTable, model.ReferenceView, model.ReferenceMacro} {
			if !value.IsNull() && !value.IsUnknown() && value.ValueString() != "" {
				narrowings++
			}
		}
		if narrowings > 1 {
			return "", false, fmt.Errorf("catalog Guide reference filters may set at most one of reference_table, reference_view, or reference_macro")
		}
		if !model.ReferenceColumn.IsNull() && model.ReferenceColumn.ValueString() != "" &&
			(model.ReferenceTable.IsNull() || model.ReferenceTable.ValueString() == "") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_table when reference_column is set")
		}
		if narrowings > 0 && (model.ReferenceSchema.IsNull() || model.ReferenceSchema.ValueString() == "") {
			return "", false, fmt.Errorf("catalog Guide reference filters require reference_schema when narrowing to a table, view, or macro")
		}
	case "dive", "flight", "guide":
		if model.ReferenceUUID.IsNull() {
			return "", false, fmt.Errorf("%s Guide reference filters require reference_uuid", refType)
		}
	default:
		return "", false, fmt.Errorf("reference_type must be catalog, dive, flight, or guide")
	}
	fields := []string{
		"'type': " + rowReferenceString(model.ReferenceType, "VARCHAR"),
		"'url': " + rowReferenceString(model.ReferenceURL, "VARCHAR"),
		"'schema': " + rowReferenceString(model.ReferenceSchema, "VARCHAR"),
		"'table': " + rowReferenceString(model.ReferenceTable, "VARCHAR"),
		"'column': " + rowReferenceString(model.ReferenceColumn, "VARCHAR"),
		"'view': " + rowReferenceString(model.ReferenceView, "VARCHAR"),
		"'macro': " + rowReferenceString(model.ReferenceMacro, "VARCHAR"),
		"'uuid': " + rowReferenceString(model.ReferenceUUID, "UUID"),
		"'description': NULL::VARCHAR",
	}
	return "{" + strings.Join(fields, ", ") + "}", true, nil
}

func rowReferenceString(value types.String, sqlType string) string {
	if value.IsNull() || value.IsUnknown() {
		return "NULL::" + sqlType
	}
	literal := sqlbuild.StringLiteral(value.ValueString())
	if sqlType == "UUID" {
		return literal + "::UUID"
	}
	return literal
}

func appendRowLimitOffset(query string, model rowsModel) string {
	if !model.Limit.IsNull() && !model.Limit.IsUnknown() {
		query += fmt.Sprintf(" LIMIT %d", model.Limit.ValueInt64())
	}
	if !model.Offset.IsNull() && !model.Offset.IsUnknown() {
		query += fmt.Sprintf(" OFFSET %d", model.Offset.ValueInt64())
	}
	return query
}
