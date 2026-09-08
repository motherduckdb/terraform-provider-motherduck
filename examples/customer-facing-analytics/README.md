# Customer-facing usage analytics

A central writer owns one database per tenant. Each tenant gets a restricted,
automatically updated share, a separate reader account, and a read-scaling pool.
The example creates an empty `app.daily_usage` table in each database.

Follow the [deployment guide](../../docs/guides/customer-facing-analytics.md)
for the architecture, sample rows, initial reader attachment, backend routing,
verification, and teardown.

## Prerequisites

- Terraform 1.5 or later and a MotherDuck organization with read scaling.
- An existing writer, created in the separate
  [writer-bootstrap root](../blueprints/writer-bootstrap/README.md).
- The writer's read-write token injected as `MOTHERDUCK_TOKEN`.
- `MOTHERDUCK_ADMIN_TOKEN` for reader accounts, tokens, and compute configuration.
- Independent state, protected because it contains the reader credentials.

## Apply

Choose an unused prefix. Set the expected username to your actual writer:

```shell
terraform init
terraform plan -var=expected_writer_username=svc_cfa_writer -out=tenants.tfplan
terraform apply tenants.tfplan
terraform plan -var=expected_writer_username=svc_cfa_writer -detailed-exitcode
```

The default two tenants create 18 managed resources. The identity check performs
one live SQL read. Expect the final plan to return 0. Distribute the outputs
through approved secret automation rather than printing credentials.

Before an application can query, connect as each reader with its setup token and
attach the corresponding share as `reporting`. A grant alone does not attach it.
The app then uses only the reader's read-scaling token.

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `name_prefix` | `example_cfa` | Unique prefix for databases, shares, and accounts |
| `tenants` | `["acme", "globex"]` | Stable tenant IDs, each a lowercase identifier |
| `expected_writer_username` | `null` | Set for live use to require the intended SQL owner. Null permits offline example plans |

## Outputs

| Output | Sensitive | Use |
| --- | --- | --- |
| `tenants` | No | Database, share, username, and attached relation per tenant |
| `share_urls` | Yes | Initial reader attachment |
| `reader_setup_tokens` | Yes | Read-write setup credentials, valid for one hour |
| `reader_tokens` | Yes | Backend read-scaling credentials, valid for 30 days |

## Lifecycle

TTL does not schedule Terraform rotation. Arrange overlapping application-token
rotation before expiration. An expired setup token may be recreated by a later
apply when it is absent from the API listing, so keep this privileged root
restricted even after initial setup.

The example sets one Standard read replica per tenant as an illustrative small
configuration. Measure your workload before choosing production sizes or limits.
The writer is shared across tenants. It must place each tenant's rows in the
right database and keep its credentials out of the application.

Removing a tenant from `tenants` destroys its database and reader account.
Changing table columns replaces the table. Add reviewed data protection and
offboarding policies before production. For disposable cleanup, detach shares
as readers, destroy this root as the writer with the admin credential available,
then destroy the writer bootstrap last.
