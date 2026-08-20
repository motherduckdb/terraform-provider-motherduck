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
}
