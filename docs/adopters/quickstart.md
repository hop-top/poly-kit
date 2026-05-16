# kit quickstart

In about 10 minutes you'll have a kit-based CLI that scaffolds cleanly,
runs locally, and emits structured output that downstream tools (and
LLM harnesses) can consume.

> Status: **skeleton**. Each step below points at the authoritative
> example or doc; the full prose tutorial is TODO. File an issue or PR
> if a step is unclear.

## Prerequisites

- Go 1.26 or newer (compatibility floor — see `go.mod`).
- `git`, `gh` CLI on `PATH`.
- Optional: Node 22+ and Python 3.13+ if you plan to mirror the CLI in
  TS or Python.

## Step 1: scaffold a new kit binary

TODO: walk through `kit init mytool` end-to-end.

For now, see [`cmd/kit/README.md`](../../cmd/kit/README.md) for the
full `kit init` flow, and [`guides/kit-init.md`](guides/kit-init.md) for the
flag reference.

```bash
go install hop.top/kit/cmd/kit@latest
kit init mytool
cd mytool && make build && ./bin/mytool --help
```

## Step 2: add a command

TODO: register a subcommand on the kit root, with proper output hints
and `--format` support.

For now, see:

- [`reference/cli-api-reference.md`](reference/cli-api-reference.md) — Go CLI factory
  types and methods (`cli.New`, `cli.Config`, command registration).
- [`examples/cmdsurface/`](../../examples/cmdsurface/) — runnable
  example wiring multiple commands.

## Step 3: expose it on REST

TODO: mount the same command logic over `transport/api` so HTTP, WS,
and ConnectRPC clients see the same surface.

For now, see:

- [`examples/multiprotocol/`](../../examples/multiprotocol/) — runnable
  multi-protocol server example.
- `go/transport/api/README.md` for the resource/router API.

## Step 4: verify the CLI contract

```bash
mytool --help          # styled help, no help/completion subcommands
mytool --version       # "mytool 1.0.0"
mytool list --format json   # structured JSON to stdout
```

For a deeper check, run `compliance --static toolspec.yaml`. See
[`reference/compliance-api.md`](reference/compliance-api.md).

## Where to go next

- [Concepts](concepts/) — the mental models behind kit
- [Guides](guides/) — task-oriented how-tos
- [Reference](reference/) — exact API surface
- [Integrations](integrations/) — connect kit to specific tools
- [`reference/bus-api.md`](reference/bus-api.md) — publish/subscribe events
- [`concepts/engine-overview.md`](concepts/engine-overview.md) — run the sidecar
  engine
