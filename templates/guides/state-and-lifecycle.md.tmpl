# State, imports, and lifecycle

Terraform stores desired infrastructure and the provider's observed metadata in
state. Each remote object should have one resource address and one owner state.
Managing the same object from two states creates conflicting plans.

## Credentials and state

`sensitive` hides values in normal CLI output; it does not encrypt state or plan
files. Use an encrypted remote backend with access controls and locking. Do not
commit state, saved plans, or credential files.

Access-token secrets are returned only at creation. Refresh preserves the
existing secret, while import cannot recover it. Importing a secret resource
also cannot recover its original credential-bearing creation SQL.

The Dive embed-session data source persists its credential in state and is
deprecated. For Terraform 1.10 or later, use the ephemeral resource where the
configuration accepts ephemeral values.

## Imports

Write a matching resource block before importing, using the import example on
that resource's reference page. Then run `terraform plan` and reconcile any
differences before applying. Terraform 1.5 or later also supports declarative
`import` blocks.

SQL object IDs use dots to separate database, schema, and object components.
Resource-name validators reject ambiguous dotted components. REST token IDs and
grant IDs use slash-separated components. Dives, Flights, and Guides use UUIDs.
Flight runs intentionally do not support import.

Imported table types use canonical server spellings. A configured equivalent
type such as `INT` is preserved during normal refresh. Review structural column
changes carefully because table changes use replacement.

## Destruction and drift

Remote deletion causes refresh to remove missing resources from state, allowing
Terraform to recreate configured objects. Permission and transport errors must
produce diagnostics, not masquerade as absence.

For important databases, consider `lifecycle { prevent_destroy = true }`.
It protects against planned destruction while the resource block remains in
configuration; it does not prevent manual deletion or protect an object after
its configuration is removed.

Schema destruction is restrictive by default. Enable `cascade` only when
Terraform is intended to own destruction of all contained objects. Snapshot
destruction removes the name from the snapshot; it does not promise immediate
physical deletion of retained historical data.

Flight definitions are durable configuration. Flight runs are explicit
operations: creating or replacing a run can execute Python again. Do not use
them for recurring schedules; configure the Flight's schedule instead.

## Troubleshooting

| Symptom | First check |
| --- | --- |
| Missing SQL token | Supply `MOTHERDUCK_TOKEN` to the Terraform process |
| Admin token required | REST administration needs `MOTHERDUCK_ADMIN_TOKEN` |
| SQL function unavailable | Confirm account permissions, region, and client support |
| Database fails during provider setup | Provider-level `database` must already exist |
| Unexpected replacement | Inspect the plan, imported defaults, and resource identity |
| Stuck or interrupted apply | Inspect remote state, recover the lock through the backend procedure, then plan again |

Never paste complete state, access tokens, share URLs, or Flight logs into an
issue. Report minimal configuration and redacted diagnostics.
