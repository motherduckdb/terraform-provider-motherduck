# Pulumi Python

This is a small Pulumi program using Pulumi's Any Terraform Provider bridge with
the MotherDuck Terraform provider. It creates one database, schema, and table.

The recipe was verified with Pulumi `v3.261.0` and the MotherDuck provider
`v0.2.10` on macOS ARM64. The bridge package is pinned to `v1.4.0` in
`Pulumi.yaml`, and the Pulumi SDK is pinned to `3.261.0` in `requirements.txt`.
The sample database name is required configuration so each stack can use a
unique name.

## Setup

Install Pulumi `v3.261.0`, then place the provider binary at
`bin/terraform-provider-motherduck`. The binary name and path are significant:
Pulumi requires a local provider path ending in `terraform-provider-<name>`.

Download the v0.2.10 artifact for your platform from the
[MotherDuck v0.2.10 GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases/tag/v0.2.10),
verify it against `terraform-provider-motherduck_0.2.10_SHA256SUMS` and its
detached publisher signature (see the [signed installation guide](../../../docs/guides/github-installation.md)), unzip
it, and rename the executable to `bin/terraform-provider-motherduck`.

Initialize a local backend and install the pinned bridge and generated SDK:

```shell
pulumi login --local
# Inject PULUMI_CONFIG_PASSPHRASE through your secret manager first.
pulumi stack init dev
pulumi config set databaseName pulumi_example_myteam_dev
pulumi install
```

`pulumi install` reads the pinned `terraform-provider` package declaration,
generates the local `pulumi_motherduck` SDK, and creates `.venv` from the
checked-in runtime options and requirements. Do not run an unversioned
`pulumi package add` command here because it can select a newer bridge. Keep
`Pulumi.yaml` and `requirements.txt` under source control. Keep the matching
release checksum beside the downloaded artifact while verifying it. `.venv`, `sdks`, `bin`, and
`Pulumi.<stack>.yaml` are local files and are ignored.

`PULUMI_CONFIG_PASSPHRASE` must come from a secret manager or protected CI
environment. The local backend encrypts stack secrets, but the passphrase and
the local state directory still need filesystem protection. Choose a database name unique to this stack before running the example.

## Lifecycle

Supply the SQL token through the environment. Wrapping it in
`pulumi.Output.secret` keeps the provider configuration and dependent values
secret in Pulumi state and UI output.

```shell
export MOTHERDUCK_TOKEN='...'
pulumi preview
pulumi up
pulumi refresh
pulumi preview                 # should report no changes
pulumi destroy
```

The verified import IDs for this provider follow the Terraform provider's
existing formats: `<database>`, `<database>.<schema>`, and
`<database>.<schema>.<table>` for a database, schema, and table respectively.
Use `pulumi import` with those IDs only after confirming the resource is intended
for this stack and is no longer managed by another Terraform or Pulumi state, then copy the generated protected resource definitions into the
program. Remove protection deliberately before destroying imported resources.

Keep the database and its owner identity in one clearly owned stack, or use an
explicit stack dependency and teardown order. Protect production databases and
owner service accounts, protect stored state and encrypt its secrets, and do not
export raw access tokens. Pin both the Pulumi bridge and the wrapped provider.
The bridge version and provider version are independent.

This minimal program uses an existing writer token. It does not create a service
account or demonstrate same-program identity bootstrap. Create the identity in a
separate protected bootstrap stack when needed and remove consuming data stacks
before deleting that identity.

From the provider repository, `make test-pulumi-example` builds the provider,
generates the pinned bridge SDK, and runs a no-update preview with a fake token.
It requires the pinned Pulumi CLI. The live check additionally applies, refreshes,
checks a no-change preview and destroys a uniquely named disposable database.
