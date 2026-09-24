# Provision a database, then ingest and model with the cookbooks

Terraform owns the writer's database. A pinned MotherDuck cookbook handles dlt
loading, and dbt owns the analytical models. Terraform never defines their tables
or triggers the jobs. This is a small companion to the [dlt Flight recipe](https://motherduck.com/docs/cookbook/flight-dlt-ingest/)
and the [dbt warehouse recipes](https://motherduck.com/docs/cookbook/dbt-ingestion-s3/).

## Provision as the writer

Use the [writer bootstrap](../blueprints/writer-bootstrap/README.md) first with
admin credentials. Transfer its writer token through your secret manager and
inject it as `MOTHERDUCK_TOKEN` for this root and its runtimes. Configure writer
compute in your admin root when your workload needs explicit sizing. The runtime
jobs do not need the admin token.

Keep dev and prod in separate directories/states using their respective writer
credentials and unique database names. Changing `--target prod` does not switch
identity or select a different Terraform state.

Changing `database_name` replaces the Terraform-managed database. That destroys
the database and any dlt or dbt tables inside it. Use a new database name for a
separate environment or review a data migration before changing this value.

```shell
terraform init
terraform plan -var=database_name=myteam_dev_pipeline -out=pipeline.tfplan
terraform apply pipeline.tfplan
terraform output -json pipeline_config > pipeline.json
uv sync --frozen
```

`pipeline.json` contains names and options, not credentials. Protect state anyway.

## Run ingestion locally

```shell
uv run --frozen python run_ingest.py pipeline.json --fixture fixtures/repos.json
uv run --frozen python run_ingest.py pipeline.json --fixture fixtures/repos.json
uv run --frozen python run_ingest.py pipeline.json --fixture fixtures/repos-updated.json
```

The first two runs leave two rows in `raw.github_repo_stats`. The third changes
`demo/acme` to 12 stars and 3 forks without adding rows. Missing merge keys fail
before loading. Omit `--fixture` to use the cookbook's public GitHub API source.
Use separate databases for fixture and real-source runs: merge does not delete
rows absent from the next source batch.

`fetch.py` downloads the immutable commit in `cookbook.json` and verifies its
SHA-256. The dlt loading implementation is not copied or maintained here. The
local fixture adapter replaces only the source generator. The adapter checks
that the Terraform database exists before invoking the recipe.

This companion pins DuckDB 1.5.5 and dlt 1.27.2. The original recipe used dlt
1.27.0, which PyPI yanked for a merge data-loss bug. Keep the corrected pin when
reproducing this example. Review supported DuckDB versions before upgrades.

## Run it as a Flight

For this optional public-API path, provision a fresh database in a separate
Terraform root and regenerate `pipeline.json`. Do not reuse the fixture
database: merge preserves rows absent from the next source batch.

```shell
uv run --frozen python flight.py create pipeline.json
uv run --frozen python flight.py run pipeline.json
```

The deploy step creates an on-demand Flight and saves its ID in `flight-id.json`.
Keep that file with the deployment records until cleanup. It is separate from
Terraform state. The deployed source has one checked ownership change: `USE`
replaces the cookbook's database-creation fallback so missing infrastructure
fails. The Flight uses the account's injected runtime token and the public API
source, not the local fixture. Source secrets belong in Flights secrets, not
`pipeline_config`. This public-source example needs no source secret.

The runner waits for terminal success and fails on errors or timeout. Inspect
Flight logs privately for details. There is no schedule until you deliberately
add one after checking a successful run and the resulting data.

## Build dbt models

```shell
uv run --frozen python run_dbt.py pipeline.json
```

Expect `analytics.repository_metrics` with `engagement = stars + forks`, one row
per repository. With the updated fixture, Acme has engagement 15 and Globex 6.
`dbt build` runs uniqueness and required-value tests. Test failures fail this
command. Modify the model and rebuild to evolve its schema, then confirm:

```shell
terraform plan -var=database_name=myteam_dev_pipeline -detailed-exitcode
```

Expect exit 0. For production, use the production config and writer token with
`run_dbt.py pipeline.json --target prod`. The [dbt Flight cookbook](https://motherduck.com/docs/cookbook/flight-dbt-build-git/)
is an optional scheduling path. Its test-failure policy differs from this local
command, so check node results rather than treating a green Flight as proof.

## Existing Terraform tables

Do not run this over Terraform-managed tables. Back up state and data, review
ownership, remove the selected table/view from state without destroying it, then
remove its configuration before enabling dlt or dbt ownership. Verify the next
Terraform plan proposes neither deletion nor recreation. Database ownership can
stay with Terraform while its contents belong to the pipeline.

## Teardown

Stop scheduling and finish or cancel active runs first. Delete a Flight created
by this companion, then review database destruction:

```shell
uv run --frozen python flight.py delete pipeline.json
terraform plan -destroy -var=database_name=myteam_dev_pipeline -out=destroy.tfplan
terraform apply destroy.tfplan
```

Skip the Flight deletion command if you only ran locally. Database destruction
also removes runtime-owned dlt and dbt tables. Retain/export real data before
approving it. Delete the writer bootstrap last, after all its consumers and data
are removed. Do not delete only `flight-id.json` and strand the remote Flight.
