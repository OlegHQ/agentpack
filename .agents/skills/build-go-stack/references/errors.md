# Contextual errors and final-boundary logging

Use this reference to make failures traceable without duplicate logs or leaked implementation details.

## Wrap with useful context

Use the standard library and wrap errors at meaningful abstraction boundaries:

```go
dog, err := repository.FindByID(ctx, id)
if err != nil {
	return Dog{}, fmt.Errorf("find dog %q: %w", id, err)
}
```

Write error context as a concise lowercase operation without trailing punctuation. Include safe identifiers or state that materially locates the failure. Do not include secrets, tokens, full request bodies, credentials, sensitive personal data, raw SQL, or noisy values.

Add context when the current layer contributes meaning. Do not wrap every helper call with redundant function/package names. A final chain should read naturally from high-level operation to root cause.

Use `%w` when callers may inspect the underlying stable error with `errors.Is` or `errors.As`. Use `errors.Is` rather than equality and `errors.As` rather than direct type assertions across wrapped chains.

Primary sources: [Go errors package](https://pkg.go.dev/errors) and [Working with Errors in Go](https://go.dev/blog/go1.13-errors).

## Preserve abstraction boundaries

Wrapping with `%w` makes the underlying error observable to callers. Translate implementation-specific errors before they leave infrastructure:

```go
if errors.Is(err, sql.ErrNoRows) {
	return Dog{}, fmt.Errorf("find dog %q: %w", id, dogs.ErrNotFound)
}
return Dog{}, fmt.Errorf("query dog %q: %v", id, err)
```

Use `%v` at this boundary when the vendor error text is useful for diagnostics but its identity must remain private. Component services and HTTP mapping must depend only on owned sentinel or typed errors. Do not make `sql.ErrNoRows`, a queue-client type, or a vendor status code part of the component API accidentally.

Define stable sentinel or typed errors only when callers need different behavior. Document them and always expect callers to use `errors.Is` or `errors.As`. Do not classify errors by matching strings.

## Return errors upward without logging

Repositories, services, helpers, retry functions, and intermediate transport code must return errors rather than log them. Context accumulates in the error chain; logs are observable output and belong to the layer that decides the final outcome.

Use these final owners:

- A centralized Fiber error handler maps an unhandled request error to a safe HTTP response and logs an unexpected server failure once with request/trace context.
- A worker or scheduled job boundary logs once after retry policy is exhausted, with job identity and attempt count.
- `main` logs once when startup fails or a fatal runtime error terminates the process.
- A command invocation boundary logs or prints once before choosing its exit status.

An individual HTTP handler should normally return the error to the centralized error handler instead of logging it. Expected outcomes such as not found, validation failure, authentication failure, or conflict should be mapped to their response and normally should not produce error-level logs. Use metrics, audit events, debug logs, or rate-limited warnings only when the product or security requirement justifies them.

Do not both log and return the same error. Logging before a retry is complete creates noise; record the final exhausted failure once. Log a transient attempt only when the attempt itself is an independently actionable event, and use a level appropriate to that event.

## Map errors at the HTTP edge

Map owned error identities centrally with `errors.Is` and `errors.As`. Return stable public error codes and safe messages; never serialize the internal error chain to clients.

Keep Fiber out of component packages. The HTTP boundary decides status codes, response schemas, and whether an unexpected error deserves an error log. Attach structured attributes such as `request_id`, `trace_id`, route, method, and safe entity ID rather than formatting them into the message.

Handle context cancellation and deadlines deliberately so disconnected clients or normal shutdown do not become noisy server errors. Preserve the underlying identity with `%w` where the final boundary needs to classify them.

## Handle multiple and cleanup errors

Use `errors.Join` only when multiple independent errors must be preserved, such as a primary operation failure plus a meaningful cleanup failure. Do not let a deferred close overwrite the primary error. Avoid joining routine harmless close results that add no actionability.

Never ignore an error without an explicit reason. When an error is intentionally discarded, make the reason locally obvious and verify that it cannot affect correctness or observability.

## Test error behavior

- Assert behavior with `errors.Is` and `errors.As`, not full error strings.
- Assert that wrapping preserves the stable identity and adds useful operation context.
- Test infrastructure translation from vendor errors to owned errors.
- Test centralized HTTP status/code mapping and safe response bodies.
- Test that expected client errors are not error-logged and unexpected failures are logged once.
- Test retry exhaustion produces one final error event rather than one duplicate event per layer.
