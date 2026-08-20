resource "motherduck_flight" "heartbeat" {
  name            = "heartbeat"
  max_runtime_sec = 900

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
