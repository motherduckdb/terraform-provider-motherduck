resource "motherduck_flight" "heartbeat" {
  name = "heartbeat"

  config = {
    mode = "default"
  }

  source_code = <<-PY
    def main():
        print("hello")

    if __name__ == "__main__":
        main()
  PY
}

resource "motherduck_flight_run" "heartbeat" {
  flight_id = motherduck_flight.heartbeat.id

  config = {
    mode = "manual"
  }

  wait_for_status       = "succeeded"
  poll_interval_seconds = 10
  timeout_seconds       = 600
}
