resource "motherduck_flight" "heartbeat" {
  name            = "heartbeat"
  max_runtime_sec = 900
  instance_type   = "F4"

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
