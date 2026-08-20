# Security Policy

## Reporting A Vulnerability

Please report suspected security vulnerabilities privately by emailing security@motherduck.com. Do not open a public issue for undisclosed vulnerabilities.

Include:

- affected provider version and platform;
- minimal reproduction details;
- whether the issue can expose MotherDuck tokens, generated access tokens, share URLs, or Terraform state;
- any relevant redacted logs.

The provider manages credentials and access-bearing metadata. Never include raw `MOTHERDUCK_TOKEN`, `MOTHERDUCK_ADMIN_TOKEN`, generated access tokens, session cookies, or complete Terraform state in public issues, pull requests, or logs.

## Supported Versions

Security fixes are shipped in the latest released provider version. Users should upgrade to the latest release unless release notes state otherwise.
