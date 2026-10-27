# Pulumi Python

A Pulumi program that runs the MotherDuck Terraform provider through Pulumi's
Any Terraform Provider bridge. It creates one database, schema, and table.

The recipe pins Pulumi `v3.271.0`, the bridge package `v1.5.0`, and the
MotherDuck provider `0.3.8` in `Pulumi.yaml` and `requirements.txt`. The
sample database name is required configuration so each stack can use a unique
name. Read [Use the provider with Pulumi](../../../docs/guides/pulumi.md) for
the differences from Terraform before adapting it.

## Setup

Install Pulumi `v3.271.0`. `pulumi install` downloads the provider from the
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
the local state directory still need filesystem protection.

## Lifecycle

Inject `MOTHERDUCK_TOKEN` into the environment from your secret manager, never
by typing it into the shell, where it lands in history. Wrapping it in
`pulumi.Output.secret` keeps the provider configuration and dependent values
secret in Pulumi state and UI output. The table sets `delete_before_replace`
because its name stays fixed across a column change.

```shell
# MOTHERDUCK_TOKEN must already be set by your secret manager.
pulumi preview
pulumi up
pulumi refresh
pulumi preview                 # should report no changes
pulumi destroy
```

Import IDs match the Terraform provider: `<database>`, `<database>.<schema>`,
and `<database>.<schema>.<table>` for a database, schema, and table.
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
