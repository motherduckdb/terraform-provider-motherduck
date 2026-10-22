terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

provider "motherduck" {}

data "motherduck_current_user" "current" {}

data "motherduck_version" "current" {}

data "motherduck_live_duckling_size" "current" {}

data "motherduck_attached_databases" "current" {}

data "motherduck_databases" "current" {}

data "motherduck_owned_shares" "current" {}

data "motherduck_shared_with_me" "current" {}

data "motherduck_shared_with_me" "missing" {
  name = "tf_missing_shared_with_me_filter_smoke"
}

data "motherduck_secrets" "current" {}

data "motherduck_current_user_tokens" "current" {}

# Describe the first owned share by name and the first listed share by URL so
# the share description is checked against both catalogs.
data "motherduck_share" "owned" {
  count = length(data.motherduck_owned_shares.current.rows) > 0 ? 1 : 0
  name  = data.motherduck_owned_shares.current.rows[0].name
}

data "motherduck_share" "listed" {
  count = length(data.motherduck_shared_with_me.current.rows) > 0 ? 1 : 0
  url   = data.motherduck_shared_with_me.current.rows[0].url
}

locals {
  attached_databases = nonsensitive(jsondecode(data.motherduck_attached_databases.current.rows_json))
  databases          = nonsensitive(jsondecode(data.motherduck_databases.current.rows_json))
  owned_shares       = nonsensitive(jsondecode(data.motherduck_owned_shares.current.rows_json))
  shared_with_me     = nonsensitive(jsondecode(data.motherduck_shared_with_me.current.rows_json))
  missing_share      = nonsensitive(jsondecode(data.motherduck_shared_with_me.missing.rows_json))
  secrets            = nonsensitive(jsondecode(data.motherduck_secrets.current.rows_json))
}

output "current_user_present" {
  value = length(data.motherduck_current_user.current.value) > 0
}

output "version_present" {
  value = length(data.motherduck_version.current.value) > 0
}

output "live_duckling_size_present" {
  value = length(data.motherduck_live_duckling_size.current.value) > 0
}

output "attached_databases_read" {
  value = can(length(local.attached_databases))
}

output "databases_read" {
  value = can(length(local.databases))
}

output "owned_shares_read" {
  value = can(length(local.owned_shares))
}

output "shared_with_me_read" {
  value = can(length(local.shared_with_me))
}

output "missing_share_count" {
  value = length(local.missing_share)
}

output "secrets_read" {
  value = can(length(local.secrets))
}

output "current_user_details_present" {
  value = alltrue([
    for value in [
      data.motherduck_current_user.current.user_id,
      data.motherduck_current_user.current.username,
      data.motherduck_current_user.current.org_id,
      data.motherduck_current_user.current.org_type,
      data.motherduck_current_user.current.region,
    ] : value != null && value != ""
  ])
}

output "version_extension_hash_present" {
  value = data.motherduck_version.current.extension_hash != null && data.motherduck_version.current.extension_hash != ""
}

output "current_user_tokens_typed" {
  value = alltrue([for token in data.motherduck_current_user_tokens.current.rows : token.token_name != null && token.created_ts != null])
}

output "shared_with_me_typed" {
  value = alltrue([for share in data.motherduck_shared_with_me.current.rows : share.access != null && share.visibility != null && share.update_mode != null])
}

output "owned_shares_source_uuid_present" {
  value = alltrue([for share in data.motherduck_owned_shares.current.rows : share.source_database_uuid != null])
}

output "owned_share_description_matches" {
  value = length(data.motherduck_share.owned) == 0 ? true : (
    data.motherduck_share.owned[0].source_database == data.motherduck_owned_shares.current.rows[0].source_database &&
    data.motherduck_share.owned[0].source_database_uuid == data.motherduck_owned_shares.current.rows[0].source_database_uuid
  )
}

output "listed_share_source_present" {
  value = length(data.motherduck_share.listed) == 0 ? true : (
    data.motherduck_share.listed[0].source_database != null && data.motherduck_share.listed[0].source_database_uuid != null
  )
}
