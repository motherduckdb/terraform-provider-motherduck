data "motherduck_database_snapshots" "analytics" {
  database_name = "analytics"
}

# Retained snapshots without a name report a null snapshot_name.
output "analytics_named_snapshots" {
  value = [
    for snapshot in data.motherduck_database_snapshots.analytics.rows :
    snapshot.snapshot_name if snapshot.snapshot_name != null
  ]
}
