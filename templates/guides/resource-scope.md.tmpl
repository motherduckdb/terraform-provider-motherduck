---
page_title: "Resource scope and migration"
subcategory: "Operations"
---

# Resource scope and migration

> **Deployment recommendation:** Use Terraform for databases, service accounts,
> access tokens, Duckling configuration, roles/grants, and shares. Keep Dives,
> Flight Python, and Guide Markdown in version control and deploy them through
> the MotherDuck CLI or a code deployment pipeline. Terraform apply does not
> compile application code, validate every data dependency, or run a data load.

Terraform's identity resources provision service accounts and manage tokens and
role assignments. They do not invite or delete human users. Manage human account
membership through MotherDuck's supported organization administration workflow.

| Surface | Deployment posture |
| --- | --- |
| Databases, schemas, tables/views, shares, roles/grants, secrets, service accounts, tokens, Duckling configuration, snapshots | Core infrastructure; consult each resource's lifecycle constraints |
| Flight definitions and schedules | Prefer CLI/code deployment; Terraform remains available when it is the sole owner |
| Dive and Guide resources | Experimental, outside the stable provider support commitment |
| Flight-run resource | Deprecated; use CLI/SQL or a deployment pipeline to execute runs |
| Catalog data sources | Read-only integration with objects managed by either workflow; availability depends on the service |

Experimental resources remain registered for compatibility. This label is not a
promise that breaking changes will occur without notice; any migration or removal
must be documented. It also does not change the service's own availability status.

## CLI and code deployment

Use the MotherDuck CLI for local file-based Dive, Flight, and Guide workflows, or
[Blueprints](blueprints-deployment.md) for application packages and release
pipelines. The CLI's `dive`, `flight`, and `guide` commands support local source
and metadata files with pull/push workflows. Scripts can also use the public
SQL/MCP functions. Check the installed CLI's help before choosing flags.

Validate Python and dependencies, preview Dive code, and check Guide references
before deployment. After deployment, verify the saved version, execute a Flight
when required, and query the resulting data. A successful definition update is
not proof that a Flight ran or a Dive rendered.

Pass Terraform database names, share identifiers, and service-account names to
the application pipeline. A missing externally managed database can leave a
no-op Terraform plan, and recreating a managed table restores structure without
reloading its rows. See [application lifecycle behavior](state-and-lifecycle.md).

## One owner per object

Keep Python, UI code, and Markdown in version control. Deploy them with a
pipeline that performs their application-specific validation. Pass provisioned
database/share identifiers into that pipeline. Avoid having Terraform and an
interactive editor or another deployment system overwrite the same content.

Teams choosing Terraform for Flight definitions should use a separate workload
state when its owners or release cadence differ from platform infrastructure.
Creating a definition and configuring its schedule are distinct from executing it.

Dive mounted resources cannot currently be reconstructed by this implementation
on refresh/import. Do not rely on complete drift detection for that field.
Guide audience management also belongs to the same authoritative owner as its
content until a separately managed, non-overlapping permissions surface exists.
The public Guide access functions currently document user/private and
organization audiences. Role audiences remain compatibility fields in the
provider schema and require the account to expose the Guide grantee functions;
check function availability before using `access = "role"` or
`motherduck_guide_grantees`.

## Move existing content out of Terraform

Back up state securely and export the object's definition using an authenticated
service interface. Establish the new deployment owner and retain the remote ID.
Remove Terraform ownership without destroying the remote object, then remove
the resource configuration and review a plan before applying.

On Terraform versions supporting `removed` blocks, use `lifecycle { destroy =
false }`. On older supported versions, use `terraform state rm RESOURCE_ADDRESS`
with a locked, backed-up state. These operations do not deploy the replacement
workflow for you. Verify that the next Terraform plan proposes neither
recreation nor deletion before proceeding.

For deprecated Flight runs, removing the resource through normal destroy can
request cancellation when `cancel_on_destroy` is true. To forget the run without
that side effect, remove state ownership as above and delete its configuration.
Future runs should be triggered by the execution workflow.

Wait-policy changes on an existing run no longer cause replacement. They update
stored settings only; they do not execute a new run or restart waiting. A changed
Flight ID or run configuration still means a new execution.
