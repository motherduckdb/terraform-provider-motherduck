---
page_title: "Manage access control as code"
subcategory: "Operations"
description: |-
  Review MotherDuck roles, membership, and share access in pull requests, then apply them from GitHub Actions.
---

# Manage access control as code

An access change made in the MotherDuck UI is immediate and invisible. Nobody
reviews it, nothing records why it happened, and the only way to answer "who can
read this share" later is to look again.

Moving the same change into Terraform in a Git repository makes access a
proposal that someone approves, a commit that says who asked and who agreed, and
a state that a scheduled job can compare against reality. This guide covers the
workflow, the credentials it needs, and the parts of MotherDuck security that
stay outside Terraform.

## What Terraform manages

| Control | Resources |
| --- | --- |
| Custom roles | [motherduck_role](../resources/role.md) |
| Role membership and inheritance | [motherduck_role_grant](../resources/role_grant.md) |
| Share audience | [motherduck_share](../resources/share.md), [motherduck_share_grant](../resources/share_grant.md) |
| Which tables a share exposes | `include_pattern` on [motherduck_share](../resources/share.md) |
| Non-human identities and their credentials | [motherduck_service_account](../resources/service_account.md), [motherduck_access_token](../resources/access_token.md) |

Three properties of the MotherDuck model shape how the configuration is written:

- Preset roles are concentric. Admin includes Builder, and Builder includes
  Explorer. A custom role cannot select platform permissions individually, so it
  reaches them by inheriting a preset role.
- Data access is separate from platform permissions. A database belongs to the
  account that created it. Other accounts reach its data through a share.
- There is no per-table privilege equivalent to `GRANT SELECT ON TABLE`. Table
  level security is the share's `include_pattern`, which belongs to the share
  rather than to a grant, so two audiences that need different tables need two
  shares.

## What stays outside Terraform

MotherDuck exposes no API for these, so the provider cannot manage them and a
configuration that claims to cover all of security would be wrong:

- SSO through SAML or OIDC, and SCIM provisioning.
- Organization membership for people, including invitations and deactivation.
- Billing, plan, and AWS PrivateLink.

Configure those in the MotherDuck UI. Terraform owns the roles, grants, and
service identities layered on top of them. If your identity provider creates
users through SCIM, Terraform still owns which roles those users hold.

## A reviewable configuration

Keep access control in its own Terraform root, separate from the warehouse that
holds the data. The two change at different rates and by different reviewers,
and separate roots keep an access review from replanning tables.

The [access control example](../../examples/access-control/README.md) turns a
team map into roles, membership, and share reads. One team is one entry, and one
grant is one resource, so a plan reads as a list of access changes:

```terraform
# One role per team, one Terraform resource per direct grant. Every access
# change is then a reviewable line in a pull request rather than a UI click.
locals {
  # Teams that inherit a preset platform role. MotherDuck preset roles are
  # concentric, so admin includes builder and builder includes explorer.
  inherited_platform_roles = {
    for team_key, team in var.teams : team_key => team.platform_role
    if team.platform_role != null
  }

  memberships = merge([
    for team_key, team in var.teams : {
      for member in team.members :
      "${team_key}/${member}" => { team = team_key, member = member }
    }
  ]...)

  share_reads = merge([
    for team_key, team in var.teams : {
      for share in team.shares :
      "${team_key}/${share}" => { team = team_key, share = share }
    }
  ]...)
}

resource "motherduck_role" "team" {
  for_each = var.teams
  name     = "${var.role_prefix}_${each.key}"
}

# Platform permissions reach a custom role only through inheritance.
resource "motherduck_role_grant" "platform" {
  for_each     = local.inherited_platform_roles
  role_name    = each.value
  grantee_name = motherduck_role.team[each.key].name
  grantee_type = "role"
}

# Membership. Use the service-account username for non-human principals.
resource "motherduck_role_grant" "member" {
  for_each     = local.memberships
  role_name    = motherduck_role.team[each.value.team].name
  grantee_name = each.value.member
  grantee_type = "user"
}

# Data access. The share must already exist and use access = "restricted",
# because organization and unrestricted shares carry their whole audience
# instead of individual grants.
resource "motherduck_share_grant" "team" {
  for_each     = local.share_reads
  share        = each.value.share
  username     = motherduck_role.team[each.value.team].name
  grantee_type = "role"
}
```

Import grants that already exist before the first apply. Without an import,
Terraform plans to create a grant MotherDuck already has, and the apply either
fails or takes ownership of something nobody reviewed.

## Review in a pull request, apply on merge

This workflow plans on a pull request and reports the plan as a comment, applies
after merge behind a GitHub environment that can require a second approver, and
reports drift on a schedule:

```yaml
# Reviewed MotherDuck access control. Copy this file into
# .github/workflows/ in the repository that holds the configuration, with the
# Terraform root in access-control/.
#
# A pull request plans and reports. A merge to main applies. A weekly run
# reports grants that changed outside Terraform.
name: MotherDuck access control

on:
  pull_request:
    paths:
      - access-control/**
      - .github/workflows/access-control.yml
  push:
    branches:
      - main
    paths:
      - access-control/**
  schedule:
    # Weekly drift report. Times are UTC.
    - cron: "17 13 * * 1"

permissions:
  contents: read

# Grants are shared state. Serialize every run against one MotherDuck
# organization rather than cancelling an in-flight apply.
concurrency:
  group: motherduck-access-control
  cancel-in-progress: false

env:
  TF_IN_AUTOMATION: "true"
  TF_INPUT: "false"

jobs:
  plan:
    name: Plan
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    timeout-minutes: 15
    permissions:
      contents: read
      pull-requests: write
    defaults:
      run:
        working-directory: access-control
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e # v4.0.1
        with:
          terraform_version: 1.16.1
          terraform_wrapper: false

      - name: Initialize
        run: terraform init -lock-timeout=5m

      - name: Check formatting
        run: terraform fmt -check -recursive

      - name: Validate
        run: terraform validate

      # A read-only token is enough to plan. Reserve the token that can grant
      # and revoke for the apply job.
      - name: Plan
        env:
          MOTHERDUCK_TOKEN: ${{ secrets.MOTHERDUCK_ACCESS_CONTROL_PLAN_TOKEN }}
        run: terraform plan -lock-timeout=5m -out=tfplan

      # Reviewers approve the plan, not just the diff. Never upload the plan
      # file itself as an artifact: a plan that creates access tokens contains
      # their values.
      - name: Report the plan on the pull request
        env:
          GH_TOKEN: ${{ github.token }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
        run: |
          terraform show -no-color tfplan > plan.txt
          {
            echo "### MotherDuck access control plan"
            echo
            echo '```terraform'
            head -c 50000 plan.txt
            echo '```'
          } > comment.md
          gh pr comment "${PR_NUMBER}" --body-file comment.md

  apply:
    name: Apply
    if: github.event_name == 'push'
    runs-on: ubuntu-latest
    timeout-minutes: 15
    # A GitHub environment holds the token that can change access and can
    # require a second approver before this job starts.
    environment: motherduck-access-control
    defaults:
      run:
        working-directory: access-control
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e # v4.0.1
        with:
          terraform_version: 1.16.1
          terraform_wrapper: false

      - name: Initialize
        run: terraform init -lock-timeout=5m

      - name: Apply
        env:
          MOTHERDUCK_TOKEN: ${{ secrets.MOTHERDUCK_ACCESS_CONTROL_TOKEN }}
        run: terraform apply -auto-approve -lock-timeout=5m

  drift:
    name: Drift report
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    timeout-minutes: 15
    permissions:
      contents: read
      issues: write
    defaults:
      run:
        working-directory: access-control
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - uses: hashicorp/setup-terraform@dfe3c3f87815947d99a8997f908cb6525fc44e9e # v4.0.1
        with:
          terraform_version: 1.16.1
          terraform_wrapper: false

      - name: Initialize
        run: terraform init -lock-timeout=5m

      # Exit code 2 means MotherDuck no longer matches the reviewed
      # configuration: a grant was added, revoked, or changed by hand.
      - name: Detect drift
        id: drift
        env:
          MOTHERDUCK_TOKEN: ${{ secrets.MOTHERDUCK_ACCESS_CONTROL_PLAN_TOKEN }}
        run: |
          set +e
          terraform plan -lock-timeout=5m -detailed-exitcode -no-color > drift.txt
          plan_exit=$?
          set -e
          if [ "${plan_exit}" -gt 2 ]; then
            cat drift.txt
            exit "${plan_exit}"
          fi
          echo "changed=$([ "${plan_exit}" -eq 2 ] && echo true || echo false)" >> "${GITHUB_OUTPUT}"

      - name: Open a drift issue
        if: steps.drift.outputs.changed == 'true'
        env:
          GH_TOKEN: ${{ github.token }}
          RUN_URL: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
        run: |
          {
            echo "MotherDuck access no longer matches the reviewed configuration."
            echo
            echo "Run: ${RUN_URL}"
            echo
            echo '```terraform'
            head -c 50000 drift.txt
            echo '```'
          } > drift-issue.md
          gh issue create \
            --title "MotherDuck access control drift" \
            --label access-control \
            --body-file drift-issue.md
```

Four details matter more than the rest:

- **Two tokens, not one.** The plan job runs on every pull request, including
  from a branch someone else pushed, so give it a token that can read the
  catalog and nothing more. The token that can grant and revoke belongs to the
  apply job, in a GitHub environment where it is unavailable to pull request
  runs.
- **Run share grants as the share owner.** Only the owner of a share can run
  `GRANT READ ON SHARE`. An organization admin who does not own it can read its
  grants but cannot change them. If shares are owned by different service
  accounts, split the configuration into one root per owner rather than looking
  for an identity that can do everything.
- **Use a remote state backend with locking.** Two concurrent applies against
  one organization will otherwise race. The `concurrency` group in the workflow
  serializes GitHub Actions runs, and it does not serialize a run against an
  engineer applying from a laptop.
- **Keep credential creation in a different root.** A plan or state that creates
  [access tokens](../resources/access_token.md) contains the token values.
  Access control configuration that only grants and revokes contains names, which
  is what makes it safe to post a plan into a pull request comment. Never upload
  the plan file itself as a build artifact.

## What a reviewer should look for

A plan that looks small can widen access considerably:

- A grant to the `explorer` role reaches every user in the organization, because
  the preset roles are concentric.
- Changing a share's `access` from `restricted` to `organization` opens it to the
  whole organization, and `unrestricted` opens it to anyone holding the share
  URL. Individual grants stop being what controls the audience.
- Removing `include_pattern` from a share exposes every table in the source
  database rather than the listed ones.
- Destroying a `motherduck_role` revokes access for everyone who held it, which
  is the intended outcome of an offboarding and a surprise in a rename.

## Audit and drift

[motherduck_share_grants](../data-sources/share_grants.md) reports the full
audience of a share, including the whole-organization or public grant that a
non-restricted share carries and any grant made by hand:

```terraform
data "motherduck_share_grants" "analytics" {
  share_name = "analytics_share"
}

output "analytics_readers" {
  value = [
    for grant in data.motherduck_share_grants.analytics.rows :
    "${grant.grantee_type}:${grant.grantee_name}"
  ]
}
```

[motherduck_role_members](../data-sources/role_members.md) answers the same
question for a role, and [motherduck_roles_for_user](../data-sources/roles_for_user.md)
answers it for one person, including roles reached through inheritance.

The scheduled job in the workflow runs `terraform plan -detailed-exitcode`. Exit
code 2 means MotherDuck no longer matches the reviewed configuration, which is
the signal that someone granted or revoked access outside the process. The next
apply restores the reviewed state.

## Related

- [Authentication](authentication.md) for which token each operation needs
- [Sharing and read scaling](sharing-and-read-scaling.md) for publishing data to other accounts
- [Separate development and production](environments.md) for isolating identities and state
