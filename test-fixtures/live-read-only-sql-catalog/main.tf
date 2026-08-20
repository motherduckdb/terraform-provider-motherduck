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
