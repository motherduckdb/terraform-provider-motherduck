locals {
  identities = toset(["dev_writer", "prod_writer"])
  tokens = {
    dev_writer  = { identity = "dev_writer", type = "read_write" }
    dev_bi      = { identity = "dev_writer", type = "read_scaling" }
    prod_writer = { identity = "prod_writer", type = "read_write" }
    prod_bi     = { identity = "prod_writer", type = "read_scaling" }
  }
}

resource "motherduck_service_account" "environment" {
  for_each = local.identities
  username = "${var.account_prefix}_${each.key}"
}

resource "motherduck_duckling_config" "environment" {
  for_each = local.identities
  username = motherduck_service_account.environment[each.key].username

  read_write_instance_size      = "standard"
  read_write_cooldown_seconds   = 60
  read_scaling_instance_size    = "standard"
  read_scaling_flock_size       = var.read_scaling_flock_size[trimsuffix(each.key, "_writer")]
  read_scaling_cooldown_seconds = 60
}

resource "motherduck_access_token" "environment" {
  for_each   = local.tokens
  username   = motherduck_service_account.environment[each.value.identity].username
  name       = "warehouse-example-${each.key}"
  token_type = each.value.type
  # Omit ttl so tokens never expire, keeping this example free of rotation setup.
  # They remain valid until revoked; protect state and remove them during cleanup.
}
