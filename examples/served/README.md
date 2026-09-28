# served

## What it answers

Does a kit CLI built with `cli.New` and options alone, nothing mounted by
hand, meet every claim the
[serve-lifecycle contract](../../docs/contracts/serve-lifecycle.md) makes
about a conformant application command? `main.go` registers the reserved
`status` and `audit` verbs, the kit-shipped `api`, `socket`, `mcp` and `rpc` services,
one adopter-owned service (`heartbeat`), a bus and an identity keypair,
plus one command per class the contract distinguishes. For the surface matrix without the
serve lifecycle, see [`examples/cmdsurface`](../cmdsurface/README.md).

## Use it when

- you want the reference root to diff your own against → `main.go`
- you need to know what a claim means on the wire → the test that pins it
- a claim holds here and not in your tool → the difference is in your wiring

## Quick start

```sh
go run ./examples/served item list
go run ./examples/served serve --list
go run ./examples/served serve api --addr 127.0.0.1:0
go run ./examples/served serve mcp --stdio
go test -race ./examples/served/
```

## Contract

| Command      | Class              | Served as                                                  |
|--------------|--------------------|------------------------------------------------------------|
| `item list`  | read, output schema | `GET /v1/commands/item/list`, answers in `data`; MCP tool `item.list`, answers in `structuredContent` |
| `item watch` | read, long-running | `GET /v1/commands/item/watch/stream`, one event per line until done or disconnected |
| `item add`   | write-local, `kit/args: name` | `POST /v1/commands/item/add` with `{"args":["washer"]}`; MCP tool `item.add` with the same `args` |
| `item tag`   | write-local, `kit/requires-confirmation` | MCP tool `item.tag`: runs once a person approves the elicitation |
| `item sync`  | read, `kit/auth-required` | `401 unauthenticated` over REST and HTTP MCP without `Auth`; runs over the socket and MCP stdio |
| `item export` | read, `kit/permissions: items:export` | `403 insufficient_scope` + `WWW-Authenticate` over REST, `isError` over HTTP MCP, for a caller without the scope; runs over the socket and MCP stdio (the owner) |
| `item purge` | destructive-shared | withheld (`unauthorized-destructive`) until a surface is named, then needs `confirm` |
| `shell`      | interactive        | never: 404 + `interactive` over REST, `NOT_INVOCABLE` over the socket |
| `upgrade`    | `kit/self-hosting` | never: 404 + `self-hosting` over REST, `NOT_FOUND` over the socket |
| `serve`      | kit's own          | never: `self-hosting`                                      |
| `status`     | reserved           | never: `management-only`                                   |
| `audit verify` | reserved         | never: `management-only`                                   |
| `token create`, `token verify` | reserved | never: `management-only`; `create` signs with the identity keypair |

The tests drive the real `Execute` path, the one that installs the
confirmation and policy gates, with the arguments an operator would
type. Each test's name is the claim it pins; its doc comment spells the
claim out.

- `served_test.go`: the serve hierarchy and `--list`, readiness on the
  bus and the log, discovery, REST and the socket, the destructive
  ceiling, exposure refusals, the adopter service, the audit chain and
  `audit verify` (exit 71 on an edited record)
- `mcp_test.go`: the `mcp` service over HTTP
- `rpc_test.go`: the `rpc` service over Connect, gRPC (h2c) and gRPC-Web
- `idempotency_test.go`: a write retried with its `Idempotency-Key` runs
  once, over REST and the socket
- `mcp_stdio_test.go`: the built binary as a desktop host spawns it,
  `serve mcp --stdio`
- `permission_rules_test.go`: the `permissions:` rules of a `--policy`
  file in `$XDG_CONFIG_HOME/served/policies/`, refusing over REST, and a
  rule that does not compile refusing the start (exit 2)
- `token_test.go`: `token create`, then `serve` with
  `services.all.auth.mode: jwt`, then `item sync` with the token over
  REST and RPC

## See also

- [serve-lifecycle contract](../../docs/contracts/serve-lifecycle.md): the normative text
- Task guides: [REST](../../docs/adopters/guides/expose-cli-over-rest.md),
  [Unix socket](../../docs/adopters/guides/serve-cli-over-unix-socket.md),
  [MCP](../../docs/adopters/guides/expose-cli-over-mcp.md),
  [gRPC](../../docs/adopters/guides/expose-cli-over-grpc.md)
