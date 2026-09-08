# Bootstrap the writer in a separate admin root before applying this one.
# MOTHERDUCK_TOKEN must be the writer's token. MOTHERDUCK_ADMIN_TOKEN creates readers.
data "motherduck_current_user" "writer" {
  count = var.expected_writer_username == null ? 0 : 1
}

resource "motherduck_database" "tenant" {
  for_each                = var.tenants
  name                    = "${var.name_prefix}_${each.key}"
  snapshot_retention_days = 7

  lifecycle {
    precondition {
      condition     = var.expected_writer_username == null || one(data.motherduck_current_user.writer[*].value) == var.expected_writer_username
      error_message = "Connect as the expected writer before provisioning tenant data."
    }
  }
}

resource "motherduck_schema" "app" {
  for_each = var.tenants
  database = motherduck_database.tenant[each.key].name
  name     = "app"
}

resource "motherduck_table" "daily_usage" {
  for_each = var.tenants
  database = motherduck_database.tenant[each.key].name
  schema   = motherduck_schema.app[each.key].name
  name     = "daily_usage"
  columns = {
    usage_date  = "DATE"
    event_count = "BIGINT"
  }
}

resource "motherduck_service_account" "reader" {
  for_each = var.tenants
  username = "${var.name_prefix}_reader_${each.key}"
}

resource "motherduck_duckling_config" "reader" {
  for_each                      = var.tenants
  username                      = motherduck_service_account.reader[each.key].username
  read_write_instance_size      = "standard"
  read_write_cooldown_seconds   = 60
  read_scaling_instance_size    = "standard"
  read_scaling_flock_size       = 1
  read_scaling_cooldown_seconds = 60
}

# Used only to initialize the reader and attach its share, then allowed to expire.
resource "motherduck_access_token" "reader_setup" {
  for_each   = var.tenants
  username   = motherduck_service_account.reader[each.key].username
  name       = "initial-share-attachment"
  token_type = "read_write"
  ttl        = 3600
}

resource "motherduck_access_token" "reader" {
  for_each   = var.tenants
  username   = motherduck_service_account.reader[each.key].username
  name       = "application-reads"
  token_type = "read_scaling"
  ttl        = 2592000
}

resource "motherduck_share" "tenant" {
  for_each        = var.tenants
  name            = "${var.name_prefix}_share_${each.key}"
  source_database = motherduck_database.tenant[each.key].name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
  depends_on      = [motherduck_table.daily_usage]
}

resource "motherduck_share_grant" "reader" {
  for_each     = var.tenants
  share        = motherduck_share.tenant[each.key].name
  username     = motherduck_service_account.reader[each.key].username
  grantee_type = "user"
}
