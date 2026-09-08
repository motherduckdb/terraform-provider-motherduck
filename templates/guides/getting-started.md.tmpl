# Create your first database

This walkthrough creates a database and schema, checks that a second plan is
empty, and removes both objects.

## Prerequisites

Use Terraform 1.5 or later, a MotherDuck account with permission to create
databases, and a SQL token supplied as `MOTHERDUCK_TOKEN`. An organization admin
token is not needed for this walkthrough.

`terraform init` installs the provider from the Terraform Registry, so no setup
is needed first. To install without Registry access, see
[direct installation](github-installation.md). For source builds, use
[local development](local-development.md).

## Create a working directory

Save this configuration as `main.tf` in an empty directory:

```hcl
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

resource "motherduck_database" "example" {
  name = "terraform_tutorial"
}

resource "motherduck_schema" "example" {
  database = motherduck_database.example.name
  name     = "app"
}
```

Choose an unused database name. Do not set the provider's `database` option to
the database being created; provider initialization happens before creation.

## Plan and apply

Run `terraform init` first and commit `.terraform.lock.hcl`, which records the
provider version and its verified digests. Development overrides use the
separate setup in the local development guide.

```shell
terraform fmt
terraform validate
terraform plan -out=tfplan
terraform apply tfplan
terraform plan -detailed-exitcode
```

The first plan should contain two creations. The final plan should report no
changes and exit with code 0. Code 2 means there are proposed changes; code 1
means an error. Review unexpected changes before applying again.

## Clean up

```shell
terraform plan -destroy -out=destroy.tfplan
terraform apply destroy.tfplan
```

Both managed resources should be removed. Do not put unrelated data in this
tutorial database. See [state and lifecycle](state-and-lifecycle.md) before
managing an existing production database.
