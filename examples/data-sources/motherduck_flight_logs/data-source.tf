# Read the most recent 200 lines of a Flight run log.
data "motherduck_flight_logs" "daily_load" {
  flight_id  = "11111111-1111-4111-8111-111111111111"
  run_number = 1
  limit      = 200
  order      = "desc"
}
