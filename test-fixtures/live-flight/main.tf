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

variable "source_label" {
  type = string
}

variable "run_flight" {
  type    = bool
  default = false
}

locals {
  suffix      = replace(var.run_id, "-", "_")
  flight_name = "tf_flight_${local.suffix}"
}

resource "motherduck_flight" "smoke" {
  name            = local.flight_name
  max_runtime_sec = 300

  config = {
    SOURCE_LABEL = var.source_label
  }

  source_code = <<-PY
    import os

    def main():
        print(os.environ.get("SOURCE_LABEL", "missing"))

    if __name__ == "__main__":
        main()
  PY
}

resource "motherduck_flight_run" "smoke" {
  count = var.run_flight ? 1 : 0

  flight_id         = motherduck_flight.smoke.id
  cancel_on_destroy = true

  config = {
    SOURCE_LABEL = "run ${var.source_label}"
  }
}

data "motherduck_flights" "all" {
  limit = 20

  depends_on = [motherduck_flight.smoke]
}

data "motherduck_flight" "smoke" {
  flight_id = motherduck_flight.smoke.id
}

data "motherduck_flight_versions" "smoke" {
  flight_id = motherduck_flight.smoke.id
}

data "motherduck_flight_runs" "smoke" {
  flight_id = motherduck_flight.smoke.id

  depends_on = [motherduck_flight_run.smoke]
}

output "flight_id" {
  value = motherduck_flight.smoke.id
}

output "current_version" {
  value = motherduck_flight.smoke.current_version
}

output "max_runtime_sec" {
  value = motherduck_flight.smoke.max_runtime_sec
}

output "flights_rows_json" {
  value     = data.motherduck_flights.all.rows_json
  sensitive = true
}

output "flight_rows_json" {
  value     = data.motherduck_flight.smoke.rows_json
  sensitive = true
}

output "flight_versions_rows_json" {
  value     = data.motherduck_flight_versions.smoke.rows_json
  sensitive = true
}

output "flight_runs_rows_json" {
  value     = data.motherduck_flight_runs.smoke.rows_json
  sensitive = true
}
