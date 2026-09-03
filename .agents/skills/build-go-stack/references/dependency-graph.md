# Dependency inversion and package graph

Use this reference to design entities, repositories, services, and cross-component workflows without circular or spaghetti dependencies.

## Contents

- Think in a DAG
- Apply parent-to-child ownership
- Prefer concrete structs
- Invert only real boundaries
- Place orchestration deliberately
- Review and test boundaries

## Think in a DAG

Model internal package imports as a directed acyclic graph (DAG), even when the business language sounds hierarchical.

```text
cmd/api                         composition root
  |
  +--> api transport
  +--> house application/service
          |
          +--> house entity
          +--> dog service
          |      |
          |      +--> dog entity             leaf
          |      +--> dog persistence port
          |
          +--> messaging capability

infrastructure/postgres/dogs
  +--> dog entity or consumer-owned port
```

Arrows mean "imports or depends on." They must only point downward. Go rejects direct import cycles, but architectural cycles can still occur through callbacks, global registries, or mutually calling interfaces; reject those as well.

Use a strict tree when ownership is naturally a tree. Permit a DAG when an orchestration package legitimately consumes multiple leaves. Never force duplicate types merely to preserve a visual tree.

## Apply parent-to-child ownership

Permit `house` to import `dog` when a house owns or refers to dogs. Forbid `dog` from importing `house`.

If both need an address, identifier, clock, or other stable concept, extract only that cohesive concept into a lower leaf package. Do not create a broad `shared`, `common`, or `types` package.

If dog behavior must notify a house workflow, return a result or component event upward. Let a higher-level orchestrator decide what happens next. Do not call the parent service from the child.

## Prefer concrete structs

Use concrete implementations by default:

```go
type DogRepository struct {
	db *sql.DB
}

func NewDogRepository(db *sql.DB) *DogRepository {
	return &DogRepository{db: db}
}

type DogService struct {
	repository *DogRepository
}

func NewDogService(repository *DogRepository) *DogService {
	return &DogService{repository: repository}
}
```

Keep `DogRepository` datastore details private. Expose task-oriented methods such as `FindByID`, `Save`, or `ListByHouseID`; do not expose the database handle, query builder, or storage records.

Use this direct concrete dependency only when service and repository belong to the same implementation boundary and the service package is allowed to import that repository package. Across the component-to-infrastructure boundary, use the consumer-owned port below so dependency direction remains inverted.

## Invert only real boundaries

Define an interface in the consuming package—not the implementing package—primarily when the dependency must be replaced with a fast deterministic fake in consumer tests. Also allow one when production genuinely has multiple implementations or the consumer needs only a narrow capability.

- a narrow fake materially improves important test speed, isolation, or determinism;
- production has multiple implementations;
- the consumer requires only a small subset of a broad dependency;
- the boundary represents a capability such as transactions, time, publishing, or object storage.

```go
// Package dogs owns the capability it consumes.
type Repository interface {
	FindByID(ctx context.Context, id ID) (Dog, error)
	Save(ctx context.Context, dog Dog) error
}

type Service struct {
	repository Repository
}
```

The concrete infrastructure `DogRepository` satisfies this interface implicitly. Do not write `DogRepositoryInterface`, mirror every concrete method, or add compile-time indirection without a consumer need.

Keep interfaces small and behavior-oriented. Accept interfaces; return concrete structs. Constructors must validate mandatory dependencies when a nil dependency could cause delayed failure.

## Place orchestration deliberately

A service may aggregate repositories, lower-level services, and infrastructure capabilities. It must not become a universal coordinator.

- Put behavior concerning one aggregate in that aggregate's service.
- Put a workflow spanning house and dog under the component/application package that owns the user-visible use case, commonly `house` if house is the parent.
- Extract a separate use-case package when neither component owns the workflow cleanly.
- Pass results downward or return events upward; avoid peer services calling each other recursively.
- Keep transactions at the highest layer that owns all writes in the atomic operation.
- Publish external messages after persistence through an established transactional pattern when consistency requires it; do not hide distributed workflows inside entity methods.

## Review every new dependency

Before adding an import or constructor field, answer:

1. Is the dependency lower and more stable than the consumer?
2. Does the consumer need the whole dependency or a smaller capability?
3. Could this dependency create a reverse path, callback cycle, or service loop?
4. Is the use case in the correct owning package?
5. Can data or an event flow upward instead of the child calling its parent?
6. Does a new abstraction remove coupling, or only add another name?

Sketch the package graph for non-trivial changes. Reject cycles and high fan-out packages before implementation.

## Test the boundaries

- Test leaf entities with table-driven tests and no infrastructure.
- Test concrete repositories with integration tests against the real datastore boundary where practical.
- Test services with concrete lightweight dependencies or small fakes for consumer-owned interfaces.
- Add architecture checks when the repository grows enough to regress: inspect `go list -deps`, enforce forbidden imports, and keep `go test ./...` cycle-free.
- Treat a test requiring mocks for most of the system as a design warning, not a reason to generate more mocks.
