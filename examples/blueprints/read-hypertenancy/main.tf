locals {
  tenant_slugs = {
    for tenant_id, tenant in var.tenants :
    tenant_id => replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")
  }
}

# The provider's SQL token must belong to the writer identity. MotherDuck
# databases are writable only by their owner, and only the owner of a share
# can GRANT READ ON SHARE, so tenant databases and shares must be created as
# the writer for the write path and the reader grants to work. Create the
# writer identity first with the writer-bootstrap blueprint.
data "motherduck_current_user" "writer" {
  count = var.expected_writer_username == null ? 0 : 1
}

resource "motherduck_database" "tenant" {
  for_each = var.tenants

  name                    = "${var.database_prefix}_${local.tenant_slugs[each.key]}"
  snapshot_retention_days = each.value.snapshot_retention_days

  lifecycle {
    precondition {
      condition     = var.expected_writer_username == null || one(data.motherduck_current_user.writer[*].value) == var.expected_writer_username
      error_message = "The provider SQL identity does not match expected_writer_username. Tenant databases must be created by the writer service account so it can write to them and grant reader access on their shares."
    }
  }
}

resource "motherduck_schema" "tenant" {
  for_each = var.tenants

  database = motherduck_database.tenant[each.key].name
  name     = "app"
}

resource "motherduck_service_account" "reader" {
  for_each = var.tenants

  username = "${var.reader_prefix}_${local.tenant_slugs[each.key]}"
}

resource "motherduck_access_token" "reader" {
  for_each = var.tenants

  username   = motherduck_service_account.reader[each.key].username
  name       = "terraform-reader"
  token_type = "read_scaling"
  ttl        = var.reader_token_ttl_seconds
}

resource "motherduck_share" "tenant" {
  for_each = var.tenants

  name            = "${var.share_prefix}_${local.tenant_slugs[each.key]}"
  source_database = motherduck_database.tenant[each.key].name
  access          = "restricted"
  visibility      = "hidden"
}

resource "motherduck_share_grant" "reader" {
  for_each = var.tenants

  share    = motherduck_share.tenant[each.key].name
  username = motherduck_service_account.reader[each.key].username
}
