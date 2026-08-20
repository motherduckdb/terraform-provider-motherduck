terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

provider "motherduck" {
  api_base_url = "https://api.motherduck.com?token=bad"
  attach_mode  = " single"
}

resource "motherduck_database" "valid" {
  name = "valid_provider_shape_validation"
}
