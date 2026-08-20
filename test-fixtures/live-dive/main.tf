terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

variable "run_id" {
  type = string
}

variable "title_suffix" {
  type = string
}

variable "description" {
  type = string
}

variable "content_label" {
  type = string
}

variable "status" {
  type     = string
  default  = null
  nullable = true
}

locals {
  suffix = replace(var.run_id, "-", "_")
}

resource "motherduck_dive" "smoke" {
  title       = "Terraform Dive ${local.suffix} ${var.title_suffix}"
  description = var.description
  api_version = 1
  status      = var.status

  content = <<-JSX
    export default function Dive() {
      return <main>${var.content_label}</main>;
    }
  JSX
}

data "motherduck_dives" "all" {
  limit = 20

  depends_on = [motherduck_dive.smoke]
}

data "motherduck_dive" "smoke" {
  dive_id = motherduck_dive.smoke.id
}

data "motherduck_dive_versions" "smoke" {
  dive_id = motherduck_dive.smoke.id
}

output "dive_id" {
  value = motherduck_dive.smoke.id
}

output "current_version" {
  value = motherduck_dive.smoke.current_version
}

output "status" {
  value = motherduck_dive.smoke.status
}

output "status_applies_to_version" {
  value = motherduck_dive.smoke.status_applies_to_version
}

output "dives_rows_json" {
  value     = data.motherduck_dives.all.rows_json
  sensitive = true
}

output "dive_rows_json" {
  value     = data.motherduck_dive.smoke.rows_json
  sensitive = true
}

output "dive_versions_rows_json" {
  value     = data.motherduck_dive_versions.smoke.rows_json
  sensitive = true
}
