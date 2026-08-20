resource "motherduck_database" "analytics" {
  name = "analytics"
}

resource "motherduck_snapshot" "monthly" {
  database = motherduck_database.analytics.name
  name     = "analytics_monthly"
}
