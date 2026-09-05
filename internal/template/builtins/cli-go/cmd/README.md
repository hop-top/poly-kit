# cmd

Go CLI template entry points: the kit root and the commands mounted on it.

## What the rendered tier-3 project guarantees

`root.go` builds the root with `cli.New` and three options, in this
order:

| Option | Why it is there |
|--------|-----------------|
| `cli.WithStatus(cli.StatusConfig{})` | every kit root is validated to carry the reserved `status` verb; without it `Execute` refuses to run |
| `cli.WithAPI(cli.APIConfig{})` | registers the `api` service: the command tree over REST on `127.0.0.1:8080`, discovery at `/v1/commands`, an OpenAPI document; enabled by default under a bare `serve` |
| `cli.WithSocket(cli.SocketConfig{})` | registers the `socket` service: the same tree over an owner-only Unix socket; registered but not enabled, per the serve contract's default |

Nothing else is mounted by hand. Reflection happens when a service
starts, so a command file added to this package is served the next
time `serve` runs. The destructive ceiling, the confirmation gate, the
loopback default and the unauthenticated-remote refusal all come from
kit; the template sets no `Policy`, so destructive commands are
withheld from every served surface until the adopter names one.

`root.go` also layers configuration into `root.Viper` from kit's own
`-c/--config` flag, the `<NAME>_*` environment, and the
system/user/project files `config.OptionsForTool` resolves. That is
what carries `services.api.addr`, `services.socket.path`, and the
rest of the `services.*` block to the supervisor.

`hello.go` is the sample command. It registers itself from an `init`
function — the convention every command file in this package follows
— and carries the annotations kit validates at startup (`Short`,
`Long`, `kit/side-effect`, `kit/idempotent`, `kit/top-level-verb`)
plus an output schema, so it answers in `data` over REST and the
socket.

## What gates a change here

`go test ./cmd/kit/init/` in poly-kit renders this template through
the real bootstrap path, compiles the result against the checkout of
kit, and drives the binary:
`TestBootstrap_CLIGo_ServesItsCommandsWithoutWiring` asserts `serve
--list`, the contract's `serve` flags, `serve api` on loopback,
discovery reasons (`unauthorized-destructive`, `self-hosting`,
`management-only`), a read over REST answering in `data`, the 404 on
a destructive route, the exit-2 refusal of `--addr 0.0.0.0:0`, and a
socket path reaching the socket service through `-c`. Run it after
every edit to a `.tmpl` here, then `make builtins-sync` so the
embedded mirror under `internal/template/builtins/` matches.

Go template actions and Go composite literals share `{{`: write
`[]T{x}` with a named value rather than `[]T{{...}}` inside a `.tmpl`.
