# AGENTS.md

Repository-specific guidance for coding agents working on the MotherDuck Terraform provider.

## Working In This Repo

- Keep changes small, schema-driven, and consistent with the Terraform Plugin Framework patterns already in `internal/`.
- Read `README.md`, `CONTRIBUTING.md`, and `docs/guides/ci-and-release.md` before changing provider behavior, docs, workflows, or release packaging.
- Treat `internal/` as implementation detail, not as a stable public Go API.
- Preserve the public provider source address: `registry.terraform.io/motherduckdb/motherduck`.

## Docs

- Generated provider docs live under `docs/`.
- Durable generated-doc inputs live under `templates/` and `examples/`.
- When changing schemas, examples, or generated docs, edit the matching template/example first and run:

```bash
make docs
```

## Validation

- Run the local pre-push gate before opening or updating a PR:

```bash
make pre-push-check
```

- Run release packaging checks after touching release scripts, release workflows, or version packaging:

```bash
make release-check
```

- Live tests require MotherDuck credentials in the environment. Do not paste raw token-backed logs into public issues or release notes.

## Releases

- Releases are tag-driven through `.github/workflows/release.yml`.
- Use a PR for release-prep changes, wait for green CI, merge to `main`, then tag the intended merge commit.
- Use semantic version tags such as `v0.1.0`.
- After pushing a release tag, verify the GitHub Actions release workflow and the created GitHub release artifacts.
