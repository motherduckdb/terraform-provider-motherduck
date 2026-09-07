# Pulumi Python

This is a small Pulumi program using Pulumi's Any Terraform Provider bridge with
the MotherDuck Terraform provider. It creates one database, schema, and table.

The recipe was verified with Pulumi `v3.261.0` and the MotherDuck provider
`v0.1.1` on macOS ARM64. The bridge package is pinned to `v1.4.0` in
`Pulumi.yaml`, and the Pulumi SDK is pinned to `3.261.0` in `requirements.txt`.
The sample database name is required configuration so each stack can use a
unique name.

## Setup

Install Pulumi `v3.261.0`, then place the provider binary at
`bin/terraform-provider-motherduck`. The binary name and path are significant:
Pulumi requires a local provider path ending in `terraform-provider-<name>`.

Download the v0.1.1 artifact for your platform from the
[MotherDuck v0.1.1 GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases/tag/v0.1.1),
verify it against the matching `provider_<platform>_<arch>.sha256` file, unzip
it, and rename the executable to `bin/terraform-provider-motherduck`.

Initialize a local backend and install the pinned bridge and generated SDK:

```shell
pulumi login --local
export PULUMI_CONFIG_PASSPHRASE="$(secret-manager-read pulumi/passphrase)"
pulumi stack init dev
pulumi config set databaseName pulumi_example_<unique-suffix>
pulumi install
```

`pulumi install` reads the pinned `terraform-provider` package declaration,
generates the local `pulumi_motherduck` SDK, and creates `.venv` from the
checked-in runtime options and requirements. Do not run an unversioned
`pulumi package add` command here because it can select a newer bridge. Keep
`Pulumi.yaml` and `requirements.txt` under source control. Keep the matching
release checksum beside the downloaded artifact while verifying it; `.venv`, `sdks`, `bin`, and
`Pulumi.<stack>.yaml` are local files and are ignored.

`PULUMI_CONFIG_PASSPHRASE` must come from a secret manager or protected CI
environment. The local backend encrypts stack secrets, but the passphrase and
the local state directory still need filesystem protection. Replace the
placeholder `secret-manager-read` command with your secret manager's CLI.

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
Use `pulumi import` with those IDs only after confirming the resource is owned
by this stack, then copy the generated protected resource definitions into the
program. Remove protection deliberately before destroying imported resources.

Keep the database and its owner identity in one clearly owned stack, or use an
explicit stack dependency and teardown order. Protect production databases and
owner service accounts, store Pulumi state in an encrypted backend, and do not
export raw access tokens. Pin both the Pulumi bridge and the wrapped provider;
the bridge version and provider version are independent.

The provider's service-account and access-token resources require an
organization admin token. A separate bootstrap stack is usually the clearest
ownership boundary: store the resulting writer token in a secret manager and
inject it into this data-plane stack. Pulumi `v3.261.0` was also verified with a
same-program bootstrap where `motherduck_access_token.token` was passed as an
`Output` to a second provider, which then created the database. If you use that
shape, keep both providers explicit and test it with the Pulumi version pinned
by your project.
