TOOLS_DIR := tools/bin
TFPLUGINDOCS := $(TOOLS_DIR)/tfplugindocs
TFPLUGINDOCS_VERSION := v0.25.0
ACTIONLINT := $(TOOLS_DIR)/actionlint
ACTIONLINT_VERSION := v1.7.12
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK := $(TOOLS_DIR)/govulncheck
GOVULNCHECK_VERSION := v1.6.0

.PHONY: build clean-generated clean-tool-cache docs docs-check fmt fmt-check lint pre-push-check release-check release-package-local shellcheck test-examples test-import-validation test-invalid-configuration test-missing-credentials test-repository-hygiene test-scripts test-unit test-integration test-acceptance test-live-blueprint-sql-only test-live-blueprint-writer-path test-live-canonical-values test-live-cleanup-audit test-live-complex test-live-database-drop-with-objects test-live-database-drift test-live-database-options test-live-dive test-live-dive-flight-blueprint test-live-ducklake-database test-live-flight test-live-guide test-live-object-storage-listing test-live-preview-function-diagnostics test-live-provider-config test-live-provider-single-attach test-live-quoted-identifiers test-live-quoted-identifiers-import test-live-read-only-sql-catalog test-live-rest-edge test-live-rest-helper test-live-rest-permission-diagnostics test-live-rest-token-matrix test-live-schema-cascade test-live-secret-metadata-drift test-live-secret-raw-sql test-live-share-grant-drift test-live-share-modes test-live-share-option-drift test-live-snapshot-drift test-live-sql-drift test-live-sql-edge test-live-sql-import test-live-sql-stable test-live-table-replace test-live-table-types test-live-table-unmanaged-view test-live-view-drift test-terraform-versions test-terraform-versions-blueprint test-terraform-versions-lifecycle tools tools-ci tools-docs vulncheck workflow-check $(TFPLUGINDOCS) $(ACTIONLINT) $(GOLANGCI_LINT) $(GOVULNCHECK)

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

docs-check:
	tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT; \
	cp -R docs "$$tmp_dir/docs"; \
	$(MAKE) docs; \
	diff -ru "$$tmp_dir/docs" docs

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
	if command -v shellcheck >/dev/null 2>&1; then shellcheck scripts/*.sh scripts/lib/*.sh; else echo "shellcheck not installed; skipping"; fi

release-package-local:
	@if [ -z "$${VERSION:-}" ]; then echo "VERSION is required, for example VERSION=0.1.0 make release-package-local" >&2; exit 1; fi
	VERSION=$${VERSION} ./scripts/package-release.sh

release-check:
	rm -rf dist/release-check dist/release-sign-test dist/release-check-darwin-amd64
	VERSION=0.0.0 DIST_DIR=$$(pwd)/dist/release-check ./scripts/package-release.sh
	cd dist/release-check && shasum -a 256 *.zip > terraform-provider-motherduck_0.0.0_SHA256SUMS
	cd dist/release-check && shasum -a 256 -c terraform-provider-motherduck_0.0.0_SHA256SUMS

pre-push-check: fmt-check lint vulncheck workflow-check shellcheck test-scripts test-unit docs-check test-examples test-import-validation test-invalid-configuration test-missing-credentials test-live-rest-helper test-repository-hygiene build

test-unit:
	go test ./...

test-scripts:
	bash -n scripts/*.sh scripts/lib/*.sh
	./scripts/test-download-checksum-unit.sh
	./scripts/test-version-matrix-unit.sh

test-integration:
	MD_TF_ACC=1 go test ./internal/client/... ./internal/resources/... ./internal/datasources/...
	TF_ACC=1 MD_TF_ACC=1 go test ./internal/acceptance/...

test-acceptance:
	TF_ACC=1 MD_TF_ACC=1 go test ./internal/acceptance/...

test-live-cleanup-audit:
	./scripts/audit-live-test-cleanup.sh

test-live-canonical-values:
	./scripts/test-live-canonical-values.sh

test-live-blueprint-sql-only:
	./scripts/test-live-blueprint-sql-only.sh

test-live-blueprint-writer-path:
	./scripts/test-live-blueprint-writer-path.sh

test-examples:
	./scripts/test-examples.sh

test-import-validation:
	./scripts/test-import-validation.sh

test-invalid-configuration:
	./scripts/test-invalid-configuration.sh

test-missing-credentials:
	./scripts/test-missing-credentials.sh

test-repository-hygiene:
	./scripts/test-repository-hygiene.sh

test-live-complex:
	./scripts/test-live-complex.sh

test-live-database-drop-with-objects:
	./scripts/test-live-database-drop-with-objects.sh

test-live-database-drift:
	./scripts/test-live-database-drift.sh

test-live-database-options:
	./scripts/test-live-database-options.sh

test-live-dive:
	./scripts/test-live-dive.sh

test-live-dive-flight-blueprint:
	./scripts/test-live-dive-flight-blueprint.sh

test-live-ducklake-database:
	./scripts/test-live-ducklake-database.sh

test-live-flight:
	./scripts/test-live-flight.sh

test-live-guide:
	./scripts/test-live-guide.sh

test-live-object-storage-listing:
	./scripts/test-live-object-storage-listing.sh

test-live-preview-function-diagnostics:
	./scripts/test-live-preview-function-diagnostics.sh

test-live-provider-config:
	./scripts/test-live-provider-config.sh

test-live-provider-single-attach:
	./scripts/test-live-provider-single-attach.sh

test-live-quoted-identifiers:
	./scripts/test-live-quoted-identifiers.sh

test-live-quoted-identifiers-import:
	./scripts/test-live-quoted-identifiers-import.sh

test-live-read-only-sql-catalog:
	./scripts/test-live-read-only-sql-catalog.sh

test-live-rest-edge:
	./scripts/test-live-rest-edge.sh

test-live-rest-helper:
	./scripts/test-live-rest-helper.sh

test-live-rest-permission-diagnostics:
	./scripts/test-live-rest-permission-diagnostics.sh

test-live-rest-token-matrix:
	./scripts/test-live-rest-token-matrix.sh

test-live-schema-cascade:
	./scripts/test-live-schema-cascade.sh

test-live-secret-metadata-drift:
	./scripts/test-live-secret-metadata-drift.sh

test-live-secret-raw-sql:
	./scripts/test-live-secret-raw-sql.sh

test-live-share-grant-drift:
	./scripts/test-live-share-grant-drift.sh

test-live-share-modes:
	./scripts/test-live-share-modes.sh

test-live-share-option-drift:
	./scripts/test-live-share-option-drift.sh

test-live-snapshot-drift:
	./scripts/test-live-snapshot-drift.sh

test-live-sql-drift:
	./scripts/test-live-sql-drift.sh

test-live-sql-edge:
	./scripts/test-live-sql-edge.sh

test-live-sql-import:
	./scripts/test-live-sql-import.sh

test-live-sql-stable:
	./scripts/test-live-sql-stable.sh

test-live-table-replace:
	./scripts/test-live-table-replace.sh

test-live-table-types:
	./scripts/test-live-table-types.sh

test-live-table-unmanaged-view:
	./scripts/test-live-table-unmanaged-view.sh

test-live-view-drift:
	./scripts/test-live-view-drift.sh

test-terraform-versions:
	./scripts/test-terraform-versions.sh

test-terraform-versions-lifecycle:
	TF_VERSION_SQL_LIFECYCLE=1 ./scripts/test-terraform-versions.sh

test-terraform-versions-blueprint:
	TF_VERSION_BLUEPRINT_LIFECYCLE=1 ./scripts/test-terraform-versions.sh
