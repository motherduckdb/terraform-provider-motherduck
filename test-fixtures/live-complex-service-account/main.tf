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

variable "run_id" {
  type = string
}

locals {
  suffix          = replace(var.run_id, "-", "_")
  database_name   = "tf_complex_${local.suffix}"
  schema_name     = "app"
  writer_username = "tf_complex_writer_${local.suffix}"
  reader_username = "tf_complex_reader_${local.suffix}"
  share_name      = "tf_complex_share_${local.suffix}"
}

resource "motherduck_service_account" "writer" {
  username = local.writer_username
}

resource "motherduck_service_account" "reader" {
  username = local.reader_username
}

resource "motherduck_duckling_config" "writer" {
  username = motherduck_service_account.writer.username

  read_write_instance_size   = "pulse"
  read_scaling_instance_size = "pulse"
  read_scaling_flock_size    = 3
}

resource "motherduck_duckling_config" "reader" {
  username = motherduck_service_account.reader.username

  read_write_instance_size   = "pulse"
  read_scaling_instance_size = "pulse"
  read_scaling_flock_size    = 4
}

resource "motherduck_access_token" "writer" {
  username   = motherduck_service_account.writer.username
  name       = "terraform-writer"
  token_type = "read_write"
  ttl        = 86400

  depends_on = [motherduck_duckling_config.writer]
}

resource "motherduck_access_token" "reader" {
  username   = motherduck_service_account.reader.username
  name       = "terraform-reader"
  token_type = "read_scaling"
  ttl        = 86400

  depends_on = [motherduck_duckling_config.reader]
}

resource "motherduck_database" "tenant" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database = motherduck_database.tenant.name
  name     = local.schema_name
}

resource "motherduck_table" "customers" {
  database = motherduck_database.tenant.name
  schema   = motherduck_schema.app.name
  name     = "customers"

  columns = {
    customer_id = "VARCHAR"
    email       = "VARCHAR"
    tier        = "VARCHAR"
    created_at  = "TIMESTAMP"
  }
}

resource "motherduck_table" "events" {
  database = motherduck_database.tenant.name
  schema   = motherduck_schema.app.name
  name     = "events"

  columns = {
    event_id    = "VARCHAR"
    customer_id = "VARCHAR"
    event_name  = "VARCHAR"
    event_ts    = "TIMESTAMP"
    amount      = "DOUBLE"
  }
}

resource "motherduck_view" "customer_event_summary" {
  database = motherduck_database.tenant.name
  schema   = motherduck_schema.app.name
  name     = "customer_event_summary"
  query    = <<-SQL
    SELECT
      c.customer_id,
      c.email,
      count(e.event_id) AS event_count,
      coalesce(sum(e.amount), 0) AS total_amount,
      max(e.event_ts) AS last_event_ts
    FROM "${motherduck_database.tenant.name}"."${motherduck_schema.app.name}"."customers" AS c
    LEFT JOIN "${motherduck_database.tenant.name}"."${motherduck_schema.app.name}"."events" AS e
      ON c.customer_id = e.customer_id
    GROUP BY ALL
  SQL

  depends_on = [
    motherduck_table.customers,
    motherduck_table.events,
  ]
}

resource "motherduck_share" "tenant" {
  name            = local.share_name
  source_database = motherduck_database.tenant.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [
    motherduck_table.customers,
    motherduck_table.events,
    motherduck_view.customer_event_summary,
  ]
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.tenant.name
  username = motherduck_service_account.reader.username
}

data "motherduck_user_tokens" "writer" {
  username = motherduck_service_account.writer.username

  depends_on = [motherduck_access_token.writer]
}

data "motherduck_user_tokens" "reader" {
  username = motherduck_service_account.reader.username

  depends_on = [motherduck_access_token.reader]
}

output "writer_username" {
  value = motherduck_service_account.writer.username
}

output "reader_username" {
  value = motherduck_service_account.reader.username
}

output "database_name" {
  value = motherduck_database.tenant.name
}

output "share_name" {
  value = motherduck_share.tenant.name
}

output "writer_token_metadata_json" {
  value     = data.motherduck_user_tokens.writer.tokens_json
  sensitive = true
}

output "reader_token_metadata_json" {
  value     = data.motherduck_user_tokens.reader.tokens_json
  sensitive = true
}
