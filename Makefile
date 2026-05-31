# Local build / install + version bump. Binaries, the Homebrew formula (-> OlegHQ/homebrew-tap), and
# the GitHub release are built and published by cargo-dist on tag push — see dist-workspace.toml and
# .github/workflows/release.yml.

CARGO ?= cargo
INSTALL_DIR ?= $(HOME)/.local/bin
BINARY := agentpack
RELEASE_BIN := target/release/$(BINARY)
RELEASE_BRANCH ?= dev

.PHONY: help all build release install uninstall minor patch ship-minor ship-patch _ship check-clean fmt lint ci hooks

.DEFAULT_GOAL := help

help:
	@echo "Local build / install (e.g. Linux without brew):"
	@echo "  all, release    cargo build --release"
	@echo "  build           cargo build (debug)"
	@echo "  install         release, then copy binary to INSTALL_DIR ($(INSTALL_DIR))"
	@echo "  uninstall       remove binary from INSTALL_DIR"
	@echo ""
	@echo "Quality gates (same checks CI runs):"
	@echo "  fmt             cargo fmt --all (auto-format)"
	@echo "  lint            cargo clippy --all-targets -- -D warnings"
	@echo "  ci              run the full CI gate locally (fmt --check + clippy + test)"
	@echo "  hooks           install git hooks (core.hooksPath -> .githooks) so the gate runs pre-commit/pre-push"
	@echo ""
	@echo "Release — cargo-dist builds binaries + Homebrew formula + GitHub release on tag push:"
	@echo "  minor / patch   bump Cargo.toml semver only"
	@echo "  ship-minor      bump minor, commit, tag v\$$v, push $(RELEASE_BRANCH) + tag (triggers CI)"
	@echo "  ship-patch      bump patch, commit, tag v\$$v, push $(RELEASE_BRANCH) + tag (triggers CI)"
	@echo "Env: RELEASE_BRANCH=$(RELEASE_BRANCH) INSTALL_DIR=$(INSTALL_DIR) CARGO=$(CARGO)"

all: release

build:
	$(CARGO) build

fmt:
	$(CARGO) fmt --all

lint:
	$(CARGO) clippy --all-targets -- -D warnings

# Mirror .github/workflows/ci.yml so a green `make ci` means green CI.
ci:
	$(CARGO) fmt --all -- --check
	$(CARGO) clippy --all-targets -- -D warnings
	$(CARGO) test

# Point git at the tracked hooks in .githooks/ (one-time, per clone).
hooks:
	git config core.hooksPath .githooks
	@echo "git hooks installed (core.hooksPath -> .githooks)"

release:
	$(CARGO) build --release

install: release
	mkdir -p "$(INSTALL_DIR)"
	cp "$(RELEASE_BIN)" "$(INSTALL_DIR)/$(BINARY)"
	@echo "Installed $(INSTALL_DIR)/$(BINARY) — ensure INSTALL_DIR is on PATH"

uninstall:
	rm -f "$(INSTALL_DIR)/$(BINARY)"

minor:
	$(CARGO) xtask bump-version minor
	@echo "Cargo.toml version -> $$($(CARGO) xtask read-version)"

patch:
	$(CARGO) xtask bump-version patch
	@echo "Cargo.toml version -> $$($(CARGO) xtask read-version)"

check-clean:
	@git diff-index --quiet HEAD -- || (echo "error: dirty working tree (commit or stash first)"; exit 1)

ship-minor: check-clean
	$(CARGO) xtask bump-version minor
	@$(MAKE) _ship

ship-patch: check-clean
	$(CARGO) xtask bump-version patch
	@$(MAKE) _ship

# Refresh Cargo.lock, commit, tag, push branch + tag. The tag push triggers cargo-dist's release
# workflow (per-platform binaries, the Homebrew formula, and the GitHub release).
_ship:
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	$(CARGO) build >/dev/null 2>&1; \
	git add Cargo.toml Cargo.lock; \
	git commit -m "chore(release): v$$v"; \
	git tag -a "v$$v" -m "v$$v"; \
	git push origin "$(RELEASE_BRANCH)"; \
	git push origin "v$$v"; \
	echo "Pushed v$$v — cargo-dist CI will publish binaries, the Homebrew formula, and the GitHub release."
