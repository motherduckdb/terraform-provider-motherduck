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

data "motherduck_flights" "bad_pagination" {
  limit  = -1
  offset = -1
}

data "motherduck_flight_logs" "bad_run_number" {
  flight_id  = "00000000-0000-0000-0000-000000000000"
  run_number = 0
}

data "motherduck_user_tokens" "bad_username" {
  username = " "
}

data "motherduck_dive_embed_session" "bad_config" {
  dive_id      = "not-a-uuid"
  username     = " "
  session_hint = " "
}

data "motherduck_dive" "bad_whitespace_dive_id" {
  dive_id = " 123e4567-e89b-42d3-a456-426614174000"
}

resource "motherduck_flight_run" "bad_whitespace_flight_id" {
  flight_id = "123e4567-e89b-42d3-a456-426614174000 "
}

resource "motherduck_flight_run" "bad_wait_options" {
  flight_id             = "123e4567-e89b-42d3-a456-426614174000"
  wait_for_status       = "done"
  poll_interval_seconds = 0
  timeout_seconds       = 0
}

resource "motherduck_flight" "bad_config" {
  name        = "bad_config"
  source_code = "print('bad config')"

  config = {
    ""               = "empty"
    "BAD=KEY"        = "equals"
    MOTHERDUCK_TOKEN = "reserved"
  }
}

resource "motherduck_flight_run" "bad_config" {
  flight_id = "123e4567-e89b-42d3-a456-426614174000"

  config = {
    MOTHERDUCK_FLIGHTS_RUN = "reserved"
  }
}

resource "motherduck_database" "dotted" {
  name = "bad.name"
}

resource "motherduck_database" "whitespace_wrapped" {
  name = " bad_whitespace_wrapped "
}

resource "motherduck_database" "transient_type" {
  name          = "bad_transient_type"
  database_type = "transient"
}

resource "motherduck_database" "database_type_case" {
  name          = "bad_database_type_case"
  database_type = "DEFAULT"
}

resource "motherduck_database" "negative_snapshot_retention" {
  name                    = "bad_negative_snapshot_retention"
  snapshot_retention_days = -1
}

resource "motherduck_database" "data_path_default" {
  name      = "bad_data_path_default"
  data_path = "s3://example-bucket/path"
}

resource "motherduck_database" "data_path_blank" {
  name          = "bad_data_path_blank"
  database_type = "ducklake"
  data_path     = " "
}

resource "motherduck_database" "encrypted_default" {
  name      = "bad_encrypted_default"
  encrypted = true
}

resource "motherduck_database" "transient_ducklake" {
  name          = "bad_transient_ducklake"
  transient     = true
  database_type = "ducklake"
}

resource "motherduck_table" "empty_columns" {
  database = "bad_table_db"
  schema   = "main"
  name     = "bad_empty_columns"
  columns  = {}
}

resource "motherduck_table" "blank_column_type" {
  database = "bad_table_db"
  schema   = "main"
  name     = "bad_blank_column_type"

  columns = {
    id = " "
  }
}

resource "motherduck_table" "semicolon_column_type" {
  database = "bad_table_db"
  schema   = "main"
  name     = "bad_semicolon_column_type"

  columns = {
    id = "INTEGER; DROP TABLE bad_table_db.main.other"
  }
}

resource "motherduck_secret" "bad_type" {
  name = "bad_secret_type"
  type = "s3; DROP SECRET other"
}

resource "motherduck_secret" "bad_type_case" {
  name = "bad_secret_type_case"
  type = "S3"
}

resource "motherduck_secret" "bad_param_key" {
  name = "bad_secret_param"
  type = "s3"

  params = {
    "key-id" = "abc"
  }
}

resource "motherduck_secret" "bad_raw_sql" {
  name       = "bad_secret_raw_sql"
  type       = "s3"
  secret_sql = "URL_STYLE 'path'; DROP SECRET other"
}

resource "motherduck_share" "bad_options" {
  name            = "bad_share_options"
  source_database = "bad_share_db"
  access          = "public"
  visibility      = "visible"
  update_mode     = "continuous"
}

resource "motherduck_share_grant" "bad_username" {
  share    = "valid_share"
  username = " "
}

resource "motherduck_service_account" "bad_username" {
  username = "1-bad service account"
}

resource "motherduck_service_account" "unicode_username" {
  username = "équipe_1"
}

resource "motherduck_access_token" "bad_config" {
  username   = "svc_reader"
  name       = ""
  token_type = "read_only"
  ttl        = 299
}

resource "motherduck_access_token" "bad_token_type_case" {
  username   = "svc_reader"
  name       = "bad-token-case"
  token_type = "READ_SCALING"
}

resource "motherduck_duckling_config" "bad_config" {
  username = "svc_reader"

  read_write_instance_size      = "tiny"
  read_write_cooldown_seconds   = 59
  read_scaling_instance_size    = "huge"
  read_scaling_flock_size       = 65
  read_scaling_cooldown_seconds = 86401
}

resource "motherduck_duckling_config" "bad_instance_case" {
  username = "svc_reader"

  read_write_instance_size   = "STANDARD"
  read_scaling_instance_size = "STANDARD"
  read_scaling_flock_size    = 1
}
