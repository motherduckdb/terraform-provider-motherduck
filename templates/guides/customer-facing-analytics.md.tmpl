---
page_title: "Serve customer-facing analytics"
subcategory: "Deployment architectures"
description: |-
  Provision tenant databases, restricted shares, and separate readers for an application backend.
---

# Serve customer-facing analytics

Suppose Acme and Globex use your SaaS application and each needs a usage
dashboard. A central pipeline writes their metrics. Your backend authenticates
the user and queries through that customer's reader account.

This guide deploys one database and restricted share per customer, plus a reader
account and read-scaling pool for each. The writer owns all tenant databases.
The app never needs its token.

![A central writer owns Acme and Globex databases. Each database has a separate restricted share and reader pool. The authenticated backend chooses the matching reader credential.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/customer-facing-analytics.png)

## What the example isolates

| Boundary | Behavior |
| --- | --- |
| Data | Each reader is granted its own tenant share |
| Reader compute | Different reader accounts have separate Ducklings and read pools |
| Writes | One central writer handles all tenants and can access every tenant database |
| Application sessions | Your backend authenticates the user and selects the tenant credential |

A database per tenant does not isolate the shared writer's compute. If tenant
writes also need independent resources, provision a separate owner and deploy
that tenant's data as that identity. See
[MotherDuck's customer-facing architecture](https://motherduck.com/docs/key-tasks/customer-facing-analytics/3-tier-cfa-guide/).

## 1. Bootstrap the writer

Apply the [writer-bootstrap example](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/blueprints/writer-bootstrap)
in its own state with `MOTHERDUCK_ADMIN_TOKEN` and
`writer_username = "svc_cfa_writer"`. Transfer its generated token to a protected
secret-manager entry.

For the next root, inject that token as `MOTHERDUCK_TOKEN`. Also supply
`MOTHERDUCK_ADMIN_TOKEN` because the example provisions the reader accounts.
For teams with separate platform administrators, split reader provisioning into
an admin state and pass usernames to the writer root.

## 2. Provision the tenants

Copy the complete [customer-facing analytics example](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/customer-facing-analytics)
into a new root with independent, protected state. Its default tenant IDs are
`acme` and `globex`. Choose an unused prefix before applying.

```shell
terraform init
terraform plan -var=expected_writer_username=svc_cfa_writer -out=tenants.tfplan
terraform apply tenants.tfplan
terraform plan -var=expected_writer_username=svc_cfa_writer -detailed-exitcode
```

The plan creates 18 managed resources, nine per tenant. The last command should
return 0. The identity precondition prevents accidental creation under your
personal account.

The publication boundary uses explicit restricted access and automatic updates:

```hcl
resource "motherduck_share" "tenant" {
  for_each        = var.tenants
  name            = "${var.name_prefix}_share_${each.key}"
  source_database = motherduck_database.tenant[each.key].name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
  depends_on      = [motherduck_table.daily_usage]
}

resource "motherduck_share_grant" "reader" {
  for_each     = var.tenants
  share        = motherduck_share.tenant[each.key].name
  username     = motherduck_service_account.reader[each.key].username
  grantee_type = "user"
}
```

This is an excerpt from the example, not a second configuration to apply.
Use stable internal IDs as tenant keys. Renaming a key is a lifecycle change,
and removing it plans destruction of the tenant's resources.

## 3. Load a distinguishable sample

As the writer, run this once against an empty disposable deployment using the
default prefix:

```sql
INSERT INTO example_cfa_acme.app.daily_usage (usage_date, event_count)
VALUES (DATE '2026-01-01', 42);

INSERT INTO example_cfa_globex.app.daily_usage (usage_date, event_count)
VALUES (DATE '2026-01-01', 17);
```

Terraform creates the tables. Your pipeline owns the rows and must route each
customer's records into the correct database. Do not use these inserts as a
repeatable production ingestion job.

## 4. Initialize each reader and attach its share

The outputs contain three sensitive maps keyed by tenant: `share_urls`,
`reader_setup_tokens`, and `reader_tokens`. Transfer them through protected
automation. Do not log the values or make the full Terraform state available to
the serving application.

For each tenant, open a SQL connection with its **setup read-write token** and
execute:

```sql
ATTACH '<this_tenants_share_url>' AS reporting;
SELECT usage_date, event_count FROM reporting.app.daily_usage;
```

Replace the placeholder with that tenant's URL from the output. The attachment
is saved for the reader account. A grant does not perform this step, and a
read-scaling token cannot attach a share. Use the one-hour setup token only for
this initialization.

Automatic share publication and read-replica synchronization have delay. Poll
until the expected sample appears rather than assuming an immediate result.

## 5. Route requests through your backend

Store each `reader_tokens` value in your backend secret store under its stable
tenant ID. On every request:

1. Authenticate the user and derive the tenant from trusted session metadata.
2. Resolve that tenant's reader credential on the server.
3. Use a connection or pool dedicated to that tenant identity.
4. Query `reporting.app.daily_usage` with parameterized application filters.
5. Return the result to the browser.

Do not select a credential from an unchecked request parameter. Do not reuse a
connection authenticated for Acme to answer Globex requests. Both readers can
use the same `reporting` alias because their account attachments differ.

The long-lived reader tokens stay in the backend. For a browser-embedded Dive,
use the separate [Dive embedding mechanism](../ephemeral-resources/dive_embed_session.md)
and its session lifecycle. Terraform is not a per-request session issuer.

## 6. Verify the access boundary

Test using the actual application credentials, not the writer token:

| Check | Expected result |
| --- | --- |
| Acme queries `reporting.app.daily_usage` | One sample row with `event_count = 42` |
| Globex queries the same relation name | One sample row with `event_count = 17` |
| Either reader attempts `INSERT` | Denied |
| Acme's setup identity attempts to attach Globex's restricted share | Denied |
| A backend request changes a tenant parameter without changing its authorized identity | Still routed to the authorized tenant, or rejected |

The cross-tenant attachment test uses the setup credential because read-scaling
credentials reject all attachment operations. It verifies the grant boundary,
not just the token's read-only mode.

The example is validated and planned offline in repository checks. Live access
and freshness must be verified in the target organization with these identities.

## Operate and offboard

Reader tokens last 30 days in this example. Arrange rotation before expiration,
update the backend secret store, and verify new connections before revoking old
tokens. Terraform does not run scheduled rotation by itself.

Setup tokens last one hour. An expired token may be recreated by a later apply
when the API no longer lists it. Restrict this Terraform root throughout its
lifetime. For a fresh attachment after expiry, create a new setup credential
through the admin workflow.

Each reader pool starts at one Standard replica with a 60-second cooldown.
Measure concurrency and freshness requirements before changing these defaults.

For offboarding, disable the backend route, stop writes, retain or export data
as required, and review the tenant removal plan. For disposable teardown, detach
shares as readers using read-write credentials, destroy this root with the writer
and admin credentials, then destroy writer bootstrap last. Protect production
databases and tables before adopting this example for real customer data.
