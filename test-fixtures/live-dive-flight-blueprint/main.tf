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
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_dive_flight_${local.suffix}"
  share_name    = "tf_dive_flight_share_${local.suffix}"
  flight_name   = "tf_dive_flight_${local.suffix}"
}

resource "motherduck_database" "pageviews" {
  name = local.database_name
}

resource "motherduck_table" "pageviews" {
  database = motherduck_database.pageviews.name
  schema   = "main"
  name     = "pageviews"

  columns = {
    page_title = "VARCHAR"
    views      = "INTEGER"
  }
}

resource "motherduck_share" "pageviews" {
  name            = local.share_name
  source_database = motherduck_database.pageviews.name
  access          = "unrestricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.pageviews]
}

data "motherduck_owned_share" "pageviews" {
  name = motherduck_share.pageviews.name

  depends_on = [motherduck_share.pageviews]
}

resource "motherduck_flight" "pageviews" {
  name = local.flight_name

  source_code = <<-PY
    def main():
        print("pageviews blueprint flight")

    if __name__ == "__main__":
        main()
  PY
}

resource "motherduck_flight_run" "pageviews" {
  flight_id = motherduck_flight.pageviews.id

  wait_for_status       = "succeeded"
  poll_interval_seconds = 10
  timeout_seconds       = 600
  cancel_on_destroy     = true
}

resource "motherduck_dive" "pageviews" {
  title       = "Wikipedia Pageviews ${local.suffix}"
  api_version = 1

  content = <<-JSX
    export default function Dive() {
      return <main>Wikipedia Pageviews</main>;
    }
  JSX

  required_resources = [
    {
      alias = "wikipedia_pageviews"
      url   = motherduck_share.pageviews.url
    }
  ]

  depends_on = [motherduck_flight_run.pageviews]
}

output "database_name" {
  value = motherduck_database.pageviews.name
}

output "share_name" {
  value = motherduck_share.pageviews.name
}

output "share_url" {
  value     = data.motherduck_owned_share.pageviews.url
  sensitive = true
}

output "flight_run_status" {
  value = motherduck_flight_run.pageviews.status
}

output "dive_id" {
  value = motherduck_dive.pageviews.id
}
