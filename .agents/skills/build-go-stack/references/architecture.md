# Architecture and upstream guidance

Use this reference when scaffolding or restructuring Go packages, selecting backend dependencies, or composing commands.

## Contents

- Required repository shape
- Command composition
- HTTP transport
- Component package contract
- Repository ownership
- Dependency selection
- Generation candidates

## Required repository shape

`backend/cmd/<command>` contains executable entrypoints and HTTP transport for API commands. `backend/internal/components` contains business rules and ports. `backend/internal/infrastructure` contains shared adapters.

This follows Go's server-project guidance: commands belong under `cmd`, while non-public server packages belong under `internal`. Go enforces the import restriction for packages beneath an `internal` directory.

Primary source: [Organizing a Go module](https://go.dev/doc/modules/layout).

## Command composition

Keep `cmd/api/main.go` small enough to show the lifecycle clearly:

1. Read bootstrap environment and load typed configuration.
2. Load Vault values when configured, apply environment overrides, and validate.
3. Construct final logging and telemetry.
4. Construct infrastructure clients, repositories, and component services.
5. Construct Fiber and register middleware and routes from `cmd/api/routes/`.
6. Start the server and coordinate graceful shutdown.

Move cohesive bootstrap details into `config.go`, `logging.go`, and `bootstrap.go` in the same `main` package when needed. Keep dependency construction visible there. Do not turn the command package into a business-logic layer or move wiring into a global container.

## HTTP transport (cmd-local)

Keep presentation out of `internal/`:

```text
cmd/api/
  server.go              # Fiber app and Listen/Shutdown lifecycle
  routes/routes.go       # route table; no business logic
  handlers/
    auth.go              # POST /api/auth/login, logout, GET /api/auth/me
    health.go
    v1/*.go              # thin handlers: parse HTTP, call component, map JSON
  middleware/auth.go
  httpx/errors.go
  apijson/               # JSON mappers; component must not import this
```

Rules:

- Handlers parse requests, resolve `auth.Principal` from middleware, call component services, and map results through `apijson` at the boundary.
- `internal/components/*` owns entities, repository ports, and use cases — including saved views (`catalog.View`) as component data, not HTTP routes.
- Do not recreate `internal/api/` or bundle login/routes/handlers inside `internal/`.

## Component package contract

For a component such as posts:

```text
internal/components/posts/
  models.go       # Post and related value types
  collections.go  # Mongo collection names owned by this component
  repository.go   # Repository port required by use cases
  service.go      # use-case behavior
  helpers.go      # pure cohesive helpers, only if useful
```

For a multi-component app such as Nudge, split by bounded context instead of one shared entities package:

```text
internal/components/
  workspace/      # users, workspaces, memberships, service accounts
  team/
  project/
  issue/
  catalog/        # labels, views, documents, audit
  agent/
  initiative/
  notification/
  auth/           # principals, sessions, credentials (no Mongo models)
  overview/       # cross-component read models / snapshot assembly
```

Rules:

- Each leaf component owns its document types and collection constants.
- Infrastructure repositories decode into the owning component package's types.
- API JSON mappers live in transport (`cmd/api/apijson/`), not in component packages. Component orchestrators such as `overview/` return component snapshots; handlers map them to JSON.
- Orchestrator services (`overview/`) may import many leaf components; leaf components must not import orchestrators or peers upward.

Treat filenames as navigation conventions, not rigid layer requirements. Keep a small component in fewer files. Split a large service by use case while retaining one cohesive package until package boundaries create a concrete benefit.

## Anti-pattern: monolithic `entities` package

Do **not** create `internal/components/entities/` (or similar) containing every collection's struct and every collection-name constant.

Why it fails:

- Any handler, repository, or service import drags the entire schema surface into scope.
- New features increase merge contention in one directory with dozens of unrelated types.
- Cross-component orchestration code naturally lands beside leaf models, blurring ownership.
- Package cycles appear when auth, issue, or project code needs types from the same dump.

Preferred shape:

```text
internal/components/issue/repository.go          # Repository contract + Mongo implementation
internal/components/issue/service.go
internal/components/overview/service.go          # injects component ports only
```

When porting a legacy schema, resist the shortcut of mirroring the old database inventory as one Go package. Mirror the **component boundaries** instead.

## Repository ownership (required)

Every persisted component owns both its repository contract and Mongo-backed implementation in one flat component package:

| File | Contents |
|------|----------|
| `internal/components/<component>/repository.go` | `Repository` interface, query/page types, component errors, concrete `MongoRepository`, `NewMongoRepository(client)`, BSON, and driver calls. |

Example for workspace:

```text
internal/components/workspace/repository.go
```

Wire in bootstrap:

```go
workspaceRepo := workspace.NewMongoRepository(mongoClient)
svc := workspace.NewService(workspaceRepo) // accepts workspace.Repository
```

Shared connection/query helpers and canonical physical collection constants may remain under `internal/infrastructure/mongodb`, but that package must not import components. Repository implementations must avoid peer-component imports; decode cross-aggregate checks into minimal local BSON projection types. Application-wide index and validator setup may remain in infrastructure bootstrap code.

## Anti-pattern: per-component infrastructure adapters

Do **not** create:

```text
internal/infrastructure/mongo/workspace_repository.go   # WRONG
internal/infrastructure/mongo/issue_repository.go       # WRONG
internal/infrastructure/mongo/workspace/repository.go   # WRONG
internal/infrastructure/mongo/store.go                  # WRONG
```

Why it fails:

- Component ownership is unclear and navigation crosses distant package trees.
- Persistence behavior changes alongside component models and use cases, so separating it increases coordination cost.
- A shared store becomes a junk drawer and couples unrelated features.

Keep each implementation inside its owning component package.

## Anti-pattern: monolithic infrastructure `Store` in component services

Do **not** grow one shared `infrastructure/mongo/store.go` with every entity's CRUD methods and inject `*mongo.Store` into component services.

Why it fails:

- Query and page types defined beside the driver leak into handlers.
- Repository ownership disappears; every feature edits the same adapter file.

Preferred shape:

```text
internal/components/workspace/repository.go
internal/components/issue/repository.go
cmd/api/bootstrap.go   # workspace.NewMongoRepository(client), issue.NewMongoRepository(client), ...
```

Rules:

- Keep the `Repository` contract small enough for service fakes.
- Assert that each component's concrete `MongoRepository` implements its local `Repository` contract.
- Translate driver errors to component errors inside the repository implementation.
- Let the implementation depend only on shared Mongo primitives, never on another component's repository implementation.

## Dependency-selection checklist

Before writing cross-cutting infrastructure, check:

- Is the need already covered safely by the standard library?
- Does Fiber or the selected database/queue client provide maintained middleware?
- Is there a mature project with recent releases, clear docs, tests, and an acceptable license?
- Does it support the project's Go version and Fiber major version?
- Is its API smaller and easier to replace than the custom code it avoids?
- Can behavior be tested behind a consumer-owned interface where a boundary is useful?

Search and verify at implementation time because library health and versions change. Prefer official docs, project repositories, and pkg.go.dev over secondary tutorials.

## Generation candidates

Choose tools only when the project needs the corresponding contract:

- OpenAPI: generate typed transport contracts from the API schema.
- SQL: generate type-safe query bindings from reviewed SQL.
- Protobuf: use the official protobuf toolchain for protocol contracts.
- Mocks: generate only for important owned interfaces when a hand-written fake would be repetitive.

Pin tool versions through the repository's established mechanism. Keep generation reproducible and verify that regeneration produces no unexplained diff.
