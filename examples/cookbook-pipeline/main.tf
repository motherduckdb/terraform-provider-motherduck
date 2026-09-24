terraform {
  required_version = ">= 1.5.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = ">= 0.2.10"
    }
  }
}
provider "motherduck" {}
variable "database_name" {
  description = "Unique database owned by the injected writer account. Changing it replaces the database and deletes its data."
  type        = string
  nullable    = false
  default     = "example_cookbook_pipeline"
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,63}$", var.database_name))
    error_message = "Use a lowercase database identifier of at most 64 characters."
  }
}
resource "motherduck_database" "pipeline" {
  name                    = var.database_name
  snapshot_retention_days = 7
}
output "pipeline_config" {
  description = "Non-secret settings consumed by the cookbook runner and dbt profile."
  value = {
    DESTINATION_DATABASE = motherduck_database.pipeline.name
    DATASET_NAME         = "raw"
    TABLE_NAME           = "github_repo_stats"
    PIPELINE_NAME        = "${var.database_name}_ingest"
    PRIMARY_KEY          = "repo"
    WRITE_DISPOSITION    = "merge"
    GITHUB_REPOS         = "duckdb/duckdb,motherduckdb/terraform-provider-motherduck"
    RUN_LEDGER_TABLE     = "dlt_ingest_runs"
  }
}
