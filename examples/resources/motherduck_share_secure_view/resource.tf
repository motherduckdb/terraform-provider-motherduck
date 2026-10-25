resource "motherduck_database" "usage" {
  name = "usage"
}

resource "motherduck_schema" "app" {
  database = motherduck_database.usage.name
  name     = "app"
}

resource "motherduck_schema" "policy" {
  database = motherduck_database.usage.name
  name     = "policy"
}

resource "motherduck_table" "daily_usage" {
  database = motherduck_database.usage.name
  schema   = motherduck_schema.app.name
  name     = "daily_usage"
  columns = {
    tenant_id     = "VARCHAR"
    usage_date    = "DATE"
    event_count   = "BIGINT"
    contact_email = "VARCHAR"
  }
}

resource "motherduck_table" "tenant_readers" {
  database = motherduck_database.usage.name
  schema   = motherduck_schema.policy.name
  name     = "tenant_readers"
  columns = {
    reader_name = "VARCHAR"
    tenant_id   = "VARCHAR"
  }
}

# An empty include pattern exposes only the secure views. A missing policy
# hides its table instead of exposing it.
resource "motherduck_share" "usage" {
  name            = "usage_share"
  source_database = motherduck_database.usage.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
  include_pattern = []
  depends_on      = [motherduck_table.daily_usage, motherduck_table.tenant_readers]
}

resource "motherduck_share_secure_view" "daily_usage" {
  share    = motherduck_share.usage.name
  schema   = motherduck_table.daily_usage.schema
  name     = motherduck_table.daily_usage.name
  replaces = true
  query    = <<-SQL
    SELECT
      u.tenant_id,
      u.usage_date,
      u.event_count,
      CASE WHEN is_role_in_session('usage_pii_readers')
        THEN u.contact_email
      END AS contact_email
    FROM app.daily_usage AS u
    JOIN policy.tenant_readers AS r ON r.tenant_id = u.tenant_id
    WHERE r.reader_name = current_user
  SQL

  # Replacing the share drops its secure views. Recreate them in the same apply.
  lifecycle {
    replace_triggered_by = [motherduck_share.usage.created_ts]
  }
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.usage.name
  username = var.reader_username

  # Grant access only after the policy exists.
  depends_on = [motherduck_share_secure_view.daily_usage]
}

variable "reader_username" {
  description = "Existing grantable MotherDuck user or service-account principal."
  type        = string
  default     = "svc_reader_acme"
  nullable    = false
}
