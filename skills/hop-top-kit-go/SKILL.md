---
name: hop-top-kit-go
description: Build with kit's Go reference implementation (module hop.top/kit), the kit binary and kit init scaffolding. Use when writing or changing Go code that imports hop.top/kit, when scaffolding a new CLI with kit init, when running the kit binary as a sidecar for a non-Go tool, or when a task needs a capability only Go has. Not for TypeScript, Python, Rust or PHP code; pick the port with the hop-top-kit router.
license: MIT
metadata:
  version: "0.5.0-beta.17" # x-release-please-version
  min_version: "0.5.0-beta.16"
---

# hop-top-kit-go

Go is kit's reference port: module `hop.top/kit`, packages under `go/`
(`hop.top/kit/go/console/cli`), plus the only `kit` binary and `kit init` scaffolding. Every other
port follows it.

## Use another port when

- The project is TypeScript, Python, Rust or PHP and the task needs no Go-only capability and no
  scaffolding: stay in that language; its skill is in `references/other-ports.md`, with the router.
- A project in another language needs one Go-only capability (list: "Go only" in
  `references/other-ports.md`; typical: secrets, audit log, job queue, peer mesh, service command
  group): keep its port and run the `kit` binary as a sidecar over HTTP, see
  `references/kit-binary-and-scaffolding.md`.

<!-- skills-sync:begin coverage -->
Tier: reference.

- CLI and console. Lacks: nothing.
- Config, storage and secrets. Lacks: nothing.
- Serving and transports. Lacks: nothing.
- Events, jobs and runtime. Lacks: nothing.
- Guardrails and trust. Lacks: nothing.
- AI and agents. Lacks: nothing.
- Conformance and integrations. Lacks: nothing.
<!-- skills-sync:end coverage -->

## Install

- Library: `go get hop.top/kit`, import `hop.top/kit/go/<area>/<pkg>`. The module needs the Go
  version its `go.mod` states.
- Binary, any one of: `go install hop.top/kit/cmd/kit@latest`, `brew install hop-top/tap/kit`, or
  the release tarball
  ([cmd/kit README](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md)).

## Default path

New tool: `kit init mytool` (tiers, flags, augment: the binary reference). Existing Go module: copy
`assets/snippets/quickstart/`: one `cli.New` root, one command, `output.Dispatch` for data,
side-effect annotations. Add a command by copying it, never by hand-registering flags kit already
owns (`--format`, `--quiet`, `--no-color`, `--no-hints`, `-c/--config`). Serve the same tree to
agents: `assets/snippets/mcp/` adds one option, `mcpserve.With`, and
`<tool> service start mcp --stdio` serves it. Test a command in-process like both `main_test.go`
files: `root.Cmd.SetOut`, `root.SetArgs`, `root.Execute(ctx)`, then compare output. Run
`go test -race ./...`.

## Before promising a capability

Find it by name in `references/coverage.md` (generated; the full catalogue with package paths is the
[capability index](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/capability-index.md)).
A name in neither is not a kit capability: say so, do not invent a package. Package READMEs under
`go/` are the API truth; this skill points, it does not restate.

## Traps

- `Execute` validates the tree and exits 2 on failure: every runnable leaf needs
  `cli.SetSideEffect`, the root needs `cli.WithStatus`, a depth-1 leaf needs `cli.SetTopLevelVerb`.
  A bare cobra tree fails here.
- Never register your own `-c/--config`; pass `root.ConfigArgs()` to `config.Load`. Project layers
  can forbid keys: `Options.Project`.
- `config.Set` writes strings; `config.SetValue` keeps the Go type.
- Served commands run on REST, socket, MCP and RPC behind one policy: destructive leaves are
  withheld until a surface is allowed. MCP over stdio owns stdout; never print to it yourself.
- SQL drivers (`go/core/upgrade/driver/postgres`) take the caller's `*sql.DB`; kit imports no
  database driver. Postgres needs a pool of two or more and no transaction-mode pooler.
- Nested modules (`examples/`, `engine/`, `contracts/`) have their own `go.mod`: `go test ./...` at
  the root does not enter them.

<!-- skills-sync:begin version -->
## Versions

Written for 0.5.0-beta.17 # x-release-please-version
Expects 0.5.0-beta.16 or newer.

Installed: `go list -m hop.top/kit`; `scripts/detect-port.sh` shows what the manifest pins. Older
than expected: tell the user, upgrade (`go get hop.top/kit@latest`). Newer than written for: this
copy may be stale; update it, then re-read it. The `kit` binary: `kit --version` prints
`kit v<version>` (a source build prints `kit vdev`: nothing to compare); older than expected:
upgrade it (`brew upgrade hop-top/tap/kit`; [other
routes](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md)); newer than written for:
update this skill. Update by install channel, then restart the harness: Claude Code plugin
`claude plugin marketplace update hop-top`, then `claude plugin update hop-top-kit-go@hop-top`
(marketplace repository `hop-top/skillset`); `npx skills` from a GitHub source
`npx skills update -g -y` (project scope `npx skills update -p -y`), from any other source run the
same `npx skills add` again; gym `gym update`, then `gym train`; by hand: take
`skills/hop-top-kit-go` again from `hop-top/kit` at `main`.
<!-- skills-sync:end version -->

## Open when

| File | Open when |
|------|-----------|
| `references/coverage.md` | a capability must be confirmed by name before promising it |
| `references/other-ports.md` | the task or project is not Go, or needs the Go-only list or the router |
| `references/kit-binary-and-scaffolding.md` | `kit init`, tiers, templates, `--update`, or running the kit binary as a sidecar |
| `references/cli-and-console.md` | building commands, output formats, completion, aliases, TUI or wizard prompts |
| `references/config-storage-secrets.md` | layered config with a project marker, XDG paths, key-value or blob stores, secrets |
| `references/serving-and-transports.md` | serving the tree over REST, socket, MCP or RPC, auth, middleware |
| `references/guardrails-and-trust.md` | destructive-command guards, scope, breaker, redaction, audit log, upgrade and state migrations |
| `references/ai-and-agents.md` | calling models, prompts, judges, toolspec, reflecting commands for agents |
| `assets/snippets/quickstart/main.go`, `main_test.go`, `quickstart.expected` | writing the first command and its in-process test |
| `assets/snippets/mcp/main.go`, `main_test.go`, `mcp.expected` | serving a tree over MCP and testing the wire |
| `scripts/detect-port.sh` | the project's kit version or language is unknown |

Events, jobs and runtime, and conformance and integrations: no reference here; find the package in
`references/coverage.md`, then read its README.
