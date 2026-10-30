# Changelog

Each release has a versioned notes file under `release-notes/`, which is also the
body of the matching [GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases).
This index lists the headline change for every release.

## v0.3.9

This release adds the `motherduck_share_secure_view` resource and the `motherduck_share_secure_views` data source, which manage and audit row-level security, column-level security and column masking on shares, and fixes grant replacement, case-insensitive catalog lookups and several plan defects. Version constraints such as `~> 0.3.0` select it, and the [v0.3.9 release notes](release-notes/v0.3.9.md) describe each change and the upgrade review.

## v0.3.8

This release restricts `motherduck_service_account` to service accounts, corrects the handling of identifiers that differ only in letter case, adds data sources and attributes for the MotherDuck production surface, fixes several refresh and plan defects, and explains how to resolve common configuration errors. Version constraints such as `~> 0.3.0` select it. See the [v0.3.8 release notes](release-notes/v0.3.8.md).

## v0.3.7

This release builds the provider with Go 1.27.3 to fix the `crypto/x509` certificate trust vulnerability GO-2026-6809 and updates Go, workflow and example dependencies. Version constraints such as `~> 0.3.0` select it. See the [v0.3.7 release notes](release-notes/v0.3.7.md).

## v0.3.6

This release keeps access tokens in Terraform state when a successful REST response has an empty or null body instead of a token inventory. Version constraints such as `~> 0.3.0` select it. See the [v0.3.6 release notes](release-notes/v0.3.6.md).

## v0.3.5

This release explains refused role grants in organizations that manage user roles through SCIM, reports an unreachable MotherDuck as a network problem rather than a token problem, and detects letter case drift in table ENUM labels. Version constraints such as `~> 0.3.0` select it. See the [v0.3.5 release notes](release-notes/v0.3.5.md).

## v0.3.4

This release recovers a missing snapshot ID after the creating database was dropped, so that refresh and destroy can manage the retained snapshot name, and builds with Go 1.27.2. Version constraints such as `~> 0.3.0` select it. See the [v0.3.4 release notes](release-notes/v0.3.4.md).

## v0.3.3

This release adds the `motherduck_users` data source, which lists the users and service accounts of an organization. Version constraints such as `~> 0.3.0` select it. See the [v0.3.3 release notes](release-notes/v0.3.3.md).

## v0.3.2

This release adds `instance_type` to set the runner size of a Flight and embeds DuckDB 1.5.6. Version constraints such as `~> 0.3.0` select it. See the [v0.3.2 release notes](release-notes/v0.3.2.md).

## v0.3.1

This release adds the `motherduck_secret` data source for separately owned secrets and keeps snapshot state when a failed create or cleanup cannot resolve the snapshot ID. Version constraints such as `~> 0.3.0` select it. See the [v0.3.1 release notes](release-notes/v0.3.1.md).

## v0.3.0

This release registers Iceberg REST catalogs as databases, manages the MotherDuck features released through 2026-09-25, and installs on glibc 2.34 and macOS 13. It includes stricter plan-time validation and behavior changes, so constraints such as `~> 0.2.13` do not select it. See the [upgrade steps and v0.3.0 release notes](release-notes/v0.3.0.md).

## v0.2.13

This release version-pins the examples, makes their token rotation safer, and provides complete Terraform root modules. See the [v0.2.13 release notes](release-notes/v0.2.13.md).

## v0.2.12

This release keeps a newly created view in Terraform state when the catalog read that follows its creation returns no row or an error. See the [v0.2.12 release notes](release-notes/v0.2.12.md).

## v0.2.11

Follow complete examples from provisioning through reader setup, token rotation, runtime workloads and teardown. A Registry guide links the version-pinned projects. See the [v0.2.11 release notes](release-notes/v0.2.11.md).

## v0.2.10

Audit share audiences against an explicit allowlist and optionally fail Terraform plans when missing or unexpected grants persist. See the [v0.2.10 release notes](release-notes/v0.2.10.md).

## v0.2.9

Preserve snapshot state after a successful rename when catalog readback fails, so retries and cleanup keep the correct identity. See the [v0.2.9 release notes](release-notes/v0.2.9.md).

## v0.2.8

Expand live regression coverage for database recovery, account and token changes, and writer/reader isolation while preserving provider schemas and features. See the [v0.2.8 release notes](release-notes/v0.2.8.md).

## v0.2.7

Respect canceled SQL requests before starting an operation, and reduce allocation overhead when converting mixed-value catalog rows. See the [v0.2.7 release notes](release-notes/v0.2.7.md).

## v0.2.6

Keep newly created databases, roles, and role grants manageable when the following catalog read is empty or fails. See the [v0.2.6 release notes](release-notes/v0.2.6.md).

## v0.2.5

Recover cleanly when a Dive or Guide is deleted outside Terraform, and get consistent diagnostics when a Flight run times out. See the [v0.2.5 release notes](release-notes/v0.2.5.md).

## v0.2.4

Follow clearer architecture diagrams for provisioning, layered warehouses, tenant isolation, and Blueprints promotion, with explicit account ownership and compute boundaries. See the [v0.2.4 release notes](release-notes/v0.2.4.md).

## v0.2.3

Build an orders warehouse, separate environments, or serve customer analytics with practical guides, runnable examples, and concise architecture diagrams. See the [v0.2.3 release notes](release-notes/v0.2.3.md).

## v0.2.2

Release checksums now carry a detached GPG signature, which is the last artifact the Terraform Registry requires to ingest a release. See the [v0.2.2 release notes](release-notes/v0.2.2.md).

## v0.2.1

Adds repeatable plan/apply checks for unintended replacement and identity drift across the checked-in examples. See the [v0.2.1 release notes](release-notes/v0.2.1.md).

## v0.2.0

In-place database and Flight updates preserve dependent infrastructure and avoid unintended Flight re-execution. See the [v0.2.0 release notes](release-notes/v0.2.0.md).

## v0.1.1

Makes SQL sessions report the authenticated MotherDuck identity on their first query and across pooled reconnects. See the [v0.1.1 release notes](release-notes/v0.1.1.md).

## v0.1.0

Manage MotherDuck SQL infrastructure and REST administration from Terraform. See the [v0.1.0 release notes](release-notes/v0.1.0.md).
