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

locals {
  suffix = replace(var.run_id, "-", "_")
}

resource "motherduck_dive" "unavailable" {
  title       = "Terraform unavailable Dive ${local.suffix}"
  description = "Negative smoke for unavailable SQL function diagnostics"
  api_version = 1

  content = <<-JSX
    export default function Dive() {
      return <main>unavailable SQL function diagnostic</main>;
    }
  JSX
}
