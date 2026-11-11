# The secret is scoped to the lake prefix, so MotherDuck uses these credentials
# only for paths under it. The write-only arguments keep the access key out of
# the Terraform plan and state.
resource "motherduck_secret" "lake" {
  name = "${var.name_prefix}_s3"
  type = "s3"

  params_wo = {
    key_id = var.aws_access_key_id
    secret = var.aws_secret_access_key
    region = var.bucket_region
    scope  = var.data_path
  }
  params_wo_version = var.credentials_version
}

# A DuckLake database keeps its catalog in MotherDuck and its data files in
# your bucket. data_path is fixed at creation, and Terraform replaces the
# database when it changes. prevent_destroy makes that plan fail instead.
resource "motherduck_database" "lake" {
  name          = "${var.name_prefix}_lake"
  database_type = "ducklake"
  data_path     = var.data_path

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [motherduck_secret.lake]
}

resource "motherduck_schema" "events" {
  database = motherduck_database.lake.name
  name     = "events"
}

# DuckLake has no primary keys, so primary_key stays unset. Leave not_null
# unset as well and enforce required columns in the pipeline that loads the
# table.
resource "motherduck_table" "page_views" {
  database = motherduck_database.lake.name
  schema   = motherduck_schema.events.name
  name     = "page_views"

  columns = {
    event_id    = "VARCHAR"
    occurred_at = "TIMESTAMP WITH TIME ZONE"
    user_id     = "VARCHAR"
    url         = "VARCHAR"
  }
  column_order = ["event_id", "occurred_at", "user_id", "url"]
}
