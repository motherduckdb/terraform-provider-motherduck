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
  }
}

resource "motherduck_view" "recent_events" {
  database = motherduck_database.analytics.name
  schema   = motherduck_schema.mart.name
  name     = "recent_events"
  query    = "SELECT event_id, occurred_at FROM \"${motherduck_database.analytics.name}\".\"${motherduck_schema.mart.name}\".\"${motherduck_table.events.name}\""
}
