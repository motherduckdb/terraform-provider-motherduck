# Requires a Business plan and a MotherDuck token with query history access,
# such as an organization admin token.
data "motherduck_compute_usage" "etl_yesterday" {
  username   = "etl_service"
  start_time = "2026-11-01T00:00:00Z"
  end_time   = "2026-11-02T00:00:00Z"
}

output "etl_uptime_seconds" {
  value = sum([for row in data.motherduck_compute_usage.etl_yesterday.rows : tonumber(row.uptime_seconds)])
}
