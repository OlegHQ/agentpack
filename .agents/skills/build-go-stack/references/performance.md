# Fast Go builds and tests

Use this reference to keep backend feedback fast as the repository grows.

## Treat speed as a feature

Name backend workflows and keep their scopes distinct:

| Workflow | Scope | Expected frequency |
| --- | --- | --- |
| targeted test | changed Go package or precise `-run` case | every edit |
| backend suite | cached `go test ./...` | before handoff |
| backend build | Go command using current generated inputs | frequently |
| integration tier | affected external boundaries | when risk requires |
| clean baseline | uncached backend pipeline | scheduled or after pipeline changes |

Measure comparable wall times. Establish stable budgets from repeated measurements and store them in version control. Warn locally and fail CI only with enough tolerance for machine noise.

## Preserve Go incrementality

The Go command caches build outputs and successful package-list test results. Use package-list mode such as `go test ./...`; avoid routine `go clean -cache`, `go clean -testcache`, `-a`, and `go test -count=1`.

Use `GODEBUG=gocachetest=1` temporarily to diagnose cache misses. Keep test inputs explicit and deterministic. Run integration, race, fuzz, and end-to-end suites as deliberate tiers rather than on every edit. Keep generation out of ordinary builds unless declared inputs changed.

Primary sources: [Go command caching](https://pkg.go.dev/cmd/go#hdr-Build_and_test_caching) and [Go testing](https://pkg.go.dev/testing).

## Keep tests valuable

- Use table-driven tests for pure component behavior.
- Use small fakes for consumer-owned I/O, clock, queue, filesystem, or network boundaries when they make important tests deterministic.
- Test concrete repositories against the real datastore in the appropriate integration tier.
- Measure slow tests with `go test -count=1 -json ./...` periodically, never as the default loop.
- Rewrite, move, or remove redundant and flaky tests only after preserving the important behavior and failure mode.
- Use benchmarks and repeated `benchstat` samples for hot code; do not confuse runtime benchmarks with build wall-clock budgets.

## Keep backend builds independent

Backend-only tests and builds must depend only on backend sources, Go module files, and explicitly declared generated inputs. They must not invoke unrelated client package installation or compilation. If a command embeds a staged external artifact, backend builds may consume the current staging directory without rebuilding its producer; the complete artifact pipeline owns freshness verification.

## Review regressions

Report timing impact when a change affects imports, generation, test topology, build steps, or CI. Compare warm with warm and clean with clean. Fix dependency or test design before raising a budget; raise it only for a documented product-value tradeoff.
