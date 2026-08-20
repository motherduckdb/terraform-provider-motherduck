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

variable "content" {
  type = string
}

locals {
  suffix = replace(var.run_id, "-", "_")
  topic  = "terraform-smoke/${local.suffix}"
}

resource "motherduck_guide" "foundation" {
  topic   = local.topic
  title   = "Terraform Guide foundation ${local.suffix}"
  content = "# Foundation"
  access  = "user"
}

resource "motherduck_guide" "managed" {
  topic          = local.topic
  title          = "Terraform Guide smoke ${local.suffix}"
  description    = "Managed by the Terraform provider live smoke"
  content        = var.content
  change_comment = "Terraform live smoke"
  external_id    = var.run_id
  access         = "user"

  references = [
    {
      type        = "guide"
      uuid        = motherduck_guide.foundation.id
      description = "Foundation Guide"
    }
  ]
}

data "motherduck_guides" "smoke" {
  topic = local.topic

  depends_on = [motherduck_guide.managed]
}

data "motherduck_guides" "referencing_foundation" {
  reference_type = "guide"
  reference_uuid = motherduck_guide.foundation.id

  depends_on = [motherduck_guide.managed]
}

data "motherduck_roles" "all" {}

data "motherduck_guide" "managed" {
  guide_id = motherduck_guide.managed.id
}

data "motherduck_guide_versions" "managed" {
  guide_id = motherduck_guide.managed.id
}

output "current_version" {
  value = motherduck_guide.managed.current_version
}

output "reference_uuid" {
  value = motherduck_guide.managed.references[0].uuid
}

output "foundation_id" {
  value = motherduck_guide.foundation.id
}

output "guides_rows_json" {
  value     = data.motherduck_guides.smoke.rows_json
  sensitive = true
}

output "referencing_guides_rows_json" {
  value     = data.motherduck_guides.referencing_foundation.rows_json
  sensitive = true
}

output "guide_rows_json" {
  value     = data.motherduck_guide.managed.rows_json
  sensitive = true
}

output "guide_versions_rows_json" {
  value     = data.motherduck_guide_versions.managed.rows_json
  sensitive = true
}

output "roles_rows_json" {
  value     = data.motherduck_roles.all.rows_json
  sensitive = true
}
