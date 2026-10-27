# Destroy drops the database and all of its data. Add
# lifecycle { prevent_destroy = true } to databases that hold production data.
resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}
