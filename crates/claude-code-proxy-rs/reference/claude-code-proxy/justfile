# Glob list shared by check-format and fix-format. Each pattern stays
# single-quoted so the shell hands oxfmt the literal pattern.
oxfmt_globs := "'**/*.ts' '**/*.tsx' '**/*.js' '**/*.jsx' '**/*.mjs' '**/*.cjs' '**/*.mts' '**/*.cts' '**/*.json' '**/*.jsonc'"

# Point git at .githooks/ so the tracked hooks run. Idempotent.
install-hooks:
    git config core.hooksPath .githooks
    @echo "git hooks installed (core.hooksPath=.githooks)"

types:
    bun run typecheck

lint:
    bunx oxlint --type-aware

test:
    bun test

# Format check across JS/TS/JSON. Intentionally not part of `just check`.
check-format:
    bunx oxfmt --check {{oxfmt_globs}}

# Auto-fix formatting across JS/TS/JSON.
fix-format:
    bunx oxfmt --write {{oxfmt_globs}}

# Pre-push gate: format-check + the full check in parallel.
[parallel]
check-push: check-format check

# Runs types, lint, and tests in parallel.
#   just check            # quiet: one PASS/FAIL line per job
#   just check --verbose  # stream all output prefixed with [job]
check mode="":
    #!/usr/bin/env bash
    set -uo pipefail
    case "{{mode}}" in
        ""|--quiet) verbose=0 ;;
        -v|--verbose) verbose=1 ;;
        *) echo "usage: just check [--verbose|--quiet]" >&2; exit 2 ;;
    esac
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    if [ -t 1 ]; then export FORCE_COLOR=1; fi
    if [ -t 1 ]; then
        reset=$'\033[0m'
        bold=$'\033[1m'
        dim=$'\033[2m'
        green=$'\033[32m'
        red=$'\033[31m'
    else
        reset='' bold='' dim='' green='' red=''
    fi
    run() {
        local label=$1 color=$2; shift 2
        [ -z "$reset" ] && color=''
        local prefix="${color}${bold}${label}${reset}${dim} |${reset} "
        (
            if [ "$verbose" = 1 ]; then
                if "$@" 2>&1 | awk -v p="$prefix" '{print p $0}'; then
                    echo "${green}${bold}✔ PASS${reset} $label"
                else
                    rc=${PIPESTATUS[0]}
                    echo "${red}${bold}✘ FAIL${reset} $label ${dim}(exit $rc)${reset}"
                    exit "$rc"
                fi
            else
                if "$@" >"$tmp/$label.out" 2>&1; then
                    echo "${green}${bold}✔ PASS${reset} $label"
                else
                    rc=$?
                    echo "${red}${bold}✘ FAIL${reset} $label ${dim}(exit $rc)${reset}"
                    grep -v $'\xe2\x9c\x93' "$tmp/$label.out" \
                        | awk -v p="$prefix" '{print p $0}'
                    exit "$rc"
                fi
            fi
        ) &
        pids+=("$!")
    }
    pids=()
    run types $'\033[38;5;75m' bun run typecheck
    run lint  $'\033[38;5;213m' bunx oxlint --type-aware
    run test  $'\033[38;5;120m' bun test
    fail=0
    for pid in "${pids[@]}"; do
        wait "$pid" || fail=1
    done
    exit "$fail"
