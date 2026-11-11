terraform {
  required_version = ">= 1.11.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.4.0"
    }
  }
}

provider "motherduck" {}
