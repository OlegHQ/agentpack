GO ?= go
GOFMT ?= gofmt
GORELEASER ?= goreleaser
INSTALL_DIR ?= $(HOME)/.local/bin
BINARY := agentpack
BUILD_DIR := bin
RELEASE_BIN := $(BUILD_DIR)/$(BINARY)
RELEASE_BRANCH ?= dev
VERSION := $(shell sed -n 's/^var Version = "\([^"]*\)"/\1/p' internal/cli/run.go)

.PHONY: help all build release install uninstall minor patch ship-minor ship-patch _ship check-clean fmt fmt-check lint test race ci hooks release-check

.DEFAULT_GOAL := help

help:
	@echo "Local build / install:"
	@echo "  all, release    build an optimized agentpack binary"
	@echo "  build           build agentpack"
	@echo "  install         release, then copy binary to INSTALL_DIR ($(INSTALL_DIR))"
	@echo "  uninstall       remove binary from INSTALL_DIR"
	@echo ""
	@echo "Quality gates (same checks CI runs):"
	@echo "  fmt             format all Go files"
	@echo "  lint            run go vet"
	@echo "  test            run the full Go test suite"
	@echo "  race            run tests with the race detector"
	@echo "  ci              fmt-check + vet + test + race + build"
	@echo "  release-check   validate and snapshot-build GoReleaser artifacts"
	@echo "  hooks           install tracked git hooks"
	@echo ""
	@echo "Release — GoReleaser publishes archives, checksums, GitHub release, and Homebrew cask:"
	@echo "  minor / patch   bump the embedded semantic version"
	@echo "  ship-minor      bump minor, commit, tag, push $(RELEASE_BRANCH) + tag"
	@echo "  ship-patch      bump patch, commit, tag, push $(RELEASE_BRANCH) + tag"
	@echo "Env: RELEASE_BRANCH=$(RELEASE_BRANCH) INSTALL_DIR=$(INSTALL_DIR) GO=$(GO) GORELEASER=$(GORELEASER)"

all: release

build:
	$(GO) build -o "$(RELEASE_BIN)" ./cmd/agentpack

release:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X github.com/OlegHQ/agentpack/internal/cli.Version=$(VERSION)" -o "$(RELEASE_BIN)" ./cmd/agentpack

fmt:
	$(GOFMT) -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check:
	@test -z "$$($(GOFMT) -l $$(find . -name '*.go' -not -path './vendor/*'))" || { $(GOFMT) -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1; }

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

ci: fmt-check lint test race build

release-check:
	$(GORELEASER) check
	$(GORELEASER) release --snapshot --clean --skip=publish

hooks:
	git config core.hooksPath .githooks
	@echo "git hooks installed (core.hooksPath -> .githooks)"

install: release
	mkdir -p "$(INSTALL_DIR)"
	cp "$(RELEASE_BIN)" "$(INSTALL_DIR)/$(BINARY)"
	@echo "Installed $(INSTALL_DIR)/$(BINARY) — ensure INSTALL_DIR is on PATH"

uninstall:
	rm -f "$(INSTALL_DIR)/$(BINARY)"

minor:
	@$(MAKE) _bump PART=minor

patch:
	@$(MAKE) _bump PART=patch

_bump:
	@old=$$(sed -n 's/^var Version = "\([^"]*\)"/\1/p' internal/cli/run.go); \
	new=$$(printf '%s\n' "$$old" | awk -v part="$(PART)" 'BEGIN { FS=OFS="." } { if (part=="minor") { $$2++; $$3=0 } else { $$3++ } print }'); \
	for file in internal/cli/run.go integration/cli_test.go integration/installer_test.go README.md scripts/agentpack-installer.sh scripts/agentpack-installer.ps1; do \
		sed "s/$$old/$$new/g" "$$file" > "$$file.tmp"; \
		mv "$$file.tmp" "$$file"; \
	done
	@chmod +x scripts/agentpack-installer.sh
	@$(GOFMT) -w internal/cli/run.go
	@echo "version -> $$(sed -n 's/^var Version = "\([^"]*\)"/\1/p' internal/cli/run.go)"

check-clean:
	@git diff-index --quiet HEAD -- || (echo "error: dirty working tree (commit or stash first)"; exit 1)

ship-minor: check-clean
	@$(MAKE) _bump PART=minor
	@$(MAKE) _ship

ship-patch: check-clean
	@$(MAKE) _bump PART=patch
	@$(MAKE) _ship

_ship:
	@set -e; \
	v=$$(sed -n 's/^var Version = "\([^"]*\)"/\1/p' internal/cli/run.go); \
	$(GO) test ./...; \
	git add internal/cli/run.go; \
	git commit -m "chore(release): v$$v"; \
	git tag -a "v$$v" -m "v$$v"; \
	git push origin "$(RELEASE_BRANCH)"; \
	git push origin "v$$v"; \
	echo "Pushed v$$v — GoReleaser CI will publish binaries, checksums, the Homebrew cask, and the GitHub release."
