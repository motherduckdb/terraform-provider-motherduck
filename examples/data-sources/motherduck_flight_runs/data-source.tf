data "motherduck_flight_runs" "daily_load" {
  flight_id = "11111111-1111-4111-8111-111111111111"
  limit     = 10
}

output "daily_load_run_statuses" {
  value = { for run in data.motherduck_flight_runs.daily_load.rows : run.run_number => run.status }
}
