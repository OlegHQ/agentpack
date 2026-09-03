# Concrete types and minimal JSON transforms

Use this reference to keep Go data flow static, cheap, and easy to follow.

## Prefer one meaningful type

Use a component entity directly across service and read-response boundaries when its semantics and JSON shape match the public API. Do not mechanically create `DogModel`, `DogRecord`, `DogDTO`, and `DogResponse` with identical fields.

Keep identifiers, enums, timestamps, money, and other values strongly typed. Prefer concrete structs and slices over `any`, `interface{}`, `map[string]any`, `[]any`, reflection mappers, and generic property bags.

Dynamic data is acceptable only where the external protocol is genuinely dynamic, such as raw Vault KV or arbitrary third-party JSON. Decode and validate it into a concrete type immediately at that boundary. Do not let dynamic values propagate into services or component entities.

## Export entities safely

Put stable public JSON names on an entity when JSON is an intentional supported representation:

```go
type Dog struct {
	ID           ID        `json:"id"`
	Name         string    `json:"name"`
	HouseID      HouseID   `json:"house_id"`
	CreatedAt    time.Time `json:"created_at"`
	InternalNote string    `json:"-"`
}
```

Use `json:"-"` for fields that must never leave the process. Prefer unexported fields when callers do not need access. Test the serialized key set so adding a field cannot accidentally expand the public API unnoticed.

When one entity needs a small computed field or conditional representation, implement localized `MarshalJSON` using a private alias/helper with concrete fields. Keep the method deterministic and side-effect free. Do not convert the entity to `map[string]any`, delete keys, and encode it again.

Treat denylist-style scrubbing carefully: secrets and credentials should be excluded at the type definition, not remembered by individual handlers. If the same entity has materially different public and privileged views, use separate explicit view types rather than conditional security logic scattered across handlers.

## Separate writes from entities

Do not decode create or update JSON directly into a stored entity when that permits clients to set IDs, ownership, audit fields, roles, status, or other server-controlled values. Use a small concrete command/input struct containing only writable fields:

```go
type CreateDogInput struct {
	Name    string  `json:"name"`
	HouseID HouseID `json:"house_id"`
}
```

Validate unknown fields, required values, bounds, and formats at the transport boundary. Pass concrete validated values into the service. A write input is justified by authority and validation semantics; it is not redundant DTO ceremony.

## Add response types only for real contracts

Create a dedicated response type when the endpoint:

- combines multiple entities or services;
- adds pagination, links, cursors, or metadata;
- exposes a versioned external contract that must evolve independently;
- requires a different privileged/public view;
- returns an allowlisted public client-configuration document;
- cannot safely expose the entity's stable JSON representation.

Keep such types private or transport-local unless they are generated from a shared API contract. Construct them explicitly; avoid auto-mapping libraries and reflection.

## Avoid unnecessary persistence transforms

Let repositories scan directly into an entity when database columns and component representation match and no persistence concern leaks upward. Use a private database record only when nullability, normalization, joins, driver types, encryption, or schema shape genuinely differs. Keep the conversion beside the repository and perform it once.

Do not add transformations merely to preserve a diagram. Each conversion must enforce a boundary, change semantics, or protect a contract. Remove passthrough mappers that only copy equal fields.

## Verify the boundary

- Test exact JSON keys for entities containing internal fields.
- Test that secrets and server-controlled fields never serialize.
- Test that write decoding rejects unknown or forbidden fields.
- Benchmark only hot serialization paths with representative payloads; avoid speculative optimization.
- Prefer compile errors from concrete type changes over runtime assertions from dynamic maps.
