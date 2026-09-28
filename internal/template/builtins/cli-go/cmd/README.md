# cmd

Go CLI template entry points: the kit root and the commands mounted on it.

## What the rendered tier-3 project guarantees

`root.go` builds the root with `cli.New` and five options, in this
order:

| Option | Why it is there |
|--------|-----------------|
| `cli.WithStatus(cli.StatusConfig{})` | every kit root is validated to carry the reserved `status` verb; without it `Execute` refuses to run |
| `cli.WithAPI(cli.APIConfig{})` | registers the `api` service: the command tree over REST on `127.0.0.1:8080`, discovery at `/v1/commands`, an OpenAPI document; enabled by default under a bare `serve` |
| `cli.WithSocket(cli.SocketConfig{})` | registers the `socket` service: the same tree over an owner-only Unix socket; registered but not enabled, per the serve contract's default |
| `mcpserve.With(mcpserve.Config{})` | registers the `mcp` service: the same tree as MCP tools over streamable HTTP on `127.0.0.1:8081/mcp`, or over stdio with `--stdio`; registered but not enabled. It is the one option that links the MCP SDK (about 2 MB stripped); removing it and its import drops the SDK |
| `rpcserve.With(rpcserve.Config{})` | registers the `rpc` service: the same tree as the `cmdsurface.v1.Commands` service for Connect, gRPC and gRPC-Web clients on `127.0.0.1:8082`; registered but not enabled. It adds about 0.4 MB stripped (0.6 MB with symbols); removing it and its import drops it |

It then mounts `spec` with `toolspec/cli.RegisterSpecCommand`: the
manifest agents read, and `spec coverage`, which lists commands that
declare no `kit/side-effect`. A fresh project passes
`spec coverage --min 100`; keep it there.

Nothing else is mounted by hand. Reflection happens when a service
starts, so a command file added to this package is served the next
time `serve` runs. The destructive ceiling, the confirmation gate, the
loopback default and the unauthenticated-remote refusal all come from
kit; the template sets no `Policy`, so destructive commands are
withheld from every served surface until the adopter names one.

`root.go` also layers configuration into `root.Viper` from kit's own
`-c/--config` flag, the `<NAME>_*` environment, and the
system/user/project files `config.OptionsForTool` resolves. That is
what carries `services.api.addr`, `services.socket.path`,
`services.rpc.addr`, and the rest of the `services.*` block to the
supervisor.

`hello.go` is the sample command. It registers itself from an `init`
function — the convention every command file in this package follows
— and carries the annotations kit validates at startup (`Short`,
`Long`, `kit/side-effect`, `kit/idempotent`, `kit/top-level-verb`)
plus an output schema, so it answers in `data` over REST, the socket
and gRPC, and in `structuredContent` over MCP.

## What gates a change here

`AGENTS.md.tmpl` is the agent-facing description of the same surface,
shipped at tiers 3 and 4 beside `root.go`. It is gated by the same
`go test ./cmd/kit/init/` run: `TestBootstrap_CLIGo_AgentsFragment`
asserts it renders with the tool's own name and that tiers 1 and 2 do
not get it, and the serve test above pins the discovery reasons and
endpoints the fragment documents. A change to the served surface that
makes the fragment wrong should fail one of those two.

`go test ./cmd/kit/init/` in poly-kit renders this template through
the real bootstrap path, compiles the result against the checkout of
kit, and drives the binary:
`TestBootstrap_CLIGo_ServesItsCommandsWithoutWiring` asserts
`spec coverage --min 100`, `serve --list`, the contract's `serve`
flags, `serve api` on loopback,
discovery reasons (`unauthorized-destructive`, `self-hosting`,
`management-only`), a read over REST answering in `data`, the 404 on
a destructive route, the exit-2 refusal of `--addr 0.0.0.0:0`, a
socket path reaching the socket service through `-c`, an MCP session
over stdio spawned from the README's host entry, `serve mcp` on its
own loopback listener, the exit-2 refusal of `--mcp-addr 0.0.0.0:0`,
the README's `Invoke` call answered by `serve rpc` on its own loopback
listener, and the exit-2 refusal of `--rpc-addr 0.0.0.0:0`. The
README's `serve --list` table is compared with the binary's output.
Run it after every edit to a `.tmpl` here, then `make builtins-sync` so the
embedded mirror under `internal/template/builtins/` matches.

Go template actions and Go composite literals share `{{`: write
`[]T{x}` with a named value rather than `[]T{{...}}` inside a `.tmpl`.
