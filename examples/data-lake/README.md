# Data lake on your own bucket

A DuckLake database keeps its catalog in MotherDuck and its data files in an S3
prefix you own. This root creates the scoped S3 secret, the DuckLake database,
a schema, and one landing table.

Choose DuckLake when the data must stay in your bucket or in an open file
format. Native MotherDuck storage remains the default for a warehouse. See
[DuckLake on MotherDuck](https://motherduck.com/docs/integrations/file-formats/ducklake/).

To query an existing Iceberg REST catalog instead of creating a lake, see the
[Iceberg database example](../resources/motherduck_database/iceberg/main.tf).

## Prerequisites

- Terraform 1.11 or later, because the secret uses write-only arguments.
- A MotherDuck read-write token for the account that will own the lake,
  injected as `MOTHERDUCK_TOKEN`. No organization admin token is needed.
- An S3 prefix that nothing else writes to.
- An IAM identity whose policy allows listing the bucket under that prefix and
  reading, writing, and deleting objects in it, and nothing else.

## Apply

Supply the access key through your secret manager as `TF_VAR_aws_access_key_id`
and `TF_VAR_aws_secret_access_key`. Do not write it to a `.tfvars` file.

```shell
terraform init
terraform plan -var=data_path=s3://my-bucket/lake/ -var=bucket_region=us-east-1 -out=lake.tfplan
terraform apply lake.tfplan
terraform plan -var=data_path=s3://my-bucket/lake/ -var=bucket_region=us-east-1 -detailed-exitcode
```

The plan creates four resources. Expect the final plan to return 0.

Terraform creates structure only. Load `events.page_views` from your pipeline
as the account that owns the database.

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `name_prefix` | `example_lake` | Unique prefix for the secret and database |
| `data_path` | none | S3 prefix for the data files, ending in a slash |
| `bucket_region` | none | AWS region of the bucket |
| `aws_access_key_id`, `aws_secret_access_key` | none | Credentials limited to `data_path` |
| `credentials_version` | `1` | Change it to write a rotated access key |

## Outputs

| Output | Use |
| --- | --- |
| `database` | DuckLake database name |
| `table` | Fully qualified landing table for the pipeline |
| `secret_name` | MotherDuck secret that grants access to the prefix |

## Rotate the access key

Terraform does not store the write-only values, so it cannot see that they
changed. Supply the new key and raise `credentials_version`:

```shell
terraform apply -var=credentials_version=2 -var=data_path=s3://my-bucket/lake/ -var=bucket_region=us-east-1
```

The plan updates the secret in place. Deactivate the old key in AWS only after
the apply succeeds.

## Lifecycle

- `data_path` is fixed when the database is created. Changing it plans a
  replacement, which `prevent_destroy` turns into an error. Create a second
  database and move the data instead.
- DuckLake does not support primary keys, so leave `primary_key` unset on
  tables in this database.
- Adding or dropping a column updates the table in place. Changing a column
  type replaces the table and its rows.
- Dropping the database does not prove that the bucket is empty. Review the
  prefix and your retention rules before deleting objects.

For disposable cleanup, remove the `prevent_destroy` block, then run
`terraform destroy` with the same variables.
