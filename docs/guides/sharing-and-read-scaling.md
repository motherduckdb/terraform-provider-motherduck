---
page_title: "Share curated data and scale reads"
subcategory: "Operations"
description: |-
  Publish a restricted share, initialize its reader, and choose a read-scaling access pattern.
---

# Share curated data and scale reads

Choose the reader's identity before choosing its token. A read-scaling token on
a writer account can read the data visible to that writer. To expose only a
curated dataset, use a separate reader account and grant a restricted share.

| Pattern | Reader sees | Initial setup |
| --- | --- | --- |
| Writer's read-scaling token | Data available to the writer, including raw layers | Initialize the writer with a read-write connection |
| Separate reader plus curated share | The granted published data, plus anything else explicitly available to that reader | Initialize the reader and attach the share with a read-write token |

Both patterns use additional read-only compute. Neither changes which account
owns and writes the source database.

## 1. Prepare the reader

An administrator creates a service account, a temporary read-write setup token,
a read-scaling token for BI, and a `motherduck_duckling_config` for that reader.
Use the [customer-facing example](customer-facing-analytics.md) for a complete
configuration, or the resource references for
[tokens](../resources/access_token.md) and [compute](../resources/duckling_config.md).

## 2. Publish a curated database as its owner

In the writer root, reference the existing marts database and the intended reader
username. This example assumes that database already contains the published
tables:

```hcl
variable "marts_database" {
  type = string
}

variable "reader_username" {
  type = string
}

resource "motherduck_share" "reporting" {
  name            = "orders_reporting_prod"
  source_database = var.marts_database
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
}

resource "motherduck_share_grant" "reporting" {
  share        = motherduck_share.reporting.name
  username     = var.reader_username
  grantee_type = "user"
}

output "reporting_share_url" {
  value     = motherduck_share.reporting.url
  sensitive = true
}
```

Use a unique share name. `hidden` controls discoverability. `restricted` and
the grant determine access. Do not substitute an unrestricted share to work
around a missing grant.

For a role-based audience, grant the share with `grantee_type = "role"` and
assign the role separately. A role assignment alone does not attach the share
or grant write access to its source. See [share grants](../resources/share_grant.md).

## 3. Attach as the reader

Transfer the share URL securely to reader setup. Connect using the reader's
read-write token and run:

```sql
ATTACH '<reporting_share_url>' AS reporting;
SELECT * FROM reporting.main.daily_revenue LIMIT 10;
```

The URL is the computed `motherduck_share.reporting.url`, not the share name.
This initializes the account and saves its attachment. The provider currently
does not expose an attachment resource, so this step belongs to reader setup.

Then switch BI to the reader's read-scaling token. Verify that queries succeed,
writes fail, and raw databases or other tenants' data remain inaccessible.

## 4. Choose publication and freshness behavior

| Setting | Who publishes new source data | What to verify |
| --- | --- | --- |
| `update_mode = "automatic"` | MotherDuck publishes updates automatically | Consumer and replica synchronization delay |
| `update_mode = "manual"` | The writer pipeline runs `UPDATE SHARE` | A successful pipeline publishes only after its data checks |

For a manual share, the writer runs:

```sql
UPDATE SHARE orders_reporting_prod;
```

An already connected reader may need `REFRESH DATABASES`. A no-change Terraform
apply does not publish new rows to a manual share. Automatic publication and read
scaling are eventually consistent, so test freshness against your application
requirements. See [updating shares](https://motherduck.com/docs/key-tasks/sharing-data/updating-shares/)
and [read scaling](https://motherduck.com/docs/key-tasks/authenticating-and-connecting-to-motherduck/read-scaling/).

Keep served tables self-contained when consumers should not access source
layers. A physical mart avoids making a published view depend on a database
that the reader cannot access.

## 5. Scale the right account

Configure instance size, read-pool limit, and cooldown on the account used by BI.
Changing the writer's compute settings does not resize a separate reader's pool.
The pool limit is a maximum, not a promise that every replica stays running.

Measure representative concurrency and query latency before choosing a size.
For supported values and deletion behavior, see
[Duckling configuration](../resources/duckling_config.md).
