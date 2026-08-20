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

variable "database_name" {
  type = string
}

variable "schema_name" {
  type = string
}

variable "table_name" {
  type = string
}

variable "view_name" {
  type = string
}

variable "view_query" {
  type = string
}

variable "share_name" {
  type = string
}

variable "snapshot_name" {
  type = string
}

variable "secret_name" {
  type = string
}

resource "motherduck_database" "imported" {
  name = var.database_name
}

resource "motherduck_schema" "imported" {
  database = var.database_name
  name     = var.schema_name
}

resource "motherduck_table" "imported" {
  database = var.database_name
  schema   = var.schema_name
  name     = var.table_name

  columns = {
    "id value"         = "INTEGER"
    "label \"quoted\"" = "VARCHAR"
  }
}

resource "motherduck_view" "imported" {
  database = var.database_name
  schema   = var.schema_name
  name     = var.view_name
  query    = chomp(var.view_query)
}

resource "motherduck_share" "imported" {
  name            = var.share_name
  source_database = var.database_name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
}

resource "motherduck_snapshot" "imported" {
  database = var.database_name
  name     = var.snapshot_name
}

resource "motherduck_secret" "imported" {
  name = var.secret_name
  type = "s3"
}
