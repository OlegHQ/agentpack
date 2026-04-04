# Versioning & Homebrew tap (see README → Releasing).
# Requires Python 3. Targets that talk to GitHub need `gh auth login`.

RELEASE_BRANCH ?= dev

GIT_SLUG := $(shell git remote get-url origin 2>/dev/null | sed -n 's/.*github\.com[:/]\(.*\)\.git/\1/p' | head -1)
ifeq ($(GIT_SLUG),)
UPSTREAM_REPO := OlegHQ/agentpack
BREW_OWNER := OlegHQ
else
UPSTREAM_REPO := $(GIT_SLUG)
BREW_OWNER := $(shell echo $(GIT_SLUG) | cut -d/ -f1)
endif

HOMEBREW_TAP_DIR ?= $(CURDIR)/../homebrew-agentpack
TAP_DESC := Homebrew tap for agentpack (https://github.com/$(UPSTREAM_REPO))

.PHONY: help minor patch check-clean check-gh \
	ship-minor ship-patch _ship-commit tag-push gh-release brew-sync brew-ship tap-init

.DEFAULT_GOAL := minor

help:
	@echo "Releasing (Cargo.toml semver):"
	@echo "  make, minor     0.x.y -> 0.(x+1).0"
	@echo "  patch           0.x.y -> 0.x.(y+1)"
	@echo "  ship-minor      bump minor, commit, tag v\$$v, push branch + tag"
	@echo "  ship-patch      bump patch, commit, tag v\$$v, push branch + tag"
	@echo "  tag-push        push $(RELEASE_BRANCH) + create/push tag for current version (no bump)"
	@echo "  gh-release      gh release create for current version (tag must exist on origin)"
	@echo "Homebrew tap (second repo: $(BREW_OWNER)/homebrew-agentpack):"
	@echo "  tap-init        create tap on GitHub if missing, clone to HOMEBREW_TAP_DIR, rsync packaging/"
	@echo "  brew-sync       refresh url + sha256 in \$$HOMEBREW_TAP_DIR/Formula/agentpack.rb"
	@echo "  brew-ship       brew-sync then commit + push the tap repo"
	@echo "Env: RELEASE_BRANCH=$(RELEASE_BRANCH) HOMEBREW_TAP_DIR=$(HOMEBREW_TAP_DIR)"

minor:
	python3 scripts/bump_version.py minor
	@echo "Cargo.toml version -> $$(python3 scripts/read_version.py)"

patch:
	python3 scripts/bump_version.py patch
	@echo "Cargo.toml version -> $$(python3 scripts/read_version.py)"

check-clean:
	@git diff-index --quiet HEAD -- || (echo "error: dirty working tree (commit or stash first)"; exit 1)

check-gh:
	@command -v gh >/dev/null 2>&1 || { echo "error: install gh (brew install gh) and run gh auth login"; exit 1; }

ship-minor: check-clean
	python3 scripts/bump_version.py minor
	@$(MAKE) _ship-commit

ship-patch: check-clean
	python3 scripts/bump_version.py patch
	@$(MAKE) _ship-commit

_ship-commit:
	@set -e; \
	v=$$(python3 scripts/read_version.py); \
	git add Cargo.toml; \
	git commit -m "chore: release v$$v"; \
	git tag -a "v$$v" -m "v$$v"; \
	git push origin "$(RELEASE_BRANCH)"; \
	git push origin "v$$v"; \
	echo "Released v$$v on $(RELEASE_BRANCH). Next: make gh-release && make brew-ship"

tag-push: check-clean check-gh
	@set -e; \
	v=$$(python3 scripts/read_version.py); \
	git push origin "$(RELEASE_BRANCH)"; \
	if git rev-parse -q --verify "refs/tags/v$$v" >/dev/null 2>&1; then \
		echo "tag v$$v already exists"; \
	else \
		git tag -a "v$$v" -m "v$$v"; \
	fi; \
	git push origin "v$$v"

gh-release: check-gh
	@set -e; \
	v=$$(python3 scripts/read_version.py); \
	if gh release view "v$$v" --repo "$(UPSTREAM_REPO)" >/dev/null 2>&1; then \
		echo "GitHub release v$$v already exists"; \
	else \
		gh release create "v$$v" --repo "$(UPSTREAM_REPO)" --generate-notes --verify-tag; \
	fi

brew-sync:
	@set -e; \
	v=$$(python3 scripts/read_version.py); \
	python3 scripts/sync_homebrew_formula.py "$(HOMEBREW_TAP_DIR)" "$(UPSTREAM_REPO)" "$$v"

brew-ship: check-gh
	@set -e; \
	v=$$(python3 scripts/read_version.py); \
	python3 scripts/sync_homebrew_formula.py "$(HOMEBREW_TAP_DIR)" "$(UPSTREAM_REPO)" "$$v"; \
	cd "$(HOMEBREW_TAP_DIR)" && \
		git add Formula/agentpack.rb && \
		if git diff --cached --quiet; then echo "no formula change"; exit 0; fi; \
		git commit -m "agentpack $$v"; \
		git push origin HEAD

tap-init: check-gh
	@set -e; \
	repo="$(BREW_OWNER)/homebrew-agentpack"; \
	if ! gh repo view "$$repo" >/dev/null 2>&1; then \
		gh repo create "$$repo" --public --description "$(TAP_DESC)"; \
	fi; \
	clone_url=$$(gh repo view "$$repo" --json sshUrl -q .sshUrl); \
	test -d "$(HOMEBREW_TAP_DIR)/.git" || git clone "$$clone_url" "$(HOMEBREW_TAP_DIR)"; \
	rsync -a "$(CURDIR)/packaging/homebrew-tap/" "$(HOMEBREW_TAP_DIR)/"; \
	echo "Synced packaging/homebrew-tap/ -> $(HOMEBREW_TAP_DIR)"; \
	echo "Next: cd $(HOMEBREW_TAP_DIR) && git add -A && git commit -m 'init tap' && git push -u origin main || git push -u origin master"
