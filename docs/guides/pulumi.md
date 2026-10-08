---
page_title: "Use the provider with Pulumi"
subcategory: "Operations"
description: |-
  Run the MotherDuck provider from Pulumi through the Any Terraform Provider bridge, and plan for the differences from Terraform.
---

# Use the provider with Pulumi

Pulumi runs this provider through its
[Any Terraform Provider](https://www.pulumi.com/docs/iac/concepts/providers/any-terraform-provider/)
bridge, the `terraform-provider` package. There is no separate Pulumi
provider and no Pulumi Registry page. The generated SDK carries the same
descriptions as these docs, so use the resource and data source pages here as
the reference.

Every resource and every data source maps to Pulumi. One ephemeral resource
does not, and a few behaviors differ from Terraform. Both are described below.

## Install

Declare the bridge and the provider in `Pulumi.yaml`, pinning both versions:

```yaml
packages:
  motherduck:
    source: terraform-provider
    version: 1.4.0
    parameters:
      - registry.terraform.io/motherduckdb/motherduck
      - 0.3.3
```

Then run `pulumi install`. For Python, TypeScript, Go, and .NET programs this
also generates the local `motherduck` SDK. The bridge downloads the provider
from the Terraform Registry and verifies its checksum and publisher signature,
the same checks `terraform init` runs.

Keep the `registry.terraform.io/` prefix. The bridge looks in the OpenTofu
Registry by default, where this provider is not listed. Pin the bridge and the
provider separately, because their versions are independent.

The [Pulumi Python example](examples.md) is a complete, pinned project.

## Credentials

The provider reads `MOTHERDUCK_TOKEN` and `MOTHERDUCK_ADMIN_TOKEN` from the
environment. Injecting them from a secret manager keeps both tokens out of
Pulumi state. If you set `token` or `adminToken` on an explicit provider
resource instead, Pulumi stores them as secrets in state.

## Names in Pulumi

- Resources keep their names without the `motherduck_` prefix. For example,
  `motherduck_role_grant` is `motherduck.RoleGrant` in Python and
  `motherduck:RoleGrant` in Pulumi YAML.
- Data sources become functions with a `get` prefix. For example,
  `data.motherduck_database` is `motherduck.get_database` in Python and
  `motherduck:getDatabase` in Pulumi YAML.
- Arguments use the language's casing, such as `source_database` in Python
  and `sourceDatabase` in YAML and TypeScript. Some list arguments are
  pluralized: the share's `include_pattern` is `includePatterns`. The
  generated SDK shows the exact names.

## Differences from Terraform

### Generated names

Terraform requires `name` on resources that have one. Pulumi makes it optional
and, when it is omitted, generates `<logical name>-<7 random characters>`, for
example `analytics-a704189`. The hyphen means SQL must quote the name. Set
`name` explicitly for databases, schemas, tables, views, secrets, and shares
that SQL, dbt, or applications refer to.

### Replacements

Pulumi creates a replacement before it deletes the original. When the
replacement keeps the same name, the create fails with an error such as
`Table with name "facts" already exists`, and the original is left unchanged.
Changing a table's `columns` is a common example.

Set the `deleteBeforeReplace` resource option on resources whose name stays
fixed across a replacement:

```yaml
resources:
  facts:
    type: motherduck:Table
    properties:
      database: analytics
      schema: main
      name: facts
      columns:
        id: INTEGER
    options:
      deleteBeforeReplace: true
```

Deleting first drops the table and its data before the new one is created,
just as `terraform apply` does. Review replacements, shown as `+-` in
`pulumi preview`, before applying them. Renaming an access token creates the
new token before the old one is revoked, which suits rotation.

### Functions during preview

Pulumi calls a function during `pulumi preview` and `pulumi up` whenever its
arguments are known, even with `dependsOn`. A function that reads a resource
created in the same program by a literal name therefore fails before the
resource exists. Terraform defers that read until apply. In Pulumi, read the
resource's own outputs, or pass a value that is only known after creation,
such as its `id`:

```yaml
variables:
  flightInfo:
    fn::invoke:
      function: motherduck:getFlight
      arguments:
        flightId: ${flight.id}
```

Functions fit objects that another stack or team owns, such as the
`motherduck:getSecret` lookup for a separately owned secret.

### Sensitive function results

The bridge returns function results as plain values
([pulumi/pulumi#12710](https://github.com/pulumi/pulumi/issues/12710)).
Data source attributes that Terraform marks sensitive, such as `rows_json` and
the Dive embed `session`, are not Pulumi secrets. Wrap them with
`pulumi.secret()` in your language, or `fn::secret` in YAML, before you export
them or pass them to a resource. Resource attributes such as an access token's
`token` remain secrets.

### Ephemeral resources

The bridge does not implement ephemeral resources, so the
`motherduck_dive_embed_session` ephemeral resource is not available. Use the
`getDiveEmbedSession` function instead. It creates a short-lived session each
time the program runs, and its `session` is a plain value, as described above.
It is stored in Pulumi state only if you export it or pass it to a resource.

### Timeouts

Set operation timeouts with the `timeouts` argument on `Database` and
`Snapshot`, using Go duration syntax such as `10m`, rather than the
`customTimeouts` resource option.

## Import and refresh

`pulumi import` takes the same IDs as `terraform import`. Each resource page
lists its ID format. For example:

```shell
pulumi import motherduck:index/schema:Schema analytics_mart analytics.mart
```

Pulumi prints the program code for the imported resource. Add it to your
program before the next `pulumi up`, or Pulumi deletes the object.

`pulumi refresh` reads the current remote state, like the refresh in
`terraform plan`. Run `pulumi preview --expect-no-changes` afterwards to check
for drift. Do not manage the same object from both Terraform and Pulumi.

## What the project tests

- Every pull request generates the Pulumi schema through the pinned bridge and
  fails if a resource or data source does not map. It also previews the Python
  example.
- Every change to `main` runs a live Pulumi lifecycle against MotherDuck:
  create, refresh, an empty preview, in-place updates, a `deleteBeforeReplace`
  replacement, import, and destroy. It covers databases, schemas, tables,
  views, secrets, shares, snapshots, Flights, Guides, and Dives, and with an
  admin token also service accounts, access tokens, Duckling configuration,
  roles, and role grants.
- `motherduck_share_grant` and the deprecated `motherduck_flight_run` map to
  Pulumi but do not have a live Pulumi test.
