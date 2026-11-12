# Requires a Business plan and a MotherDuck token with query history access,
# such as an organization admin token.
data "motherduck_agent_usage" "november" {
  start_time = "2026-11-01T00:00:00Z"
  end_time   = "2026-12-01T00:00:00Z"
}

output "ai_units_by_user" {
  value = {
    for user in distinct([for row in data.motherduck_agent_usage.november.rows : row.user_name]) :
    user => sum([for row in data.motherduck_agent_usage.november.rows : tonumber(row.ai_units) if row.user_name == user])
  }
}
