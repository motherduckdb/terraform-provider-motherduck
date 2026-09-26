resource "motherduck_secret" "partner_api" {
  name = "partner_api"
  type = "flights"

  flight_params = {
    PARTNER_API_KEY = var.partner_api_key
    PARTNER_REGION  = "eu-west-1"
  }
}

resource "motherduck_flight" "partner_sync" {
  name                = "partner_sync"
  max_runtime_sec     = 900
  flight_secret_names = [motherduck_secret.partner_api.name]

  # Each pair is exposed as PARTNER_API_KEY and as partner_api_PARTNER_API_KEY.
  source_code = <<-PY
    import os

    def main():
        print("region:", os.environ["PARTNER_REGION"])

    if __name__ == "__main__":
        main()
  PY
}

variable "partner_api_key" {
  type      = string
  sensitive = true
}
