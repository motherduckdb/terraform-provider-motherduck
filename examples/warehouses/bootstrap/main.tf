locals {
  identities = toset(["dev_writer", "dev_bi", "prod_writer", "prod_bi"])
}

resource "motherduck_service_account" "environment" {
  for_each = local.identities
  username = "${var.account_prefix}_${each.key}"
}

resource "motherduck_access_token" "environment" {
  for_each   = local.identities
  username   = motherduck_service_account.environment[each.key].username
  name       = "warehouse-example"
  token_type = endswith(each.key, "_bi") ? "read_scaling" : "read_write"
  ttl        = 2592000
}
