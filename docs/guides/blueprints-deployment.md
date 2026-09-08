---
page_title: "Deploy pipelines and dashboards with Blueprints"
subcategory: "Deployment architectures"
description: |-
  Combine Terraform-owned infrastructure with MotherDuck Blueprints application deployments.
---

# Deploy pipelines and dashboards with Blueprints

Use Terraform for the platform your data application runs on, and
[MotherDuck Blueprints](https://github.com/motherduckdb/motherduck-blueprints)
for the Python, UI, and Markdown that you release together. This gives
infrastructure and application changes their own review and deployment cycles.

A useful example is Wikipedia pageviews: a Flight ingests public data and a
Dive presents it. The Blueprints starter already includes this workload.

![Terraform provisions identities and compute. Blueprints deploys application packages to previews and staging with a staging account, then promotes tagged code to production with a separate account.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/blueprints-promotion.png)

## Choose one owner for every object

| Object | Owner in this workflow |
| --- | --- |
| Service accounts, tokens, compute settings | Terraform |
| Shared platform databases and access policy | Terraform, when explicitly provisioned as shared infrastructure |
| Flight definitions, Dive content, Guide content | Blueprints |
| Package-specific databases and shares | Blueprints or its pipeline when the package creates them |
| Data ingestion and refresh | Flight or modeling runtime |

The stock Wikipedia Flight creates its own database and share. Do not also
declare those objects as Terraform resources. If you adapt a package to use a
Terraform-owned database, make its existence a deployment prerequisite and
remove conflicting create/drop operations from the package.

## 1. Provision deployment identities

Use the [environment bootstrap](environments.md) pattern to create separate
staging and production service accounts. The names can follow your application,
such as `svc_pageviews_staging` and `svc_pageviews_prod`.

Supply read-write tokens to the application deployment jobs. The application
pipeline does not need the organization admin token just because Terraform used
one to create its accounts.

## 2. Start from the Blueprints template

Create a private repository from the
[Blueprints template](https://github.com/motherduckdb/blueprints-template/generate).
Its `motherduck.yml` selects packages and targets. The starter separates the
ingestion Flight from the dashboard:

```text
motherduck.yml
flights/
  wikipedia-pageviews-ingest/
    blueprint.yml
    src/flight.py
dives/
  wikipedia-pageviews/
    blueprint.yml
    src/dive.tsx
```

For a new application with related assets kept together, `make new-project
revenue` scaffolds a package under `projects/revenue/`. Keep its definitions and
source files together. See the [repository reference](https://github.com/motherduckdb/motherduck-blueprints/blob/main/docs/repository-reference.md).

## 3. Configure the deployment targets

The starter's default is intentionally simple: preview and production use the
same service account and GitHub Environment. A merge to `main` deploys production.
Use that only when its credential boundary fits your project.

For separate production credentials, follow
[the Blueprints staging setup](https://github.com/motherduckdb/motherduck-blueprints/blob/main/docs/setup-your-repository.md#add-staging-optional):

1. Create GitHub Environments `motherduck-staging` and `motherduck-production`.
2. Store the corresponding writer token as the environment secret
   `MOTHERDUCK_TOKEN` in each.
3. Route `preview` and `staging` targets to `motherduck-staging`.
4. Route `prod` to `motherduck-production` and configure its approval rules.
5. Give produced shares distinct physical names across targets. Keep preview
   resources branch-scoped and preview schedules disabled.

The environment secret selects the authenticated owner. A descriptive
`deployment.identity` label documents intent but does not mint or switch a
credential. Keep preview jobs out of the production environment.

## 4. Validate, preview, and promote

With the conventional staging target configured, the template follows this
release path:

| Event | Destination | Verification |
| --- | --- | --- |
| Local change | Offline checks | `make validate`, plus the package's tests |
| Pull request | Branch-scoped preview under staging | Inspect the preview plan and data/dashboard result |
| Merge to `main` | Stable staging deployment | Verify the combined application with staging credentials |
| Published non-prerelease GitHub Release | Production from the exact tag | Verify production resource identity and data |
| Pull request closes | Preview cleanup | Check that branch-owned preview resources were removed |

Promotion applies the reviewed code under the production identity. It does not
copy staging databases, state, or data. Terraform infrastructure must already be
present before a package that depends on it is deployed.

For custom automation, the [Blueprints action guide](https://github.com/motherduckdb/motherduck-blueprints/blob/main/docs/github-action.md)
documents explicit deployment inputs and verification. Pin an action version
according to your repository policy. The default action command is validation,
so a bare action step is not a deployment.

## 5. Pass the right information between tools

Use narrow Terraform outputs for non-secret identifiers such as a database name
or service-account username. Map them into the package's declared configuration
or pipeline environment. Use a secret manager for tokens and share URLs.

If Blueprints owns a share that another Terraform root needs to inspect,
`motherduck_owned_share` can read it without taking lifecycle ownership. Plan
that downstream root only after the share exists, using the owning account.

Deleting a production package from Git does not automatically delete all its
deployed data. Treat retirement as a deliberate operation and preserve the
credentials needed for cleanup.

## Adopt existing resources

For existing Flights, Dives, and Guides, start with
[Blueprints adoption](https://github.com/motherduckdb/motherduck-blueprints/blob/main/docs/adopt-existing-resources.md).
Import binds packages to existing IDs. It does not transfer data, grants, tokens,
or ownership.

If Terraform currently owns that content, preserve its IDs and transfer state
ownership without destroying remote resources before enabling the Blueprints
deployment. See [resource scope and migration](resource-scope.md). Verify that
the next Terraform plan proposes neither deletion nor recreation.
