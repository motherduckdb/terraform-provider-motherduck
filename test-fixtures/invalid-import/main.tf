terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

resource "motherduck_database" "database" {
  name = "valid_database"
}

resource "motherduck_schema" "schema" {
  database = "valid_database"
  name     = "valid_schema"
}

resource "motherduck_table" "table" {
  database = "valid_database"
  schema   = "valid_schema"
  name     = "valid_table"

  columns = {
    id = "INTEGER"
  }
}

resource "motherduck_secret" "secret" {
  name = "valid_secret"
  type = "s3"

  params = {
    key_id = "dummy"
  }
}

resource "motherduck_share" "share" {
  name            = "valid_share"
  source_database = "valid_database"
}

resource "motherduck_share_grant" "grant" {
  share    = "valid_share"
  username = "valid_user"
}

resource "motherduck_access_token" "token" {
  username = "valid_user"
  name     = "valid-token"
}

resource "motherduck_dive" "dive" {
  title   = "valid dive"
  content = "export default function Dive() { return <main>valid</main>; }"
}

resource "motherduck_flight" "flight" {
  name        = "valid_flight"
  source_code = "SELECT 1 AS ok"
}
