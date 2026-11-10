resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_schema" "mart" {
  database = motherduck_database.analytics.name
  name     = "mart"
}

resource "motherduck_table" "events" {
  database = motherduck_database.analytics.name
  schema   = motherduck_schema.mart.name
  name     = "events"

  columns = {
    event_id    = "VARCHAR"
    occurred_at = "TIMESTAMP"
    payload     = "JSON"
  }

  # Optional. Without column_order, a new table has its columns in
  # alphabetical order.
  column_order = ["event_id", "occurred_at", "payload"]
  primary_key  = ["event_id"]
  not_null     = ["occurred_at"]
}
