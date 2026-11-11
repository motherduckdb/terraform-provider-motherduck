---
page_title: "Give BI tools and AI agents their own readers"
subcategory: "Deployment architectures"
description: |-
  Publish one curated share and give each BI tool, application, and AI agent a separate read-only account.
---

# Give BI tools and AI agents their own readers

Suppose a dashboard tool, a notebook environment, and an AI agent all read the
same marts. Sharing the writer's token between them gives every consumer write
access, one compute pool, and one usage line.

This guide publishes the marts through one restricted share and gives each
consumer its own service account, read-scaling pool, and read-only token.

![A pipeline loads a marts database owned by one writer account. One restricted share publishes it. The share is granted to a separate reader account for dashboards, for notebooks, and for an AI agent. Each reader attaches the share once and its consumer queries on that reader's own read pool with a read-scaling token.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/bi-and-agent-readers.png)

## What a separate reader gives you

| Concern | Behavior |
| --- | --- |
| Read-only access | A read-scaling token cannot write, create databases, or attach and detach databases. MotherDuck enforces this, so it holds even when an agent's client offers a write tool |
| Cost attribution | Compute belongs to the account that runs the query. One account per consumer gives one usage line per consumer |
| Noisy neighbors | Each consumer has its own Duckling size and flock size |
| Revocation | Removing one consumer deletes its account and tokens and leaves the others untouched |
| Exposure | The share is the only path to the data |

A read-scaling token on the writer account is simpler and is enough for one BI
tool. See [build a layered warehouse](warehouse-examples.md). It reads every
database the writer owns and shares the writer's usage line, so move to separate
readers when a second consumer or an agent arrives.

For one database and one reader per customer of your product, use
[customer-facing analytics](customer-facing-analytics.md).

## Configuration

Run this root with the writer's `MOTHERDUCK_TOKEN` and with
`MOTHERDUCK_ADMIN_TOKEN` for the consumer accounts. Create the writer first with
the [writer bootstrap](blueprint-writer-bootstrap.md).

The organization needs a Business plan or an active free trial. Other plans
allow at most two service accounts and limit read-scaling Duckling sizes. See
[Duckling configuration](../resources/duckling_config.md) for the limits.

```terraform
# The provider's SQL token must belong to the writer that owns the marts.
# Only the owner of a share can grant it, and a service-account resource does
# not switch the SQL identity.
data "motherduck_current_user" "writer" {
  count = var.expected_writer_username == null ? 0 : 1
}

resource "motherduck_database" "marts" {
  name = "${var.name_prefix}_marts"

  lifecycle {
    precondition {
      condition     = var.expected_writer_username == null || one(data.motherduck_current_user.writer[*].value) == var.expected_writer_username
      error_message = "The provider SQL identity does not match expected_writer_username. The marts database must be created by the writer so it can grant its share."
    }
  }
}

resource "motherduck_schema" "reporting" {
  database = motherduck_database.marts.name
  name     = "reporting"
}

# Terraform defines the table. The pipeline that owns the marts loads it.
resource "motherduck_table" "daily_revenue" {
  database = motherduck_database.marts.name
  schema   = motherduck_schema.reporting.name
  name     = "daily_revenue"

  columns = {
    order_date  = "DATE"
    order_count = "BIGINT"
    revenue     = "DECIMAL(18,2)"
  }
  column_order = ["order_date", "order_count", "revenue"]
  not_null     = ["order_date"]
}

# One share publishes the curated marts. Every consumer reads the same data
# on its own compute, so the share is the single place to decide what leaves
# the warehouse.
resource "motherduck_share" "marts" {
  name            = "${var.name_prefix}_marts"
  source_database = motherduck_database.marts.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.daily_revenue]
}

# One service account per consumer. Compute is billed and limited per account,
# so each tool or agent gets its own usage line, its own Duckling size, and a
# credential that can be revoked without touching the others.
resource "motherduck_service_account" "consumer" {
  for_each = var.consumers

  username = "${var.name_prefix}_${each.key}"
}

resource "motherduck_duckling_config" "consumer" {
  for_each = var.consumers

  username                      = motherduck_service_account.consumer[each.key].username
  read_write_instance_size      = "standard"
  read_write_cooldown_seconds   = 60
  read_scaling_instance_size    = each.value.instance_size
  read_scaling_flock_size       = each.value.flock_size
  read_scaling_cooldown_seconds = 60
}

# A read-scaling token cannot attach a share. This one-hour read-write token
# exists only to attach the share once as the consumer.
resource "motherduck_access_token" "setup" {
  for_each = var.consumers

  username    = motherduck_service_account.consumer[each.key].username
  name        = "initial-share-attachment"
  description = "One-time share attachment for ${each.value.purpose}"
  token_type  = "read_write"
  ttl         = 3600
}

# The credential the tool or agent holds. MotherDuck rejects writes and
# administrative statements on a read-scaling token, so read-only access does
# not depend on the client behaving.
resource "motherduck_access_token" "reader" {
  for_each = var.consumers

  username    = motherduck_service_account.consumer[each.key].username
  name        = "terraform-reader"
  description = each.value.purpose
  token_type  = "read_scaling"
  ttl         = each.value.token_ttl_seconds
}

resource "motherduck_share_grant" "consumer" {
  for_each = var.consumers

  share    = motherduck_share.marts.name
  username = motherduck_service_account.consumer[each.key].username
}
```

Each entry in `consumers` sets a purpose, and optionally a Duckling size, a
flock size, and a token lifetime:

```hcl
consumers = {
  dashboards    = { purpose = "BI dashboards", flock_size = 2 }
  notebooks     = { purpose = "Analyst notebooks" }
  support_agent = { purpose = "AI agent over an MCP server", token_ttl_seconds = 604800 }
}
```

## Apply and attach

```shell
terraform init
terraform plan -var=expected_writer_username=svc_analytics_writer -out=readers.tfplan
terraform apply readers.tfplan
```

A grant alone does not attach a share, and a read-scaling token cannot attach
one. Connect once as each consumer with its entry in the `setup_tokens` output
and run:

```sql
ATTACH '<share_url>' AS marts;
```

The setup token expires after one hour. Store each entry of `reader_tokens` in
that consumer's secret store through automation. The consumer then queries
`marts.reporting.daily_revenue`.

## Connect an agent

Use the agent's read-scaling token as the access token of the MotherDuck MCP
server. See [connect with an access token](https://motherduck.com/docs/key-tasks/ai-and-motherduck/mcp-workflows/connect-with-access-token/)
and [restricting read-only MCP access](https://motherduck.com/docs/key-tasks/ai-and-motherduck/mcp-workflows/securing-read-only-access/).

Keep these boundaries for an agent:

- Publish only what the agent should see. Narrow the share with
  `include_pattern`, or filter rows and mask columns with
  [share secure views](row-and-column-security.md).
- Give the agent a short token lifetime and rotate it on a schedule.
- Give each agent its own account. A shared account hides which agent ran a
  query.

## Review usage per consumer

Read one account's usage with the
[motherduck_compute_usage](../data-sources/compute_usage.md) data source in a
separate read-only root:

```hcl
data "motherduck_compute_usage" "support_agent" {
  username   = "example_readers_support_agent"
  start_time = "2026-11-01T00:00:00Z"
}
```

## Lifecycle

- TTL does not schedule rotation. Create the replacement token, move the
  consumer to it, and then remove the old one. See
  [authentication](authentication.md).
- Changing a consumer's Duckling size or flock size updates it in place.
- Removing a consumer deletes its service account. Destroying the root also
  drops the marts database and its rows.

The complete root, with inputs, outputs, and teardown, is in
[examples/bi-and-agent-readers](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/bi-and-agent-readers).
