resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}

