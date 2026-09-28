# cli

standardized CLI framework and flag parsing.

## Flag validators

`Root.WithFlagValidator(name, fn)` registers a closure that rejects ill-formed
values for a persistent flag and routes the rejection through kit's structured
`output.RenderError` envelope (honoring `--format json|yaml|table|text`).

```go
root.WithFlagValidator("api-version", func(v string) *output.Error {
    if semver.IsValid(v) {
        return nil
    }
    return &output.Error{Code: "INVALID_API_VERSION",
        Message: "api-version must be semver", ExitCode: 2}
})
root.WrapRunE() // installs the validator on every leaf
```

The middleware:

- runs once per leaf invocation, AFTER cobra parses the flag and BEFORE the adopter RunE
- only fires when the user actually set the flag (`flag.Changed == true`); defaults pass through
- last-registered wins for a given name (ergonomic for tests)
- silently never fires when the named flag doesn't exist anywhere on the tree (no panic)
- is inert unless registered BEFORE `WrapRunE` (or `Execute`, which calls it)

## Flag enums and parse-time errors

`Root.WithFlagEnum` records a flag's legal values once; parse-time errors,
help text and shell completion all read that one annotation. Unknown or
value-less flags come back as a structured `USAGE` envelope carrying a
concrete fix or alternatives.

Suggest-only by default. `cli.autocorrect` (`off|prompt|read`, via
`--autocorrect` / `KIT_AUTOCORRECT` / config) opts into applying the fix —
`read` only on `kit/side-effect: read` leaves, `prompt` only on a terminal,
answered from `/dev/tty`. An applied rewrite reports `corrected_from` in the
envelope and re-dispatches through every gate.
See [flag-enums.md](../../../docs/adopters/reference/flag-enums.md).

## Served commands

`<tool> serve` supervises services; kit ships four, each projecting the
command tree through the same gates (policy, `WithPermission`,
`WithAuditSinks`, `WithRootFactory`):

| Option | Service | Reached by |
|--------|---------|------------|
| `WithAPI(APIConfig{})` | `api` (on by default) | REST under `/v1/commands`, `127.0.0.1:8080` |
| `WithSocket(SocketConfig{})` | `socket` | NDJSON over a `0600` Unix socket |
| `mcpserve.With(mcpserve.Config{})` | `mcp` | MCP tools over streamable HTTP (`127.0.0.1:8081/mcp`), or stdio with `serve mcp --stdio` |
| `rpcserve.With(rpcserve.Config{})` | `rpc` | `cmdsurface.v1.Commands` over Connect, gRPC and gRPC-Web, h2c on `127.0.0.1:8082` |

`mcp` lives in [`mcpserve/`](mcpserve/) and `rpc` in [`rpcserve/`](rpcserve/),
so only a CLI that serves them links the MCP SDK or `go/transport/rpc`.
`mcp` over stdio admits `kit/auth-required` leaves on the spawn's trust and
keeps stdout for the protocol; `kit/requires-confirmation` leaves need an
accepted elicitation or, over HTTP, `X-Confirm-Token`. Normative text:
[the mcp service](../../../docs/contracts/serve-lifecycle.md#the-mcp-service),
[the rpc service](../../../docs/contracts/serve-lifecycle.md#the-rpc-service).

## Sub-packages

| Path | What it answers |
|------|-----------------|
| [`breaker/`](breaker/README.md) | `breaker` subcommand tree: which breakers are registered, their state, closing one |
| [`cmdmeta/`](cmdmeta/README.md) | read the `kit/*` annotations on a cobra command without importing `cli` |
| [`completion/`](completion/README.md) | dynamic shell completions for flags and positionals |
| [`config/`](config/README.md) | `config path` and `config paths` subcommands: which file loads and the precedence chain |
| [`conformance/`](conformance/README.md) | the `kit conformance` command tree and its exit codes |
| [`idemstore/`](idemstore/README.md) | storage backend for `--idempotency-key` recorded results |
| [`policy/`](policy/README.md) | delegation-safety policy YAML enforced per agent-driven invocation |
| [`router/`](router/README.md) | `kit llm router` subtree: start, stop, list, inspect RouteLLM instances |
| [`scope/`](scope/README.md) | `kit scope show`, `check`, `test`: would the path policy allow this path |
