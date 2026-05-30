# Versioning, local install (no Homebrew on Linux), and Homebrew tap release.
# Release targets use `cargo xtask` (Rust workspace task runner). Targets that talk to GitHub need `gh auth login`.

CARGO ?= cargo
INSTALL_DIR ?= $(HOME)/.local/bin
BINARY := agentpack
RELEASE_BIN := target/release/$(BINARY)

RELEASE_BRANCH ?= dev

GIT_SLUG := $(shell git remote get-url origin 2>/dev/null | sed -n 's/.*github\.com[:/]\(.*\)\.git/\1/p' | head -1)
ifeq ($(GIT_SLUG),)
UPSTREAM_REPO := OlegHQ/agentpack
BREW_OWNER := OlegHQ
else
UPSTREAM_REPO := $(GIT_SLUG)
BREW_OWNER := $(shell echo $(GIT_SLUG) | cut -d/ -f1)
endif

# Shared multi-formula tap: $(BREW_OWNER)/homebrew-tap (root-level formulae, e.g. agentpack.rb).
HOMEBREW_TAP_REPO ?= $(BREW_OWNER)/homebrew-tap
HOMEBREW_TAP_DIR ?= $(CURDIR)/../homebrew-tap
TAP_DESC := Homebrew tap for OlegHQ tools

.PHONY: help all build release install uninstall \
	minor patch check-clean check-gh \
	ship-minor ship-patch _ship-commit tag-push gh-release brew-sync brew-ship tap-init

.DEFAULT_GOAL := minor

help:
	@echo "Local build / install (e.g. Linux without brew):"
	@echo "  all, release    cargo build --release"
	@echo "  build           cargo build (debug)"
	@echo "  install         release, then copy binary to INSTALL_DIR ($(INSTALL_DIR))"
	@echo "  uninstall       remove binary from INSTALL_DIR"
	@echo ""
	@echo "Releasing (Cargo.toml semver):"
	@echo "  make, minor     0.x.y -> 0.(x+1).0"
	@echo "  patch           0.x.y -> 0.x.(y+1)"
	@echo "  ship-minor      bump minor, commit, tag v\$$v, push branch + tag"
	@echo "  ship-patch      bump patch, commit, tag v\$$v, push branch + tag"
	@echo "  tag-push        push $(RELEASE_BRANCH) + create/push tag for current version (no bump)"
	@echo "  gh-release      gh release create for current version (tag must exist on origin)"
	@echo "Homebrew shared tap ($(HOMEBREW_TAP_REPO)):"
	@echo "  tap-init        clone $(HOMEBREW_TAP_REPO) to HOMEBREW_TAP_DIR (creates it if missing)"
	@echo "  brew-sync       copy packaging formula -> tap root, refresh url + sha256 in agentpack.rb"
	@echo "  brew-ship       brew-sync then commit + push agentpack.rb to the shared tap"
	@echo "Env: RELEASE_BRANCH=$(RELEASE_BRANCH) HOMEBREW_TAP_DIR=$(HOMEBREW_TAP_DIR)"
	@echo "     INSTALL_DIR=$(INSTALL_DIR) CARGO=$(CARGO)"

all: release

build:
	$(CARGO) build

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

check-gh:
	@command -v gh >/dev/null 2>&1 || { echo "error: install gh (brew install gh) and run gh auth login"; exit 1; }

ship-minor: check-clean
	$(CARGO) xtask bump-version minor
	@$(MAKE) _ship-commit

ship-patch: check-clean
	$(CARGO) xtask bump-version patch
	@$(MAKE) _ship-commit

_ship-commit:
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	git add Cargo.toml; \
	git commit -m "chore: release v$$v"; \
	git tag -a "v$$v" -m "v$$v"; \
	git push origin "$(RELEASE_BRANCH)"; \
	git push origin "v$$v"; \
	echo "Released v$$v on $(RELEASE_BRANCH). Next: make gh-release && make brew-ship"

tag-push: check-clean check-gh
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	git push origin "$(RELEASE_BRANCH)"; \
	if git rev-parse -q --verify "refs/tags/v$$v" >/dev/null 2>&1; then \
		echo "tag v$$v already exists"; \
	else \
		git tag -a "v$$v" -m "v$$v"; \
	fi; \
	git push origin "v$$v"

gh-release: check-gh
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	if gh release view "v$$v" --repo "$(UPSTREAM_REPO)" >/dev/null 2>&1; then \
		echo "GitHub release v$$v already exists"; \
	else \
		gh release create "v$$v" --repo "$(UPSTREAM_REPO)" --generate-notes --verify-tag; \
	fi

brew-sync:
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	cp "$(CURDIR)/packaging/homebrew-tap/agentpack.rb" "$(HOMEBREW_TAP_DIR)/agentpack.rb"; \
	$(CARGO) xtask sync-homebrew "$(HOMEBREW_TAP_DIR)" "$(UPSTREAM_REPO)" "$$v"

brew-ship: check-gh
	@set -e; \
	v=$$($(CARGO) xtask read-version); \
	cp "$(CURDIR)/packaging/homebrew-tap/agentpack.rb" "$(HOMEBREW_TAP_DIR)/agentpack.rb"; \
	$(CARGO) xtask sync-homebrew "$(HOMEBREW_TAP_DIR)" "$(UPSTREAM_REPO)" "$$v"; \
	cd "$(HOMEBREW_TAP_DIR)" && \
		git add agentpack.rb && \
		if git diff --cached --quiet; then echo "no formula change"; exit 0; fi; \
		git commit -m "agentpack $$v"; \
		git push origin HEAD

# Shared tap: only ever touch our own `agentpack.rb` — never rsync the whole dir (it holds other
# formulae, e.g. rssdude.rb).
tap-init: check-gh
	@set -e; \
	repo="$(HOMEBREW_TAP_REPO)"; \
	if ! gh repo view "$$repo" >/dev/null 2>&1; then \
		gh repo create "$$repo" --public --description "$(TAP_DESC)"; \
	fi; \
	clone_url=$$(gh repo view "$$repo" --json sshUrl -q .sshUrl); \
	test -d "$(HOMEBREW_TAP_DIR)/.git" || git clone "$$clone_url" "$(HOMEBREW_TAP_DIR)"; \
	echo "Cloned $$repo -> $(HOMEBREW_TAP_DIR). Next: make brew-ship"
