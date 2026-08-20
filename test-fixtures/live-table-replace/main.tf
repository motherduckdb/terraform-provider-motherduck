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

variable "include_amount" {
  type = bool
}

locals {
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_replace_${local.suffix}"
  columns = merge(
    {
      id    = "INTEGER"
      label = "VARCHAR"
    },
    var.include_amount ? { amount = "DOUBLE" } : {}
  )
  view_query_with_amount    = <<-SQL
    SELECT
      id,
      label,
      amount
    FROM "${motherduck_database.edge.name}"."${motherduck_schema.app.name}"."${motherduck_table.facts.name}"
  SQL
  view_query_without_amount = <<-SQL
    SELECT
      id,
      label
    FROM "${motherduck_database.edge.name}"."${motherduck_schema.app.name}"."${motherduck_table.facts.name}"
  SQL
  view_query                = var.include_amount ? local.view_query_with_amount : local.view_query_without_amount
}

resource "motherduck_database" "edge" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database = motherduck_database.edge.name
  name     = "app"
}

resource "motherduck_table" "facts" {
  database = motherduck_database.edge.name
  schema   = motherduck_schema.app.name
  name     = "facts"
  columns  = local.columns
}

resource "motherduck_view" "facts_v" {
  database = motherduck_database.edge.name
  schema   = motherduck_schema.app.name
  name     = "facts_v"
  query    = local.view_query

  depends_on = [motherduck_table.facts]
}

output "database_name" {
  value = motherduck_database.edge.name
}
