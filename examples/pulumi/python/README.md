# Pulumi Python

This is a small Pulumi program using Pulumi's Any Terraform Provider bridge with
the MotherDuck Terraform provider. It creates one database, schema, and table.

The recipe was verified with Pulumi `v3.261.0` and the MotherDuck provider
`v0.1.1` on macOS ARM64. The bridge package is pinned to `v1.4.0` in
`Pulumi.yaml`, and the Pulumi SDK is pinned to `3.261.0` in `requirements.txt`.

## Setup

Install Pulumi `v3.261.0`, then place the provider binary at
`bin/terraform-provider-motherduck`. The binary name and path are significant:
Pulumi requires a local provider path ending in `terraform-provider-<name>`.

Download the v0.1.1 artifact for your platform from the
[MotherDuck v0.1.1 GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases/tag/v0.1.1),
verify it against the matching `provider_<platform>_<arch>.sha256` file, unzip
it, and rename the executable to `bin/terraform-provider-motherduck`.

Initialize a local encrypted backend and generate the typed SDK:

```shell
pulumi login --local
export PULUMI_CONFIG_PASSPHRASE='use-a-password-manager'
pulumi stack init dev
pulumi package add terraform-provider ./bin/terraform-provider-motherduck
```

`pulumi package add` generates the local `pulumi_motherduck` SDK. Keep
`Pulumi.yaml`, `requirements.txt`, and the provider checksum under source
control; generated SDKs can be regenerated with `pulumi install`.

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
existing formats: `pulumi_import_db`, `pulumi_import_db.app`, and
`pulumi_import_db.app.facts` for a database, schema, and table respectively.
Use `pulumi import` with those IDs only after confirming the resource is owned
by this stack, then copy the generated protected resource definitions into the
program. Remove protection deliberately before destroying imported resources.

Keep the database and its owner identity in one clearly owned stack, or use an
explicit stack dependency and teardown order. Protect production databases and
owner service accounts, store Pulumi state in an encrypted backend, and do not
export raw access tokens. Pin both the Pulumi bridge and the wrapped provider;
the bridge version and provider version are independent.

The provider's service-account and access-token resources require an
organization admin token. Use a separate bootstrap stack for those resources,
store the resulting writer token in a secret manager, and inject that token
into this data-plane stack. If you experiment with deriving a provider token
from a same-program bootstrap output, verify the behavior with your Pulumi
version before adopting it because provider configuration timing can affect
whether the token is known early enough.
