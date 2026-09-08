---
page_title: "Authentication and account ownership"
subcategory: "Operations"
description: |-
  Select SQL and admin credentials and bootstrap a dedicated writer identity.
---

# Authentication and account ownership

Choose credentials for the job each Terraform root performs. Warehouse objects
belong to the account authenticated by the SQL token. Organization administration
uses a separate REST credential.

## Select the credential

| Root manages | Required environment | Account requirement |
| --- | --- | --- |
| Databases, schemas, tables, views, shares, secrets, snapshots | `MOTHERDUCK_TOKEN` | Read-write token for the intended owner |
| SQL roles and role assignments | `MOTHERDUCK_TOKEN` | SQL identity with the required role-administration permissions |
| Service accounts, tokens, Duckling settings | `MOTHERDUCK_ADMIN_TOKEN` | Organization admin credential accepted by the REST API |
| Tenant databases plus reader provisioning | Both | Writer SQL token and organization admin credential |

Use `provider "motherduck" {}` to read the environment. Explicit `token` and
`admin_token` arguments override their respective environment defaults. Prefer
secret-manager injection so credentials do not appear in source or shell history.
See [MotherDuck authentication](https://motherduck.com/docs/key-tasks/authenticating-and-connecting-to-motherduck/authenticating-to-motherduck/)
for obtaining a SQL token and the
[REST API overview](https://motherduck.com/docs/sql-reference/rest-api/)
for organization administration.

A read-scaling token is for application reads. It cannot create or update your
warehouse, or attach a share for the first time.

## Bootstrap a writer

Use the [writer-bootstrap example](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/blueprints/writer-bootstrap)
from an admin-only root with its own state. It creates a service account and a
read-write token. Supply `writer_username`, review the plan, and apply.

Transfer the sensitive `writer_token` output to the secret manager through your
approved automation. The next Terraform root receives that value as
`MOTHERDUCK_TOKEN`. It does not need the admin credential unless it also manages
REST resources.

Do not feed a token resource from the same apply into the provider configuration.
Terraform configures its provider before creating resources, so that value would
be unknown when the connection is needed.

## Check the SQL identity before creation

Use a data source and a resource precondition when the identity must match a
known writer. Add this to a root with an existing provider configuration:

```hcl
variable "expected_writer_username" {
  type = string
}

data "motherduck_current_user" "writer" {}

resource "motherduck_database" "warehouse" {
  name = "orders_prod"

  lifecycle {
    precondition {
      condition     = data.motherduck_current_user.writer.value == var.expected_writer_username
      error_message = "Connect with the intended warehouse writer token."
    }
  }
}
```

Set the expected value to the service-account username. This is a check of the
connected SQL session, not a mechanism for switching accounts.

## Rotate and retire credentials

Token secrets are returned once, at creation. Import recovers metadata but
cannot recover the original secret. Keep bootstrap state restricted even when
all token outputs are marked sensitive.

Terraform does not rotate tokens on a timer. For finite-TTL tokens, schedule a
rotation workflow before expiration. Create a second token, update and verify
all consumers, then revoke the previous token. Replacing a token directly can
interrupt consumers that still use its old value.

The writer-bootstrap module uses a 30-day default TTL. The warehouse bootstrap
example deliberately omits TTL and requires explicit revocation. Read the chosen
example's outputs and defaults before adopting it.

## Troubleshoot the connection

| Symptom | Check |
| --- | --- |
| SQL token is missing | Inject `MOTHERDUCK_TOKEN` into the actual Terraform process |
| REST resource requests an admin token | A normal SQL token does not replace `MOTHERDUCK_ADMIN_TOKEN` |
| Objects appear under the wrong account | Compare `motherduck_current_user.value` with the intended writer |
| Initialization cannot find a database | Provider-level `database` must already exist. Leave it unset when creating databases |
| An account cannot read a granted share | Bootstrap the account and attach the share with its read-write token |

For state recovery and imports, continue with [state and lifecycle](state-and-lifecycle.md).
