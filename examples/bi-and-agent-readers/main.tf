# The provider's SQL token must belong to the writer that owns the marts.
# Only the owner of a share can grant it, and a service-account resource does
# not switch the SQL identity.
data "motherduck_current_user" "writer" {
  count = var.expected_writer_username == null ? 0 : 1
}

resource "motherduck_database" "marts" {
  name = "${var.name_prefix}_marts"

  lifecycle {
    precondition {
      condition     = var.expected_writer_username == null || one(data.motherduck_current_user.writer[*].value) == var.expected_writer_username
      error_message = "The provider SQL identity does not match expected_writer_username. The marts database must be created by the writer so it can grant its share."
    }
  }
}

resource "motherduck_schema" "reporting" {
  database = motherduck_database.marts.name
  name     = "reporting"
}

# Terraform defines the table. The pipeline that owns the marts loads it.
resource "motherduck_table" "daily_revenue" {
  database = motherduck_database.marts.name
  schema   = motherduck_schema.reporting.name
  name     = "daily_revenue"

  columns = {
    order_date  = "DATE"
    order_count = "BIGINT"
    revenue     = "DECIMAL(18,2)"
  }
  column_order = ["order_date", "order_count", "revenue"]
  not_null     = ["order_date"]
}

# One share publishes the curated marts. Every consumer reads the same data
# on its own compute, so the share is the single place to decide what leaves
# the warehouse.
resource "motherduck_share" "marts" {
  name            = "${var.name_prefix}_marts"
  source_database = motherduck_database.marts.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.daily_revenue]
}

# One service account per consumer. Compute is billed and limited per account,
# so each tool or agent gets its own usage line, its own Duckling size, and a
# credential that can be revoked without touching the others.
resource "motherduck_service_account" "consumer" {
  for_each = var.consumers

  username = "${var.name_prefix}_${each.key}"
}

resource "motherduck_duckling_config" "consumer" {
  for_each = var.consumers

  username                      = motherduck_service_account.consumer[each.key].username
  read_write_instance_size      = "standard"
  read_write_cooldown_seconds   = 60
  read_scaling_instance_size    = each.value.instance_size
  read_scaling_flock_size       = each.value.flock_size
  read_scaling_cooldown_seconds = 60
}

# A read-scaling token cannot attach a share. This one-hour read-write token
# exists only to attach the share once as the consumer.
resource "motherduck_access_token" "setup" {
  for_each = var.consumers

  username    = motherduck_service_account.consumer[each.key].username
  name        = "initial-share-attachment"
  description = "One-time share attachment for ${each.value.purpose}"
  token_type  = "read_write"
  ttl         = 3600
}

# The credential the tool or agent holds. MotherDuck rejects writes and
# administrative statements on a read-scaling token, so read-only access does
# not depend on the client behaving.
resource "motherduck_access_token" "reader" {
  for_each = var.consumers

  username    = motherduck_service_account.consumer[each.key].username
  name        = "terraform-reader"
  description = each.value.purpose
  token_type  = "read_scaling"
  ttl         = each.value.token_ttl_seconds
}

resource "motherduck_share_grant" "consumer" {
  for_each = var.consumers

  share    = motherduck_share.marts.name
  username = motherduck_service_account.consumer[each.key].username
}
