# Configuration, Vault, bootstrap, and logging

Use this reference to implement startup configuration and the `cmd/<app>` composition root.

## Contents

- Configuration precedence
- Bootstrap Vault from the environment
- Load Vault configuration safely
- Keep secrets out of observable state
- Own composition in cmd
- Use slog with two output modes

## Configuration precedence

Choose one stable uppercase prefix derived from the application, for example `NUDGE_`. Document every supported key and reject ambiguous duplicate spellings.

Apply sources field-by-field in this order:

```text
typed defaults < Vault values < prefixed environment overrides
```

For example, Vault may provide `database_url`, while `NUDGE_DATABASE_URL` intentionally overrides it. Decode all sources into one typed `Config`, normalize once, and validate the final result. Report missing or invalid key names together where practical, but never include secret values.

Avoid `map[string]any` beyond the source boundary. Use explicit duration, URL, enum, byte-size, and list parsing. Distinguish unset from explicitly empty when empty is meaningful. Do not mutate configuration after dependency construction.

## Bootstrap Vault from the environment

Reserve a narrow prefixed namespace such as:

```text
NUDGE_VAULT_ENABLED
NUDGE_VAULT_ADDR
NUDGE_VAULT_NAMESPACE
NUDGE_VAULT_AUTH_METHOD
NUDGE_VAULT_PATH
NUDGE_VAULT_MOUNT
NUDGE_VAULT_ROLE_ID
NUDGE_VAULT_SECRET_ID
NUDGE_VAULT_TOKEN
```

Names are examples; keep the selected set small and consistent. Treat Vault as configured only through an explicit enable flag or a complete required bootstrap set. Do not probe arbitrary local endpoints to guess availability.

Use the official `github.com/hashicorp/vault/api` client. Construct its configuration explicitly from the prefixed bootstrap values; do not accidentally mix prefixed application settings with ambient `VAULT_*` variables unless the repository deliberately standardizes on those names.

Prefer workload identity such as Kubernetes auth when deployed on Kubernetes. Use AppRole for machine workflows where workload identity is unavailable. Accept a directly supplied token only for an intentional deployment integration or local development; never use a root token. Scope policies to the exact application path and operations.

HashiCorp describes AppRole as machine-oriented and recommends short TTLs plus renewal where possible. Kubernetes auth binds Vault access to the pod service account identity.

Primary sources: [Vault AppRole](https://developer.hashicorp.com/vault/docs/auth/approle), [AppRole best practices](https://developer.hashicorp.com/vault/docs/auth/approle/approle-pattern), and [Vault Kubernetes auth](https://developer.hashicorp.com/vault/docs/auth/kubernetes).

## Load Vault configuration safely

Read one application path or a small documented set beneath an application/environment prefix. With the client's KV v2 helper, for example, use mount `kv` and logical path `nudge/production/api`; let the client construct the versioned HTTP path. Keep the environment and app identity in the path, not supplied by untrusted request data.

Map Vault keys to canonical configuration field names, then apply prefixed environment overrides. Reject unknown Vault keys when a typo could hide configuration drift; otherwise warn with names only. Validate required keys after all overrides.

If Vault is configured:

1. Create the client with TLS verification enabled and a bounded startup timeout.
2. Authenticate once with the selected method.
3. Read the required configuration path.
4. Decode values without logging the response.
5. Apply environment overrides and validate.
6. Retain the client only if runtime renewal, dynamic secrets, or later reads are required.

Fail startup when authentication, required reads, decoding, or validation fails. Do not silently fall back to environment-only configuration after Vault was explicitly enabled. This prevents an outage or policy error from changing the application's secret source unnoticed.

If a renewable token or dynamic secret remains in use, manage its lease with the official lifetime watcher, tie it to application context, and define behavior for renewal failure. Prefer reusing and renewing an issued token over authenticating for every read.

Primary source: [Vault authentication and token renewal](https://developer.hashicorp.com/vault/docs/concepts/auth).

## Keep secrets out of observable state

- Mark configuration fields as sensitive in metadata or explicit redaction code.
- Never log the full config, Vault response, token, SecretID, database URL credentials, headers, cookies, or environment.
- Do not expose secrets in health endpoints, public configuration responses, metrics labels, traces, panic messages, command arguments, or generated client assets.
- Expose only explicitly allowlisted public values through a dedicated transport response when a client needs runtime configuration.
- Report the active source for non-sensitive settings only when useful, such as `source=vault+env-overrides`, without revealing values.
- Ensure errors wrap operational context while excluding request/response bodies that may contain secrets.

## Own composition in cmd

Use an explicit sequence in `cmd/<app>`:

```text
main.go       signal context, call run, exit status
config.go     bootstrap env, source merge, typed validation
logging.go    bootstrap logger and final slog handler
bootstrap.go  instantiate clients -> repositories -> services -> transports
```

Keep these files in `package main`. Put reusable parsing or Vault adapter code under a focused `internal/config` package only when multiple commands share it, while leaving each command's final config type, policy, and wiring visible at the composition root.

Use a minimal bootstrap logger before final configuration is available. After merging and validating config, construct the final logger and pass it into dependencies. Build dependencies leaf-first in DAG order and register cleanup immediately after each successful resource acquisition. On partial startup failure, close already-created resources in reverse order.

Avoid package-level mutable config, `init` functions, implicit singleton clients, reflection-driven dependency injection, and service locators. A small explicit bootstrap function is easier to review and test.

## Use slog with two output modes

Use the standard library `log/slog` API so call sites remain handler-independent. Default to pretty stdout output with level, time, message, and structured attributes. A maintained `slog.Handler` such as `github.com/lmittmann/tint` is an appropriate colored handler after verifying its current maintenance and API. Use `slog.JSONHandler` for JSON mode.

Primary sources: [Go `log/slog`](https://pkg.go.dev/log/slog), [Go `x/term`](https://pkg.go.dev/golang.org/x/term), and [tint repository](https://github.com/lmittmann/tint).

Color behavior:

- Write to stdout as required by this stack.
- Enable color for an interactive terminal by default; use `term.IsTerminal(int(os.Stdout.Fd()))` or the selected handler's equivalent.
- Disable ANSI color when stdout is not a TTY, `NO_COLOR` is present, or configuration explicitly disables it.
- Never emit ANSI escapes in JSON mode.

Logging behavior:

- Use lowercase `snake_case` attribute keys consistently.
- Attach stable component context with `logger.With("component", "queue")`.
- Pass context to `InfoContext`, `ErrorContext`, and related methods when request cancellation or trace extraction matters.
- Log a failure once at the layer that handles or terminates it. Lower layers return wrapped errors unless they add a distinct operational event.
- Keep high-cardinality or unbounded values out of routine logs unless needed for diagnosis.
- Avoid logging successful hot-path operations at info level; use debug or aggregated metrics.
- Use lazy values or level checks for expensive attribute construction.

Test handler selection, configured level, redaction, environment-over-Vault precedence, Vault-disabled behavior, Vault-required failure, parsing, and final validation with fast table-driven tests. Use a fake config source or `httptest.Server`; do not require a live Vault instance in the fast suite.
