---
page_title: "Build a data lake on your own bucket"
subcategory: "Deployment architectures"
description: |-
  Create a DuckLake database whose data files stay in your S3 prefix, with credentials kept out of Terraform state.
---

# Build a data lake on your own bucket

A DuckLake database keeps its catalog in MotherDuck and its data files in an S3
prefix you own. Choose it when the data must stay in your bucket or in an open
file format. Native MotherDuck storage remains the default for a warehouse.

This guide creates the scoped S3 secret, the DuckLake database, a schema, and
one landing table.

![Terraform provisions a scoped S3 secret, a DuckLake database, a schema, and a table in the writer account. A pipeline loads data through the writer's Duckling. The DuckLake catalog stays in MotherDuck and the data files are written to an S3 prefix in your AWS account through the scoped secret.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/data-lake.png)

To query an existing Iceberg REST catalog instead of creating a lake, register
it with `database_type = "iceberg"`. See the [database resource](../resources/database.md).

## Before you start

- Terraform 1.11 or later, because the secret uses write-only arguments.
- A read-write token for the account that will own the lake, as
  `MOTHERDUCK_TOKEN`. No organization admin token is needed.
- An S3 prefix that nothing else writes to. The bucket's region can differ from
  your MotherDuck organization's region.
- An IAM identity limited to that prefix. Supply its access key through your
  secret manager as `TF_VAR_aws_access_key_id` and
  `TF_VAR_aws_secret_access_key`.

## Configuration

```terraform
# The secret is scoped to the lake prefix, so MotherDuck uses these credentials
# only for paths under it. The write-only arguments keep the access key out of
# the Terraform plan and state.
resource "motherduck_secret" "lake" {
  name = "${var.name_prefix}_s3"
  type = "s3"

  params_wo = {
    key_id = var.aws_access_key_id
    secret = var.aws_secret_access_key
    region = var.bucket_region
    scope  = var.data_path
  }
  params_wo_version = var.credentials_version
}

# A DuckLake database keeps its catalog in MotherDuck and its data files in
# your bucket. data_path is fixed at creation, and Terraform replaces the
# database when it changes. prevent_destroy makes that plan fail instead.
resource "motherduck_database" "lake" {
  name          = "${var.name_prefix}_lake"
  database_type = "ducklake"
  data_path     = var.data_path

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [motherduck_secret.lake]
}

resource "motherduck_schema" "events" {
  database = motherduck_database.lake.name
  name     = "events"
}

# DuckLake has no primary keys, so primary_key stays unset. Leave not_null
# unset as well and enforce required columns in the pipeline that loads the
# table.
resource "motherduck_table" "page_views" {
  database = motherduck_database.lake.name
  schema   = motherduck_schema.events.name
  name     = "page_views"

  columns = {
    event_id    = "VARCHAR"
    occurred_at = "TIMESTAMP WITH TIME ZONE"
    user_id     = "VARCHAR"
    url         = "VARCHAR"
  }
  column_order = ["event_id", "occurred_at", "user_id", "url"]
}
```

Three choices in this configuration matter in production:

| Choice | Reason |
| --- | --- |
| `scope` equals `data_path` | MotherDuck uses the credentials only for paths under the lake prefix |
| `params_wo` and `params_wo_version` | The access key is stored in neither the plan nor the state. See [secret](../resources/secret.md) |
| `prevent_destroy` on the database | `data_path` is fixed at creation, and a changed path would otherwise plan a replacement |

## Apply

```shell
terraform init
terraform plan -var=data_path=s3://my-bucket/lake/ -var=bucket_region=us-east-1 -out=lake.tfplan
terraform apply lake.tfplan
```

The plan creates four resources. Terraform creates structure only. Load
`events.page_views` from your pipeline as the account that owns the database.

## Rotate the access key

Terraform cannot see a change in a value it does not store. Supply the new key
and raise `credentials_version`. The plan updates the secret in place.
Deactivate the old key in AWS only after the apply succeeds.

## Lifecycle

- DuckLake does not support primary keys. Leave `primary_key` unset on tables
  in a DuckLake database.
- Adding or dropping a column updates a table in place. Changing a column type
  replaces the table and its rows.
- Dropping the database does not prove that the bucket is empty. Review the
  prefix and your retention rules before deleting objects.

The complete root, with inputs, outputs, and teardown, is in
[examples/data-lake](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/data-lake).
