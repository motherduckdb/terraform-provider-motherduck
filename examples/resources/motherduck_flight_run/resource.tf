# Deprecated. Each create or replacement runs the Flight again. Prefer
# schedule_cron on motherduck_flight, or trigger runs from a deployment
# pipeline.
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

  wait_for_status = "succeeded"
}
