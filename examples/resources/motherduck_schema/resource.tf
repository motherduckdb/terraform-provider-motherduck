resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_schema" "mart" {
  database = motherduck_database.analytics.name
  name     = "mart"
}
