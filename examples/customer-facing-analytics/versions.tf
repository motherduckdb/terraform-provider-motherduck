terraform {
  required_version = ">= 1.5.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.2.2"
    }
  }
}

provider "motherduck" {}
