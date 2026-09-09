---
page_title: "State, imports, and lifecycle"
subcategory: "Operations"
---

# State, imports, and lifecycle

See [resource scope](resource-scope.md) for the recommended infrastructure-first
workflow, experimental content resources, and Flight-run deprecation.

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

## Dives and missing data dependencies

Dive source is stored by the service without JSX compilation during Terraform
apply. Invalid JSX can therefore deploy successfully and fail when opened.
Compile or preview Dive content in the application deployment workflow, and open
the saved Dive to verify rendering before treating the deployment as healthy.

A successful plan checks managed objects, not whether every SQL query inside a
Dive or Flight will run. If a Dive references an existing database only through
its source code, dropping that database can leave a no-op Terraform plan while
the Dive query and the next Flight run fail. Manage the database and table as
resources, or validate externally managed dependencies in your deployment
pipeline before publishing application changes.

Recreating a managed database or table restores its configured structure. It
does not restore rows loaded by a previous Flight. `depends_on` orders Terraform
operations but does not rerun an already completed Flight run when a table is
recreated. Run the pipeline again after structural repair, then check its output.
For an existing deprecated `motherduck_flight_run` resource, an explicit
`-replace` or `replace_triggered_by` can request another execution. Prefer an
explicit deployment pipeline for new work.

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
