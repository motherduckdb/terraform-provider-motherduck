resource "motherduck_flight" "load_orders" {
  name = "load_orders"

  source_code = <<-PY
    def main():
        print("loading orders")

    if __name__ == "__main__":
        main()
  PY
}

resource "motherduck_flight_run" "load_orders" {
  flight_id = motherduck_flight.load_orders.id

  wait_for_status = "succeeded"
  timeout_seconds = 1800
}

# If the wait times out or is interrupted, the apply still succeeds. The
# precondition keeps this view from being created until the run succeeds.
resource "motherduck_view" "recent_orders" {
  database = "analytics"
  schema   = "main"
  name     = "recent_orders"
  query    = "SELECT * FROM analytics.main.orders WHERE ordered_at > now() - INTERVAL 7 DAY"

  lifecycle {
    precondition {
      condition     = motherduck_flight_run.load_orders.status == "SUCCEEDED"
      error_message = "Flight run ${motherduck_flight_run.load_orders.run_number} has status ${motherduck_flight_run.load_orders.status}. Wait for it to succeed, then apply again."
    }
  }
}
