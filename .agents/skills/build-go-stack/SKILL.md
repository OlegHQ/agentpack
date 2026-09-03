---
name: build-go-stack
description: Structure, implement, refactor, or review Go backend applications using Fiber, cmd-local composition and HTTP transport, component-owned domain and Mongo persistence, contextual error wrapping with final-boundary logging, concrete types, prefixed environment and optional Vault configuration, dependency inversion, official MCP integrations, and fast cached verification. Use for Go module layout, component models, repositories, services, JSON/API contracts, handlers, workers, CLIs, configuration, secrets, logging, startup, shutdown, dependency design, generation, backend builds, tests, performance, or architecture reviews. This skill is backend-only.
---

# Build Go Stack

Build an idiomatic Go backend around `cmd/` and `internal/`. Keep repository instructions and the requested product architecture authoritative.

## Start with discovery

1. Inspect repository instructions, `go.mod`, build tooling, package layout, and established conventions before editing.
2. Preserve compatible choices. Do not restructure unrelated code merely to match this skill.
3. Read [references/architecture.md](references/architecture.md) before scaffolding, moving packages, selecting dependencies, or changing backend build composition.
4. Read [references/dependency-graph.md](references/dependency-graph.md) before adding component dependencies, service calls, repositories, or cross-component workflows.
5. Read [references/performance.md](references/performance.md) before changing Go tests, build targets, generation, CI caches, or developer feedback loops.
6. Read [references/configuration.md](references/configuration.md) before adding configuration, environment variables, Vault, logging, startup, shutdown, or dependency construction.
7. Read [references/serialization.md](references/serialization.md) before changing API types, JSON, database records, dynamic values, or layer transforms.
8. Read [references/errors.md](references/errors.md) before changing error handling, retries, HTTP responses, worker failures, cleanup, or logging.
9. Read [references/mcp.md](references/mcp.md) before adding an MCP command, endpoint, transport, tool, resource, prompt, or protocol behavior.
10. Confirm unclear behavior only when a reasonable assumption could materially change the result.

## Use the standard backend layout

Use this target unless the repository has a compatible variation:

```text
backend/
  cmd/
    api/
      main.go
      config.go
      logging.go
      bootstrap.go
      server.go
      routes/
      handlers/
      middleware/
      httpx/
      apijson/
    worker/
    mcp/
  internal/
    components/
      workspace/
      project/
      issue/
      auth/
      overview/
    infrastructure/
      mongodb/
      messages/
      queue/
  go.mod
```

Add only commands and packages the application needs. Keep component rules and adapters under `internal/`. Keep Fiber handlers, middleware, route tables, login endpoints, and JSON response mappers under `cmd/<command>/`. Use **component** consistently for business packages and put their root at `internal/components/`.

## Compose and observe explicitly

- Use one uppercase application prefix for application environment variables.
- Apply configuration as typed defaults, optional Vault values, then prefixed environment overrides. Validate once before constructing dependencies.
- Fail startup if explicitly configured Vault cannot authenticate or read required data. Never log secrets or full configuration.
- Construct configuration, `log/slog`, clients, repositories, services, transports, lifecycle, and cleanup explicitly inside `cmd/<app>`.
- Keep `main` minimal and return errors from a testable `run` or bootstrap function.
- Pass `*slog.Logger` explicitly. Log a failure once at its final operational boundary; lower layers wrap and return it.
- Default to readable stdout logs, disable color for non-interactive output or `NO_COLOR`, and support JSON through configuration.

## Preserve component ownership

- Treat humans and agents as first-class actors wherever the domain permits ownership, responsibility, authorship, participation, or assigned work.
- Keep each bounded context under `internal/components/<name>/`; do not create a shared entity dump.
- Keep the service-facing `Repository` port and concrete `MongoRepository` together in the owning component's flat `repository.go`.
- Expose `NewMongoRepository(client) *MongoRepository` and assert interface satisfaction. Keep shared driver helpers free of component imports.
- Keep services concrete. Add a small consumer-owned interface only for a real replacement boundary, important fast fake, or multiple implementations.
- Keep cross-component workflows in the highest package owning the use case. Maintain an acyclic import graph.
- Keep HTTP concerns out of components and database-specific records out of handlers and service APIs.

## Keep data flow concrete

- Use an entity directly when its domain semantics and public JSON contract match.
- Add write inputs for authority and validation, and response types only for materially different public contracts.
- Exclude internal fields statically and test exact serialized keys on sensitive boundaries.
- Avoid `any`, dynamic maps, reflection mappers, and redundant entity-record-DTO chains. Decode unavoidable dynamic external data immediately.
- Wrap failures with useful operation context and `%w` when stable identity should cross the boundary. Translate private vendor errors before exposing them.

## Select and generate deliberately

1. Search the standard library first, then official framework documentation and established packages.
2. Verify current maintenance, releases, license, security, API size, and compatibility before adding a dependency.
3. Prefer mature libraries for protocols, validation, migrations, queues, observability, retry, and cryptography; avoid speculative wrappers.
4. Prefer `go generate` or the repository task runner for mechanical contracts. Pin generators and never hand-edit generated output.
5. Use the official `github.com/modelcontextprotocol/go-sdk/mcp` package for MCP lifecycle, schemas, stdio, and Streamable HTTP. Keep handlers thin over component services.

Use current primary sources for unstable APIs. Never guess package APIs from memory.

## Verify proportionally

- Follow repository validation order and choose the smallest check capable of falsifying the change.
- Run targeted package tests during iteration, then cached `go test ./...` from the module before handoff.
- Run `go vet ./...` and the complete backend artifact pipeline when their inputs or release risk require them.
- Preserve Go build and test caches; do not routinely use `-count=1`, `-a`, or cache-clearing commands.
- Add focused component-service and repository-contract tests. Prefer small fakes at owned boundaries over broad mocks.
- Record commands, failures, assumptions, skipped checks, and meaningful timing regressions.
- Keep client implementation, visual design, and client toolchains outside this backend skill.

## Reject recurring failure modes

- Do not put business logic in handlers, `main.go`, generated code, or database adapters.
- Do not create user-only ownership shortcuts where actors can also be agents.
- Do not create a global entities package, shared persistence store, or per-component infrastructure adapter tree.
- Do not introduce interfaces for every service or repository.
- Do not create circular ownership, bidirectional service calls, or a coordinator that knows every component.
- Do not log and return the same error or expose private vendor error identity unintentionally.
- Do not add one-to-one DTOs, dynamic JSON scrubbing, or datastore types at component boundaries.
- Do not put Fiber transport under `internal/` or return Fiber types from repositories.
- Do not rebuild unrelated toolchains, rerun generation, clear caches, or execute broad integration suites on every backend edit.
- Do not invent security, serialization, routing, protocol, migration, or retry machinery when a mature solution fits.
