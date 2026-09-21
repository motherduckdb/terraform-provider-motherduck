TOOLS_DIR := tools/bin
TFPLUGINDOCS := $(TOOLS_DIR)/tfplugindocs
TFPLUGINDOCS_VERSION := v0.25.0
ACTIONLINT := $(TOOLS_DIR)/actionlint
ACTIONLINT_VERSION := v1.7.12
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint
GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK := $(TOOLS_DIR)/govulncheck
GOVULNCHECK_VERSION := v1.6.0

.PHONY: \
  build clean-generated clean-tool-cache docs docs-check \
  fmt fmt-check lint pre-push-check release-check \
  release-package-local release-sign-check shellcheck static-check test-cli-versions \
  test-contract test-scripts test-unit test-integration test-acceptance \
  test-live-cleanup-audit test-terraform-versions-blueprint test-terraform-versions-lifecycle tools tools-ci \
  tools-docs vulncheck workflow-check $(TFPLUGINDOCS) $(ACTIONLINT) \
  $(GOLANGCI_LINT) $(GOVULNCHECK) release-preflight-check

tools: tools-docs

tools-docs: $(TFPLUGINDOCS)

$(TFPLUGINDOCS):
	@installed_version=$$(go version -m "$@" 2>/dev/null | awk '$$1 == "mod" && $$2 == "github.com/hashicorp/terraform-plugin-docs" { print $$3 }'); \
	if [ "$$installed_version" != "$(TFPLUGINDOCS_VERSION)" ]; then \
		mkdir -p $(TOOLS_DIR); \
		GOBIN=$$(pwd)/$(TOOLS_DIR) go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@$(TFPLUGINDOCS_VERSION); \
	fi

tools-ci: $(ACTIONLINT) $(GOLANGCI_LINT) $(GOVULNCHECK)

$(ACTIONLINT):
	@installed_version=$$(go version -m "$@" 2>/dev/null | awk '$$1 == "mod" && $$2 == "github.com/rhysd/actionlint" { print $$3 }'); \
	if [ "$$installed_version" != "$(ACTIONLINT_VERSION)" ]; then \
		mkdir -p $(TOOLS_DIR); \
		GOBIN=$$(pwd)/$(TOOLS_DIR) go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION); \
	fi

$(GOLANGCI_LINT):
	@installed_version=$$(go version -m "$@" 2>/dev/null | awk '$$1 == "mod" && $$2 == "github.com/golangci/golangci-lint/v2" { print $$3 }'); \
	if [ "$$installed_version" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		mkdir -p $(TOOLS_DIR); \
		GOBIN=$$(pwd)/$(TOOLS_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

$(GOVULNCHECK):
	@installed_version=$$(go version -m "$@" 2>/dev/null | awk '$$1 == "mod" && $$2 == "golang.org/x/vuln" { print $$3 }'); \
	if [ "$$installed_version" != "$(GOVULNCHECK_VERSION)" ]; then \
		mkdir -p $(TOOLS_DIR); \
		GOBIN=$$(pwd)/$(TOOLS_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi

build:
	go build ./...

clean-generated:
	find tools/provider-bin tools/provider-mirror -type f -delete 2>/dev/null || true
	find tools/provider-bin tools/provider-mirror -depth -type d -empty -delete 2>/dev/null || true

clean-tool-cache:
	find tools/terraform tools/opentofu tools/bin -type f -delete 2>/dev/null || true
	find tools/terraform tools/opentofu tools/bin -depth -type d -empty -delete 2>/dev/null || true

docs: tools-docs
	TF_PLUGIN_TIMEOUT=120s $(TFPLUGINDOCS) generate --provider-name=motherduck
	mkdir -p docs/assets
	cp templates/assets/*.png templates/assets/*.svg docs/assets/

docs-check:
	set -eu; tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT; \
	cp -R docs "$$tmp_dir/docs"; \
	$(MAKE) docs; \
	diff -ru "$$tmp_dir/docs" docs
	$(TFPLUGINDOCS) validate --provider-name motherduck

fmt:
	gofmt -w .
	terraform fmt -recursive examples test-fixtures

fmt-check:
	test -z "$$(gofmt -l .)"
	terraform fmt -check -recursive examples test-fixtures

lint: $(GOLANGCI_LINT)
	go vet ./...
	$(GOLANGCI_LINT) run

workflow-check: tools-ci
	$(ACTIONLINT)

vulncheck: tools-ci
	$(GOVULNCHECK) ./...

shellcheck:
	@command -v shellcheck >/dev/null 2>&1 || { echo "shellcheck is required (brew install shellcheck / apt-get install shellcheck)" >&2; exit 1; }
	shellcheck scripts/*.sh scripts/lib/*.sh

release-package-local:
	@if [ -z "$${VERSION:-}" ]; then echo "VERSION is required, for example VERSION=0.1.0 make release-package-local" >&2; exit 1; fi
	VERSION=$${VERSION} ./scripts/package-release.sh

release-check:
	rm -rf dist/release-check dist/release-sign-test dist/release-check-darwin-amd64
	VERSION=0.0.1 DIST_DIR=$$(pwd)/dist/release-check ./scripts/package-release.sh
	cd dist/release-check && shasum -a 256 *.zip > terraform-provider-motherduck_0.0.1_SHA256SUMS
	cd dist/release-check && shasum -a 256 -c terraform-provider-motherduck_0.0.1_SHA256SUMS
	bash ./scripts/test-release-package.sh "$(CURDIR)/dist/release-check/terraform-provider-motherduck_0.0.1_$$(go env GOOS)_$$(go env GOARCH).zip" 0.0.1

release-sign-check:
	./scripts/test-release-signing-unit.sh

STATIC_CHECKS := fmt-check lint workflow-check shellcheck test-scripts docs-check test-cli test-repository-hygiene build

static-check: $(STATIC_CHECKS) vulncheck

# CI's required CLI matrix already runs test-cli on every supported version.
# The local pre-push gate retains it through static-check.
.PHONY: ci-static-check
ci-static-check: $(filter-out test-cli,$(STATIC_CHECKS)) vulncheck

pre-push-check: static-check test-unit test-contract

# The pull-request gate without the live vulnerability feed. The tag-driven
# release workflow uses this so a new advisory in an indirect dependency cannot
# block an unrelated release. Vulncheck still runs there as an advisory step
# and stays blocking in pull-request CI through static-check.
release-preflight-check: $(STATIC_CHECKS) test-unit test-contract

test-unit:
	go test -race -shuffle=on -timeout=5m -count=1 -cover ./...

test-contract:
	go test -race -shuffle=on -timeout=5m -tags=contract -run '^TestContract' -count=1 -cover ./internal/provider

test-scripts:
	for script in scripts/*.sh scripts/lib/*.sh; do bash -n "$$script" || exit; done
	python3 -c 'import ast, pathlib; [ast.parse(p.read_text(), filename=str(p)) for p in pathlib.Path("scripts").rglob("*.py")]'
	./scripts/test-download-checksum-unit.sh
	./scripts/test-version-matrix-unit.sh
	./scripts/test-terraform-test-unit.sh
	./scripts/test-live-common-unit.sh
	./scripts/test-live-rest-helper.sh
	./scripts/test-live-examples-runner-unit.sh
	./scripts/test-live-examples-core-unit.sh
	python3 ./scripts/test-terraform-cycle-audit-unit.py
	./scripts/test-live-rest-admin-gates.sh
	./scripts/test-gates-unit.sh
	./scripts/test-release-signing-unit.sh
	./scripts/test-live-cleanup-audit-unit.sh

test-integration:
	@if [ -z "$${MOTHERDUCK_TOKEN:-}" ]; then echo "MOTHERDUCK_TOKEN is required for SQL integration tests" >&2; exit 1; fi
	MD_TF_ACC=1 go test -tags=acceptance -count=1 ./internal/client/sql

test-acceptance:
	@if [ -z "$${MOTHERDUCK_TOKEN:-}" ]; then echo "MOTHERDUCK_TOKEN is required for Terraform acceptance tests" >&2; exit 1; fi
	TF_ACC=1 MD_TF_ACC=1 go test -tags=acceptance -count=1 ./internal/acceptance

.PHONY: test-live-admin
test-live-admin:
	TF_ACC=1 MD_TF_ACC=1 go test -timeout=20m -tags=acceptance,admin_acceptance -run '^TestPluginTestingAdminGraphLifecycle$$' -count=1 ./internal/acceptance
	./scripts/test-live-blueprint-writer-path.sh

test-live-cleanup-audit:
	./scripts/audit-live-test-cleanup.sh

test-cli-versions:
	./scripts/test-terraform-versions.sh --offline

test-terraform-versions-lifecycle:
	TF_VERSION_SQL_LIFECYCLE=1 ./scripts/test-terraform-versions.sh

test-terraform-versions-blueprint:
	TF_VERSION_BLUEPRINT_LIFECYCLE=1 ./scripts/test-terraform-versions.sh

SCRIPT_TEST_SCRIPTS := \
  scripts/test-live-required.sh \
  scripts/test-live-cycles.sh \
  scripts/test-live-examples.sh \
  scripts/test-live-examples-core.sh \
  scripts/test-live-examples-warehouses.sh \
  scripts/test-live-examples-apps.sh \
  scripts/test-live-examples-roles.sh \
  scripts/test-live-canonical-values.sh \
  scripts/test-live-blueprint-sql-only.sh \
  scripts/test-live-blueprint-writer-path.sh \
  scripts/test-cli.sh \
  scripts/test-examples.sh \
  scripts/test-import-validation.sh \
  scripts/test-invalid-configuration.sh \
  scripts/test-missing-credentials.sh \
  scripts/test-repository-hygiene.sh \
  scripts/test-live-complex.sh \
  scripts/test-live-database-drop-with-objects.sh \
  scripts/test-live-database-drift.sh \
  scripts/test-live-database-options.sh \
  scripts/test-live-dive.sh \
  scripts/test-live-dive-flight-blueprint.sh \
  scripts/test-live-ducklake-database.sh \
  scripts/test-live-flight.sh \
  scripts/test-live-guide.sh \
  scripts/test-live-object-storage-listing.sh \
  scripts/test-live-preview-function-diagnostics.sh \
  scripts/test-live-provider-config.sh \
  scripts/test-live-provider-single-attach.sh \
  scripts/test-live-quoted-identifiers.sh \
  scripts/test-live-quoted-identifiers-import.sh \
  scripts/test-live-read-only-sql-catalog.sh \
  scripts/test-live-rest-edge.sh \
  scripts/test-live-rest-helper.sh \
  scripts/test-live-rest-permission-diagnostics.sh \
  scripts/test-live-rest-token-matrix.sh \
  scripts/test-live-schema-cascade.sh \
  scripts/test-live-secret-metadata-drift.sh \
  scripts/test-live-secret-raw-sql.sh \
  scripts/test-live-share-grant-drift.sh \
  scripts/test-live-share-modes.sh \
  scripts/test-live-share-option-drift.sh \
  scripts/test-live-snapshot-drift.sh \
  scripts/test-live-sql-drift.sh \
  scripts/test-live-sql-edge.sh \
  scripts/test-live-sql-import.sh \
  scripts/test-live-sql-stable.sh \
  scripts/test-live-table-replace.sh \
  scripts/test-live-table-types.sh \
  scripts/test-live-table-unmanaged-view.sh \
  scripts/test-live-view-drift.sh \
  scripts/test-terraform-versions.sh
SCRIPT_TEST_TARGETS := $(notdir $(SCRIPT_TEST_SCRIPTS:.sh=))

.PHONY: $(SCRIPT_TEST_TARGETS)

$(SCRIPT_TEST_TARGETS):
	./scripts/$@.sh
