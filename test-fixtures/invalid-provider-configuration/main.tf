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
  api_base_url = "https://user:pass@api.motherduck.com"
  attach_mode  = "isolated"
}

resource "motherduck_database" "valid" {
  name = "valid_preview_provider_validation"
}
