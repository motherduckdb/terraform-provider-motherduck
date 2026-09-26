# Changelog

Each release has a versioned notes file under `release-notes/`, which is also the
body of the matching [GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases).
This index lists the headline change for every release.

## v0.3.0

Register Iceberg REST catalogs as databases, manage the MotherDuck features released through 2026-09-25, and install on glibc 2.34 and macOS 13. Includes stricter plan-time validation and a few behavior changes, so `~> 0.2.x` constraints do not select it. See the [upgrade steps and v0.3.0 release notes](release-notes/v0.3.0.md).

## v0.2.13

Follow the current, version-pinned examples with safer token rotation and complete copyable Terraform roots. See the [v0.2.13 release notes](release-notes/v0.2.13.md).

## v0.2.12

Retain a newly created view's Terraform state when its immediate catalog read returns no row or an error. See the [v0.2.12 release notes](release-notes/v0.2.12.md).

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
