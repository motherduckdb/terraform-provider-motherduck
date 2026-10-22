# Requires a MotherDuck token for an organization admin.
data "motherduck_storage_info" "analytics" {
  database_name = "analytics"
}

output "analytics_active_bytes" {
  value = one(data.motherduck_storage_info.analytics.rows[*].active_bytes)
}
