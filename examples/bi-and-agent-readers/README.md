# BI tools and AI agents as separate readers

A writer owns a curated marts database and publishes it through one restricted
share. Each BI tool, application, or AI agent gets its own service account,
read-scaling pool, and read-only token.

Use this layout when several consumers read the same warehouse and you need to
answer three questions per consumer: what it can read, what it costs, and how
to cut it off.

| Concern | How this example answers it |
| --- | --- |
| Read-only access | Consumers hold a read-scaling token. MotherDuck rejects writes and administrative statements on it, so an agent cannot change data even when its client offers a write tool |
| Cost attribution | Compute belongs to the account that runs the query. One account per consumer gives one usage line per consumer |
| Noisy neighbors | Each consumer has its own Duckling size and flock size. A heavy dashboard does not slow the agent |
| Revocation | Removing a key from `consumers` deletes that account and its tokens. The other consumers are unaffected |
| Exposure | The share is the only path to the data. Narrow it with `include_pattern`, or filter rows and mask columns with [share secure views](../../docs/guides/row-and-column-security.md) |

For one database and one reader per customer, use
[customer-facing analytics](../customer-facing-analytics/README.md) instead.

## Prerequisites

- Terraform 1.5 or later and a MotherDuck organization with read scaling.
- A Business plan or an active free trial. Other plans allow at most two
  service accounts and do not allow Standard read-scaling Ducklings.
- An existing writer, created in the separate
  [writer-bootstrap root](../blueprints/writer-bootstrap/README.md).
- The writer's read-write token injected as `MOTHERDUCK_TOKEN`.
- `MOTHERDUCK_ADMIN_TOKEN` for consumer accounts, tokens, and compute configuration.
- Independent state, protected because it contains the consumer credentials.

## Apply

Choose an unused prefix. Set the expected username to your actual writer:

```shell
terraform init
terraform plan -var=expected_writer_username=svc_analytics_writer -out=readers.tfplan
terraform apply readers.tfplan
terraform plan -var=expected_writer_username=svc_analytics_writer -detailed-exitcode
```

The default three consumers create 19 managed resources. Expect the final plan
to return 0.

## Attach the share once per consumer

A grant alone does not attach a share, and a read-scaling token cannot attach
one. Connect as each consumer with its entry in `setup_tokens` and run:

```sql
ATTACH '<share_url>' AS marts;
```

The setup token expires after one hour. From then on the consumer uses only its
entry in `reader_tokens` and queries `marts.reporting.daily_revenue`. Move both
outputs to the consumer's secret store through automation. Do not print them.

## Connect a BI tool or an agent

Give each tool the read-scaling token for its own account:

- A BI tool or notebook uses it as the MotherDuck token in its connection.
- An agent uses it as the access token of the MotherDuck MCP server. See
  [connect with an access token](https://motherduck.com/docs/key-tasks/ai-and-motherduck/mcp-workflows/connect-with-access-token/)
  and [restricting read-only MCP access](https://motherduck.com/docs/key-tasks/ai-and-motherduck/mcp-workflows/securing-read-only-access/).

Do not share one token between tools. A shared token hides which consumer ran a
query and forces every consumer to rotate together.

## Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `name_prefix` | `example_readers` | Unique prefix for the database, share, and accounts |
| `consumers` | `dashboards`, `notebooks`, `support_agent` | One entry per consumer with `purpose`, and optional `instance_size`, `flock_size`, and `token_ttl_seconds` |
| `expected_writer_username` | `null` | Set for live use to require the intended SQL owner. Null permits offline example plans |

## Outputs

| Output | Sensitive | Use |
| --- | --- | --- |
| `consumers` | No | Username, purpose, compute settings, and attached relation per consumer |
| `share_name` | No | Share that every consumer reads |
| `share_url` | Yes | First share attachment |
| `setup_tokens` | Yes | Read-write setup credentials, valid for one hour |
| `reader_tokens` | Yes | Read-scaling credentials for each consumer |

## Review usage per consumer

Read usage for one account with the
[motherduck_compute_usage](../../docs/data-sources/compute_usage.md) data source
in a separate read-only root:

```hcl
data "motherduck_compute_usage" "support_agent" {
  username   = "example_readers_support_agent"
  start_time = "2026-11-01T00:00:00Z"
}
```

## Lifecycle

TTL does not schedule rotation. Create the replacement token, move the consumer
to it, and only then remove the old one. The default agent token lasts seven
days and the others 30 days.

The Duckling sizes are illustrative. Measure each workload before choosing
production sizes. Changing `instance_size` or `flock_size` updates the
configuration in place.

Removing a consumer deletes its service account. Destroying the root also drops
the marts database and its rows. For disposable cleanup, detach the share as
each consumer, destroy this root as the writer with the admin credential
available, then destroy the writer bootstrap last.
