# Query tenant analytics from a backend

A small Node HTTP server completes the [Terraform tenant example](../README.md).
It follows the [MotherDuck Next.js cookbook](https://motherduck.com/docs/cookbook/vercel-nextjs/)
for verified TLS, parameterized SQL, and pg connection pooling. It binds to
localhost and has no browser bundle or deployment requirement.

## Prepare runtime configuration

Apply the tenant root, load the sample data, and attach each reader's share as
`reporting` using its setup token. Give this server only the read-scaling tokens.
Through your secret manager, create a protected `TENANT_CREDENTIALS_FILE`:

```json
{
  "acme": {"enabled": true, "token": "<acme_read_scaling_token>"},
  "globex": {"enabled": true, "token": "<globex_read_scaling_token>"}
}
```

For the local demonstration, generate random application bearer credentials and
store their SHA-256 hashes in a protected `APP_SESSIONS_FILE` mapped to tenant IDs:

```json
{"<sha256_of_application_session>": "acme"}
```

The application bearer credential is distinct from the MotherDuck token. Do not
use an MD token as an application session. Replace this static demonstration
mapping with your identity provider's verified server-side sessions before
exposing the service. Never trust a browser-supplied tenant header.

```shell
npm ci
npm run build
npm test
# Inject file paths and the host for your organization's region.
export MOTHERDUCK_PG_HOST=pg.us-east-1-aws.motherduck.com
npm start
```

Set `TENANT_CREDENTIALS_FILE` and `APP_SESSIONS_FILE` through your environment.
Call `GET /usage?start=2026-01-01&end=2026-01-02` with the application bearer
credential in the Authorization header. The server derives the tenant from the
session and returns that tenant's rows. A mismatching `tenant` query parameter
is rejected rather than selecting another credential.

## Pool and lifecycle boundaries

The example accepts at most 16 configured tenants, each with a lazy pool of at
most two connections, idle expiry and query/connect timeouts. This is a bounded
demo, not a production sizing recommendation. Scale the routing design before
raising the cap. A pool always belongs to one credential and never switches
identity between requests. Big integers are returned as strings without precision
loss. Only value filters are accepted, not arbitrary SQL or identifiers.

When suspending a tenant, disable its application route first by setting
`enabled=false` (the token can then be omitted) and restarting the server to close existing pools. Then apply
`suspended_tenants` in Terraform to revoke grants and managed credentials while
retaining data. Re-enable only after Terraform restores access and reader setup
is complete. Every credential or session-file update requires a restart, including
rotation. During a zero-downtime deployment, drain old instances after new instances
successfully connect, then retire old credentials.

For cleanup, stop this server before destroying the tenant root, then remove the
writer bootstrap last. Database errors return a generic response rather than
exposing SQL or credential-bearing diagnostics to callers.
