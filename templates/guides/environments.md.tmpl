---
page_title: "Separate development and production"
subcategory: "Deployment architectures"
description: |-
  Bootstrap environment writers and isolate credentials, compute, and Terraform state.
---

# Separate development and production

Give each environment its own writer service account, credentials, compute
settings, and Terraform state. The warehouse bootstrap creates `dev_writer` and
`prod_writer` identities and separate read-scaling tokens for BI.

An `environment` variable is a naming convention. Changing it does not switch
the authenticated MotherDuck account. Terraform workspaces separate state, but
do not select a different MotherDuck token for you.

## 1. Create the environment identities

Copy the [bootstrap root](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/warehouses/bootstrap)
into an admin-controlled working directory. Configure an encrypted, locking
backend, inject `MOTHERDUCK_ADMIN_TOKEN`, and choose an organization-unique
`account_prefix`.

```shell
terraform init
terraform plan -var=account_prefix=svc_orders -out=bootstrap.tfplan
terraform apply bootstrap.tfplan
```

This creates two accounts, their compute settings, and four tokens:

| Token output key | Owner with this prefix | Used by |
| --- | --- | --- |
| `dev_writer` | `svc_orders_dev_writer` | Dev Terraform and ingestion |
| `dev_bi` | `svc_orders_dev_writer` | Dev BI read-scaling connections |
| `prod_writer` | `svc_orders_prod_writer` | Production Terraform and ingestion |
| `prod_bi` | `svc_orders_prod_writer` | Production BI read-scaling connections |

The BI entries are tokens, not separate accounts. They can read all data visible
to their writer, including raw data. For narrower access, use
[separate readers with curated shares](sharing-and-read-scaling.md).

The example uses Standard compute, 60-second cooldowns, and read-pool limits of
one for dev and two for prod. These are illustrative starting values, not
workload measurements. Size for your account and test expected concurrency.

## 2. Distribute credentials securely

Transfer the sensitive token outputs into separate secret-manager entries.
Restrict bootstrap state because it contains all four credentials. Pass
usernames through non-secret configuration rather than exposing bootstrap state
to every pipeline.

The example tokens do not expire because `ttl` is omitted. Adopt a rotation
policy for production and revoke retired credentials. Changing TTL on an
existing token resource requests replacement.

## 3. Keep deployment roots independent

```text
infrastructure/
  bootstrap/        # Admin state and account/token resources
  environments/
    dev/            # Dev backend, writer token, and variables
    prod/           # Production backend, writer token, and variables
  modules/
    warehouse/      # Shared configuration and SQL templates
```

Both environment roots call the same warehouse module. Keep provider
configuration in the roots. Their CI jobs receive the corresponding writer
token as `MOTHERDUCK_TOKEN` and use distinct backend keys and access controls.

For the standalone examples, copy the whole selected root for each environment.
In dev, apply `dev.tfvars` with the dev token. In production, apply `prod.tfvars`
with the production token. Add the
[writer-identity precondition](authentication.md#check-the-sql-identity-before-creation)
before working with important data.

## 4. Promote the reviewed code

1. Run formatting and validation without production credentials.
2. Plan and apply dev with the dev writer token.
3. Run ingestion and data checks, then verify reader access.
4. Review a production plan made with the production token and state.
5. Apply that saved plan through your production approval process.
6. Verify production data and a no-change Terraform plan.

Apply with the same provider configuration and identity used to produce the
plan. Keep saved plans protected because they can contain sensitive values.

## Extend isolation by workload

The examples keep all layers under one writer per environment. When ingestion,
transformation, and serving need independent compute, give those workloads
separate accounts as well.

A transformation account can read a restricted share of production source data
and write models into databases it owns. CI can use a separate account with
the same read-only source and isolated outputs. Use a sanitized source share
when development must not read sensitive production data. See
[MotherDuck environment management](https://motherduck.com/docs/key-tasks/data-warehousing/environment-management/).

Sharing production input is an explicit access decision. Environment naming
does not grant it, and promoting a configuration does not copy the data.

## Clean up in dependency order

For disposable deployments, stop consumers, destroy each warehouse with its
original writer token, and destroy bootstrap last. Deleting a service account
deletes its owned data. Revoking a writer token first can remove the credential
needed to finish cleanup.
