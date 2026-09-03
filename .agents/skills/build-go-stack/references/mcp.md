# MCP integration

Use the official Model Context Protocol Go SDK instead of implementing protocol machinery inside the application.

## Library and version

Use `github.com/modelcontextprotocol/go-sdk/mcp`. It is the official Go SDK maintained by the Model Context Protocol project in collaboration with Google. Select a current stable v1 release compatible with the repository's Go version; v1.7.0 and newer support MCP protocol `2026-07-28` while retaining negotiation compatibility with the older supported protocol versions.

Pin the selected release in `backend/go.mod` and review its release notes before upgrading. MCP protocol and transport behavior are version-sensitive; verify APIs against the installed module and current official documentation rather than memory.

Do not add a second MCP framework or use the SDK's low-level `jsonrpc` package to recreate behavior already provided by `mcp`.

## Shared server definition

Construct one focused MCP server definition from injected component services:

```go
server := mcp.NewServer(
    &mcp.Implementation{Name: "nudge", Version: version},
    &mcp.ServerOptions{Logger: logger},
)
mcp.AddTool(server, &mcp.Tool{
    Name:        "create_issue",
    Description: "Create an issue in the active workspace.",
}, createIssueHandler(issueService))
```

Use concrete input and output structs so the SDK generates and validates schemas. Keep MCP handlers as transport adapters: decode transport identity, call the same component workflow used by REST/UI/CLI, and map its typed result. Do not duplicate authorization, validation, persistence, or product workflows in MCP packages.

Keep the reusable server builder outside the executable composition roots when both stdio and HTTP mount it. Keep dependency construction, configuration, logging, and shutdown in `cmd/<command>`.

## Stdio

For a local stdio adapter, run the server with the SDK transport:

```go
if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
    return fmt.Errorf("run MCP stdio server: %w", err)
}
```

Write protocol traffic only through the SDK on stdout. Send diagnostics through the injected logger to stderr, or configure logging so stdout cannot be contaminated. Do not scan newline-delimited JSON or dispatch method names manually.

When the product contract defines the stdio command as an API client rather than a database process, inject an HTTP-backed component capability into the same typed tool handlers; do not silently give the adapter direct database access.

## Streamable HTTP and Fiber

Create remote MCP transport with `mcp.NewStreamableHTTPHandler`. It returns a standard `net/http.Handler`; Fiber v3 can register `net/http.Handler` values directly, so do not write a JSON-RPC bridge or copy request and response bodies manually.

When remote MCP is part of the application contract, mount it in the production Go API binary. Treat a stdio adapter as an additional client surface, never as a substitute for the mounted endpoint. Preserve separate full-access and read-only endpoint contracts by filtering advertised tools as well as enforcing authorization at execution time.

```go
mcpHandler := mcp.NewStreamableHTTPHandler(
    func(*http.Request) *mcp.Server { return server },
    &mcp.StreamableHTTPOptions{Stateless: true},
)
app.All("/mcp", mcpHandler)
```

Confirm the exact Fiber route form against the installed Fiber version. Register MCP before any catch-all route. Preserve all MCP request/response headers and streaming behavior; add an integration test through Fiber, not only a direct `httptest` test of the SDK handler.

Use stateless Streamable HTTP for protocol `2026-07-28`. Only enable stateful sessions, event stores, resumability, or server-to-client features when the product explicitly needs them and their lifecycle works with the deployment topology. Do not use an in-memory event store to imply durable production resumability.

Authenticate the HTTP endpoint before handing a request to the MCP SDK. Convert the validated service-account token into the same typed principal used by component services. Apply origin validation and the SDK's current transport-security guidance when browser-origin requests are possible. Never forward an unvalidated bearer token through tool arguments.

## Errors and tests

Return ordinary Go errors or SDK-supported structured tool errors from handlers; let the SDK own JSON-RPC error envelopes and protocol codes. Preserve component error identity long enough to map expected not-found, conflict, forbidden, and validation outcomes. Log unexpected failures once at the MCP command/request boundary.

Test with SDK clients and transports:

- use `mcp.NewInMemoryTransports` for fast tool contract tests;
- use the SDK client over the real stdio command to prove stdout framing and lifecycle behavior;
- use `mcp.StreamableClientTransport` through the Fiber endpoint to prove mounting, authentication, negotiation, and headers;
- inventory every first-party tool name and exercise its typed input, authorization boundary, component effect, and result shape;
- test at least one older supported protocol client when backward negotiation is a product requirement.

Primary references: the official Go SDK README, `docs/server.md`, `docs/protocol.md`, release notes, and the MCP transport specification.
