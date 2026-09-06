# Resource scope and migration

The recommended default is Terraform for infrastructure and access controls,
with application deployment tooling for code and content.

| Surface | First-release posture |
| --- | --- |
| Databases, schemas, tables/views, shares, roles/grants, secrets, service accounts, tokens, Duckling configuration, snapshots | Core infrastructure; consult each resource's lifecycle constraints |
| Flight definitions and schedules | Optional Terraform ownership; use only when Terraform is the authoritative deployment owner |
| Dive and Guide resources | Experimental, outside the stable provider support commitment |
| Flight-run resource | Deprecated; use CLI/SQL or a deployment pipeline to execute runs |
| Catalog data sources | Read-only integration with objects managed by either workflow; availability depends on the service |

Experimental resources remain registered for compatibility. This label is not a
promise that breaking changes will occur without notice; any migration or removal
must be documented. It also does not change the service's own availability status.

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
