package datasources

import (
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/guideref"
	"github.com/motherduckdb/terraform-provider-motherduck/internal/sqlbuild"
)

type rowSpec struct {
	name             string
	description      string
	requiredFunction string
	// requiredParameter, when set, must be a named parameter of
	// requiredFunction in the current session.
	requiredParameter string
	attrs             []string
	requiredAttrs     []string
	typedRows         []typedRowAttribute
	// permissionHint, when set, replaces the generic read error for
	// MotherDuck functions that only organization admins can call.
	permissionHint string
	build          func(rowsModel) (string, error)
	postProcess    func([]map[string]any) []map[string]any
}

type typedRowAttribute struct {
	name        string
	source      string
	description string
	sensitive   bool
	deprecation string
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
		{name: "attached_databases", description: "Reads databases attached to the current MotherDuck SQL session. Rows are sorted by alias and name.", requiredFunction: "md_attached_databases", typedRows: []typedRowAttribute{
			{name: "alias", description: "Alias of the attached database in the current session."},
			{name: "is_share", description: "Whether the attached database is a share."},
			{name: "is_readonly", description: "Whether the attached database is read only."},
			{name: "is_error_catalog", description: "Whether MotherDuck could not attach the database and reports it as an error catalog."},
			{name: "error_message", description: "Attach error reported for an error catalog."},
			{name: "is_ducklake", description: "Whether the attached database is a DuckLake database."},
		}, postProcess: sortRowsByKeys(rowSortKey{field: "alias"}, rowSortKey{field: "name"}), build: func(m rowsModel) (string, error) {
			return "SELECT * FROM md_attached_databases()", nil
		}},
		{name: "database_snapshots", description: "Reads MotherDuck database snapshot metadata from public SQL catalog tables.", attrs: []string{"database_name", "limit", "offset"}, typedRows: []typedRowAttribute{
			{name: "database_name", description: "Database that owns the snapshot."},
			{name: "snapshot_id", description: "MotherDuck snapshot ID."},
			{name: "snapshot_name", description: "Snapshot name when the snapshot is named."},
			{name: "created_ts", description: "Snapshot creation timestamp."},
			{name: "database_id", description: "UUID of the database that owns the snapshot."},
			{name: "active_bytes", description: "Bytes referenced by the snapshot."},
			{name: "bytes_written", description: "Bytes written by the change that produced the snapshot."},
			{name: "bytes_deleted", description: "Bytes deleted by the change that produced the snapshot."},
			{name: "user_name", description: "User that produced the snapshot."},
			{name: "user_id", description: "ID of the user that produced the snapshot."},
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
			{name: "source_database_uuid", source: "source_db_uuid", description: "UUID of the database backing the share."},
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
			{name: "source_database", source: "source_db_name", description: "Always null. MotherDuck does not report the source database for received shares. Read it with the `motherduck_share` data source and the share `url`.", deprecation: "MotherDuck does not report the source database for received shares, so this attribute is always null. Use the motherduck_share data source with the share url instead."},
			{name: "owner", description: "Share owner when exposed by MotherDuck."},
			{name: "url", description: "Share URL. This is sensitive because unrestricted share URLs can grant access.", sensitive: true},
			{name: "access", description: "Share access mode."},
			{name: "visibility", description: "Share visibility mode."},
			{name: "update_mode", description: "Share update mode."},
			{name: "created_ts", description: "Share creation timestamp."},
			{name: "updated_ts", description: "Timestamp of the latest share update."},
		}, build: func(m rowsModel) (string, error) {
			query := "SELECT * FROM MD_INFORMATION_SCHEMA.SHARED_WITH_ME"
			if !m.Name.IsNull() {
				query += " WHERE name = " + sqlbuild.StringLiteral(m.Name.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY name", m), nil
		}},
		{name: "share_grants", description: "Lists direct user and role grants and whole-audience access on one MotherDuck share. Does not expand role membership.", requiredFunction: "md_list_share_grantees", attrs: []string{"share_name"}, requiredAttrs: []string{"share_name"}, typedRows: []typedRowAttribute{
			{name: "share_owner", description: "Account that owns the share."},
			{name: "grantee_name", description: "Role name, username, or the ENTIRE_ORGANIZATION or ALL_USERS keyword reported for an organization-wide or public share."},
			{name: "grantee_type", description: "Grantee type: role, user, organization, or domain."},
			{name: "privilege", description: "Granted share privilege."},
			{name: "granted_at", description: "Grant creation timestamp."},
		}, build: func(m rowsModel) (string, error) {
			if m.ShareName.IsNull() {
				return "", fmt.Errorf("share_name is required")
			}
			return "SELECT * FROM MD_LIST_SHARE_GRANTEES(" + sqlbuild.StringLiteral(m.ShareName.ValueString()) + ") ORDER BY grantee_type, grantee_name", nil
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
		{name: "buckets_for_secret", description: "Lists object-storage buckets accessible through a MotherDuck secret. Rows are sorted by bucket name. Set `limit` and `offset` to page through large listings. Paging follows bucket name order, so pages are stable, and MotherDuck stops listing once it has returned `limit` plus `offset` buckets.", requiredFunction: "md_list_buckets_for_secret", attrs: []string{"secret_name", "limit", "offset"}, requiredAttrs: []string{"secret_name"}, typedRows: []typedRowAttribute{
			{name: "name", description: "Bucket name."},
			{name: "creation_date", description: "Bucket creation date reported by the object store."},
			{name: "region", description: "Bucket region."},
			{name: "arn", description: "Bucket ARN."},
		}, postProcess: sortRowsByKeys(rowSortKey{field: "bucket_name"}, rowSortKey{field: "name"}, rowSortKey{field: "bucket"}), build: func(m rowsModel) (string, error) {
			if m.SecretName.IsNull() {
				return "", fmt.Errorf("secret_name is required")
			}
			return appendOrderedRowLimitOffset("SELECT * FROM md_list_buckets_for_secret("+sqlbuild.StringLiteral(m.SecretName.ValueString())+nativeListLimit(m)+")", "ALL", m), nil
		}},
		{name: "files", description: "Lists files and folders for an object-storage path through MotherDuck SQL. Rows are sorted by name. Set `limit` and `offset` to page through large listings. Paging follows object key order, where a folder sorts as its name followed by a slash, so pages are stable. MotherDuck stops listing once it has returned `limit` plus `offset` entries.", requiredFunction: "md_list_files", attrs: []string{"path", "limit", "offset"}, requiredAttrs: []string{"path"}, typedRows: []typedRowAttribute{
			{name: "name", description: "File or folder name relative to `path`."},
			{name: "type", description: "Entry type: `file` or `folder`."},
			{name: "size", description: "File size in bytes. Null for folders."},
			{name: "last_modified", description: "File modification timestamp. Null for folders."},
		}, postProcess: sortRowsByKeys(rowSortKey{field: "path"}, rowSortKey{field: "name"}, rowSortKey{field: "key"}), build: func(m rowsModel) (string, error) {
			if m.Path.IsNull() {
				return "", fmt.Errorf("path is required")
			}
			return appendOrderedRowLimitOffset("SELECT * FROM md_list_files("+sqlbuild.StringLiteral(m.Path.ValueString())+nativeListLimit(m)+")", objectKeyOrder, m), nil
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
			{name: "email", description: "User email when the member is a user. Null for service accounts and role members."},
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
		{name: "dives", description: "Lists MotherDuck Dives available to the current account. Rows in the returned page are sorted by Dive ID.", requiredFunction: "md_list_dives", attrs: []string{"limit", "offset", "include_org_shares"}, typedRows: diveSummaryRows(), postProcess: sortRowsByKeys(rowSortKey{field: "id"}), build: func(m rowsModel) (string, error) {
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
		{name: "dive", description: "Reads metadata for one MotherDuck Dive, including its current version and content.", requiredFunction: "md_get_dive", attrs: []string{"dive_id"}, requiredAttrs: []string{"dive_id"}, typedRows: append(diveSummaryRows(),
			typedRowAttribute{name: "version_id", description: "ID of the current Dive version."},
			typedRowAttribute{name: "version_storage_url", description: "Storage URL of the current Dive version. Sensitive because it locates the Dive content.", sensitive: true},
			typedRowAttribute{name: "version_description", description: "Description of the current Dive version."},
			typedRowAttribute{name: "version_created_at", description: "Creation timestamp of the current Dive version."},
			typedRowAttribute{name: "version_api_version", description: "Dive API version of the current Dive version."},
			typedRowAttribute{name: "version_required_resources", description: "JSON list of resources the current Dive version requires. Sensitive because entries can include share URLs.", sensitive: true},
			typedRowAttribute{name: "content", description: "Dive source code of the current version. Sensitive because it can embed queries and account details.", sensitive: true},
		), build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			return "SELECT * FROM MD_GET_DIVE(id := " + sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID)", nil
		}},
		{name: "dive_versions", description: "Lists versions for one MotherDuck Dive. Set `limit` and `offset` to page through the version history. Rows in the returned page are sorted newest version first.", requiredFunction: "md_list_dive_versions", attrs: []string{"dive_id", "limit", "offset"}, requiredAttrs: []string{"dive_id"}, typedRows: []typedRowAttribute{
			{name: "id", description: "Dive ID."},
			{name: "version", description: "Dive version number."},
			{name: "storage_url", description: "Storage URL of the version. Sensitive because it locates the Dive content.", sensitive: true},
			{name: "description", description: "Version description."},
			{name: "created_at", description: "Version creation timestamp."},
			{name: "api_version", description: "Dive API version of the version."},
			{name: "required_resources", description: "JSON list of resources the version requires. Sensitive because entries can include share URLs.", sensitive: true},
		}, postProcess: sortRowsByKeys(newestVersionFirst()...), build: func(m rowsModel) (string, error) {
			if m.DiveID.IsNull() {
				return "", fmt.Errorf("dive_id is required")
			}
			args := map[string]string{"id": sqlbuild.StringLiteral(m.DiveID.ValueString()) + "::UUID"}
			if !m.Limit.IsNull() {
				args[`"limit"`] = fmt.Sprintf("%d", m.Limit.ValueInt64())
			}
			if !m.Offset.IsNull() {
				args[`"offset"`] = fmt.Sprintf("%d", m.Offset.ValueInt64())
			}
			return "SELECT * FROM MD_LIST_DIVE_VERSIONS" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "flights", description: "Lists MotherDuck Flights available to the current account. Callers with organization-wide Flight visibility can restrict results to their own Flights. MotherDuck returns up to 5,000 Flights when `limit` is omitted. Set `limit` and `offset` to page through larger accounts. Rows in the returned page are sorted by Flight ID.", requiredFunction: "md_list_flights", attrs: []string{"limit", "offset", "owner_only"}, typedRows: flightSummaryRows(), postProcess: sortRowsByKeys(rowSortKey{field: "flight_id"}), build: func(m rowsModel) (string, error) {
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
		{name: "flight", description: "Reads metadata for one MotherDuck Flight.", requiredFunction: "md_get_flight", attrs: []string{"flight_id"}, requiredAttrs: []string{"flight_id"}, typedRows: flightSummaryRows(), build: func(m rowsModel) (string, error) {
			if m.FlightID.IsNull() {
				return "", fmt.Errorf("flight_id is required")
			}
			return "SELECT * FROM MD_GET_FLIGHT(flight_id := " + sqlbuild.StringLiteral(m.FlightID.ValueString()) + "::UUID)", nil
		}},
		{name: "flight_versions", description: "Lists versions for one MotherDuck Flight. MotherDuck returns up to 5,000 versions when `limit` is omitted. Set `limit` and `offset` to page through more. Rows in the returned page are sorted newest version first and include the `instance_type` of each version.", requiredFunction: "md_list_flight_versions", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, typedRows: []typedRowAttribute{
			{name: "version_id", description: "Flight version ID."},
			{name: "flight_id", description: "Flight ID."},
			{name: "flight_version", description: "Flight version number."},
			{name: "created_at", description: "Version creation timestamp."},
			{name: "access_token_name", description: "Name of the access token the version runs with."},
			{name: "flight_secret_names", description: "JSON list of secret names available to the version."},
			{name: "config", description: "JSON object of configuration values for the version. Sensitive because configuration can carry credentials.", sensitive: true},
			{name: "max_runtime_sec", description: "Maximum run time in seconds."},
			{name: "instance_type", description: "Instance type the version runs on."},
		}, postProcess: sortRowsByKeys(newestVersionFirst()...), build: func(m rowsModel) (string, error) {
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
		{name: "flight_runs", description: "Lists runs for one MotherDuck Flight. MotherDuck returns the 50 newest runs when `limit` is omitted, so set `limit` and `offset` to read more. Rows in the returned page are sorted newest run first.", requiredFunction: "md_list_flight_runs", attrs: []string{"flight_id", "limit", "offset"}, requiredAttrs: []string{"flight_id"}, typedRows: []typedRowAttribute{
			{name: "run_id", description: "Flight run ID."},
			{name: "flight_id", description: "Flight ID."},
			{name: "flight_name", description: "Flight name."},
			{name: "flight_version", description: "Flight version the run used."},
			{name: "config", description: "JSON object of configuration values for the run. Sensitive because configuration can carry credentials.", sensitive: true},
			{name: "run_number", description: "Flight run number."},
			{name: "is_scheduled", description: "Whether the run was started by the Flight schedule."},
			{name: "status", description: "Flight run status."},
			{name: "created_at", description: "Run creation timestamp."},
			{name: "started_at", description: "Run start timestamp."},
			{name: "ended_at", description: "Run end timestamp."},
			{name: "scheduled_at", description: "Scheduled run timestamp."},
			{name: "cancelled_at", description: "Run cancellation timestamp."},
			{name: "exit_code", description: "Process exit code of the run."},
		}, postProcess: sortRowsByKeys(rowSortKey{field: "run_number", descending: true}), build: func(m rowsModel) (string, error) {
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
		{name: "flight_logs", description: "Reads line-oriented logs for one MotherDuck Flight run, in ascending line order. Without `limit`, MotherDuck returns only the most recent 1,000 lines. Set `limit` to read a window of lines from the start of the log, add `offset` to skip lines, and set `order = \"desc\"` to count the window from the end of the log instead. MotherDuck applies the window on the server, so large logs are not transferred in full. An `offset` without `limit` skips lines within the most recent 1,000.", requiredFunction: "md_get_flight_logs", attrs: []string{"flight_id", "run_number", "limit", "offset", "order"}, requiredAttrs: []string{"flight_id", "run_number"}, typedRows: []typedRowAttribute{
			{name: "line_number", description: "Absolute zero-based position of the line in the Flight run log."},
			{name: "reported_at", description: "Timestamp reported for the Flight log line."},
			{name: "line", description: "Flight stdout or stderr line.", sensitive: true},
		}, build: flightLogsQuery},
		{name: "guides", description: "Lists MotherDuck Guides visible to the current account, optionally filtered by topic or referenced object. Rows in the returned page are sorted by Guide ID.", requiredFunction: "md_list_guides", postProcess: sortRowsByKeys(rowSortKey{field: "id"}), attrs: []string{"topic", "reference_type", "reference_url", "reference_schema", "reference_table", "reference_column", "reference_view", "reference_macro", "reference_uuid", "limit", "offset"}, typedRows: guideSummaryRows(), build: func(m rowsModel) (string, error) {
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
		{name: "guide", description: "Reads one MotherDuck Guide, including its content and references. Reads the current version unless `version` is set.", requiredFunction: "md_get_guide", attrs: []string{"guide_id", "version"}, requiredAttrs: []string{"guide_id"}, typedRows: append(guideSummaryRows(),
			typedRowAttribute{name: "version", description: "Guide version returned in this row."},
			typedRowAttribute{name: "version_change_comment", description: "Change comment of the returned version."},
			typedRowAttribute{name: "version_external_id", description: "External ID of the returned version."},
			typedRowAttribute{name: "version_created_at", description: "Creation timestamp of the returned version."},
			typedRowAttribute{name: "references", description: "JSON list of objects the Guide references. Sensitive because catalog references can include share URLs.", sensitive: true},
			typedRowAttribute{name: "content", description: "Guide Markdown content of the returned version. Sensitive because it can include account details.", sensitive: true},
		), build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			args := map[string]string{"id": sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID"}
			if !m.Version.IsNull() && !m.Version.IsUnknown() {
				args["version"] = fmt.Sprintf("%d", m.Version.ValueInt64())
			}
			return "SELECT * FROM MD_GET_GUIDE" + sqlbuild.NamedArgs(args), nil
		}},
		{name: "guide_versions", description: "Lists version history for one MotherDuck Guide without returning content. Rows in the returned page are sorted newest version first.", requiredFunction: "md_list_guide_versions", attrs: []string{"guide_id", "limit", "offset"}, requiredAttrs: []string{"guide_id"}, typedRows: []typedRowAttribute{
			{name: "version", description: "Guide version number."},
			{name: "change_comment", description: "Version change comment."},
			{name: "external_id", description: "External ID recorded for the version."},
			{name: "created_at", description: "Version creation timestamp."},
		}, postProcess: sortRowsByKeys(newestVersionFirst()...), build: func(m rowsModel) (string, error) {
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
		// Role-scoped Guide access is not in production. Its planned design adds
		// role_names to MD_SET_GUIDE_ACCESS and access_role_names to Guide rows.
		{name: "guide_grantees", description: "Experimental. Lists the roles that can read one MotherDuck Guide with role-scoped access, read from the Guide's `access_role_names`. Returns no rows for user or organization access. Requires a MotherDuck SQL session whose `MD_SET_GUIDE_ACCESS` accepts `role_names`, which production MotherDuck does not offer yet. Rows are sorted by role name.", requiredFunction: "md_set_guide_access", requiredParameter: "role_names", attrs: []string{"guide_id"}, requiredAttrs: []string{"guide_id"}, typedRows: []typedRowAttribute{
			{name: "grantee_name", description: "Role name."},
			{name: "grantee_type", description: "Grantee type. Always `role`."},
			{name: "privilege", description: "Granted Guide privilege. Always `read`."},
		}, build: func(m rowsModel) (string, error) {
			if m.GuideID.IsNull() {
				return "", fmt.Errorf("guide_id is required")
			}
			return "SELECT grantee_name, 'role' AS grantee_type, 'read' AS privilege FROM (SELECT unnest(access_role_names) AS grantee_name FROM MD_GET_GUIDE(id := " + sqlbuild.StringLiteral(m.GuideID.ValueString()) + "::UUID) WHERE lower(access) = 'role') ORDER BY lower(grantee_name), grantee_name", nil
		}},
		{name: "current_user_tokens", description: "Lists access token metadata for the principal of the current MotherDuck SQL session. Works without organization admin permission. Rows carry no token IDs or token values. Use `motherduck_user_tokens` with an admin token when you need token IDs. Rows are sorted by token name.", requiredFunction: "md_access_tokens", typedRows: []typedRowAttribute{
			{name: "token_name", description: "Access token name."},
			{name: "token_type", description: "Access token type."},
			{name: "created_ts", description: "Token creation timestamp."},
			{name: "expire_at", description: "Token expiration timestamp. Null when the token does not expire."},
		}, postProcess: sortRowsByKeys(rowSortKey{field: "token_name"}, rowSortKey{field: "created_ts"}), build: func(m rowsModel) (string, error) {
			return "SELECT * FROM md_access_tokens()", nil
		}},
		{name: "storage_info", description: "Reads storage usage for the databases in the organization from `MD_INFORMATION_SCHEMA.STORAGE_INFO`, or from `STORAGE_INFO_HISTORY` when `include_history` is true. Requires an organization admin token. Rows are sorted by database name, database ID, and computation time.", attrs: []string{"database_name", "include_history", "limit", "offset"}, permissionHint: "motherduck_storage_info reads organization storage usage, which only organization admins can query. Use a MotherDuck token for an organization admin.", typedRows: []typedRowAttribute{
			{name: "database_name", description: "Database name."},
			{name: "database_id", description: "Database UUID."},
			{name: "created_ts", description: "Database creation timestamp."},
			{name: "deleted_ts", description: "Database deletion timestamp. Null for databases that still exist."},
			{name: "user_name", description: "User that owns the database."},
			{name: "active_bytes", description: "Bytes referenced by the current database state."},
			{name: "historical_bytes", description: "Bytes kept for historical snapshots."},
			{name: "retained_for_clone_bytes", description: "Bytes retained because other databases were cloned from this one."},
			{name: "failsafe_bytes", description: "Bytes kept in failsafe storage."},
			{name: "transient", description: "Whether the database is transient."},
			{name: "historical_snapshot_retention", description: "Historical snapshot retention as server interval text."},
			{name: "computed_ts", description: "Timestamp at which MotherDuck computed the storage figures."},
		}, build: func(m rowsModel) (string, error) {
			table := "MD_INFORMATION_SCHEMA.STORAGE_INFO"
			if !m.IncludeHistory.IsNull() && !m.IncludeHistory.IsUnknown() && m.IncludeHistory.ValueBool() {
				table = "MD_INFORMATION_SCHEMA.STORAGE_INFO_HISTORY"
			}
			query := "SELECT * FROM " + table
			if !m.DatabaseName.IsNull() {
				query += " WHERE database_name = " + sqlbuild.StringLiteral(m.DatabaseName.ValueString())
			}
			return appendRowLimitOffset(query+" ORDER BY database_name, database_id, computed_ts", m), nil
		}},
	}
}

// diveSummaryRows mirrors the columns MD_LIST_DIVES and MD_GET_DIVE share.
func diveSummaryRows() []typedRowAttribute {
	return []typedRowAttribute{
		{name: "id", description: "Dive ID."},
		{name: "title", description: "Dive title."},
		{name: "description", description: "Dive description."},
		{name: "owner_id", description: "Dive owner ID."},
		{name: "owner_name", description: "Dive owner name."},
		{name: "current_version", description: "Current Dive version."},
		{name: "status", description: "Dive status."},
		{name: "status_changed_at", description: "Timestamp of the latest status change."},
		{name: "status_set_by", description: "ID of the user that set the status."},
		{name: "status_applies_to_version", description: "Dive version the status applies to."},
		{name: "created_at", description: "Dive creation timestamp."},
		{name: "updated_at", description: "Dive update timestamp."},
		{name: "required_resources", description: "JSON list of resources the current Dive version requires. Sensitive because entries can include share URLs.", sensitive: true},
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
	reference := guideref.Reference{
		Type: model.ReferenceType, URL: model.ReferenceURL, Schema: model.ReferenceSchema, Table: model.ReferenceTable,
		Column: model.ReferenceColumn, View: model.ReferenceView, Macro: model.ReferenceMacro, UUID: model.ReferenceUUID,
	}
	refType := model.ReferenceType.ValueString()
	switch guideref.Validate(reference) {
	case guideref.Valid:
		return guideref.StructLiteral(reference, types.StringNull()), true, nil
	case guideref.MissingURL:
		return "", false, fmt.Errorf("catalog Guide reference filters require reference_url beginning with the MotherDuck md scheme")
	case guideref.MultipleNarrowings:
		return "", false, fmt.Errorf("catalog Guide reference filters may set at most one of reference_table, reference_view, or reference_macro")
	case guideref.ColumnWithoutTable:
		return "", false, fmt.Errorf("catalog Guide reference filters require reference_table when reference_column is set")
	case guideref.MissingSchema:
		return "", false, fmt.Errorf("catalog Guide reference filters require reference_schema when narrowing to a table, view, or macro")
	case guideref.MissingUUID:
		return "", false, fmt.Errorf("%s Guide reference filters require reference_uuid", refType)
	default:
		return "", false, fmt.Errorf("reference_type must be catalog, dive, flight, or guide")
	}
}

// newestVersionFirst orders version listings by their version number. The
// candidate columns cover the naming used across Dive, Flight, and Guide
// version functions.
func newestVersionFirst() []rowSortKey {
	return []rowSortKey{
		{field: "version_number", descending: true},
		{field: "version", descending: true},
		{field: "flight_version", descending: true},
	}
}

// objectKeyOrder orders MD_LIST_FILES rows the way object stores list keys.
// Folders are listed as their name with the trailing slash removed, so the
// slash is restored for ordering. The order then matches the server listing,
// and a natively limited listing holds exactly the first rows of the full one.
const objectKeyOrder = "CASE WHEN type = 'folder' THEN name || '/' ELSE name END"

// nativeListLimit passes the paging window to listing functions whose `limit`
// argument stops the object-store listing early. The server cannot skip rows,
// so the native limit covers the offset too and SQL paging trims the window.
// Without a limit the whole listing is needed and nothing is pushed down.
func nativeListLimit(model rowsModel) string {
	if model.Limit.IsNull() || model.Limit.IsUnknown() {
		return ""
	}
	window := model.Limit.ValueInt64()
	if !model.Offset.IsNull() && !model.Offset.IsUnknown() {
		if model.Offset.ValueInt64() > math.MaxInt64-window {
			return ""
		}
		window += model.Offset.ValueInt64()
	}
	return fmt.Sprintf(`, "limit" := %d`, window)
}

// appendOrderedRowLimitOffset applies SQL paging to a table function that has
// no paging arguments. Paging needs a total order, so the query is ordered
// only when limit or offset is set, which keeps the unpaged query unchanged.
func appendOrderedRowLimitOffset(query, orderBy string, model rowsModel) string {
	limited := !model.Limit.IsNull() && !model.Limit.IsUnknown()
	offset := !model.Offset.IsNull() && !model.Offset.IsUnknown()
	if !limited && !offset {
		return query
	}
	return appendRowLimitOffset(query+" ORDER BY "+orderBy, model)
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
