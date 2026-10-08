# Pulumi Python

This is a small Pulumi program using Pulumi's Any Terraform Provider bridge with
the MotherDuck Terraform provider. It creates one database, schema, and table.

The recipe pins Pulumi `v3.265.0`, the bridge package `v1.4.0`, and the
MotherDuck provider `0.3.3` in `Pulumi.yaml` and `requirements.txt`. The
sample database name is required configuration so each stack can use a unique
name. Read [Use the provider with Pulumi](../../../docs/guides/pulumi.md) for
the differences from Terraform before adapting it.

## Setup

Install Pulumi `v3.265.0`. `pulumi install` downloads the provider from the
Terraform Registry and verifies its checksum and publisher signature, so no
manual download is needed. To run a provider binary you built or verified
yourself, replace the two `parameters` entries in `Pulumi.yaml` with a local
path ending in `terraform-provider-motherduck`, such as
`./bin/terraform-provider-motherduck`.

Initialize a local backend and install the pinned bridge and generated SDK:

```shell
pulumi login --local
# Inject PULUMI_CONFIG_PASSPHRASE through your secret manager first.
pulumi stack init dev
pulumi config set databaseName pulumi_example_myteam_dev
pulumi install
```

`pulumi install` reads the pinned `terraform-provider` package declaration,
installs the pinned provider, generates the local `pulumi_motherduck` SDK, and creates `.venv` from the
checked-in runtime options and requirements. Do not run an unversioned
`pulumi package add` command here because it can select a newer bridge. Keep
`Pulumi.yaml` and `requirements.txt` under source control. `requirements.txt`
pins every transitive Python dependency and documents how to regenerate it. `.venv`, `sdks`, `bin`, and
`Pulumi.<stack>.yaml` are local files and are ignored.

`PULUMI_CONFIG_PASSPHRASE` must come from a secret manager or protected CI
environment. The local backend encrypts stack secrets, but the passphrase and
the local state directory still need filesystem protection. Choose a database name unique to this stack before running the example.

## Lifecycle

Inject `MOTHERDUCK_TOKEN` into the environment from your secret manager, never
by typing it into the shell, where it lands in history. Wrapping it in
`pulumi.Output.secret` keeps the provider configuration and dependent values
secret in Pulumi state and UI output.

```shell
# MOTHERDUCK_TOKEN must already be set by your secret manager.
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
