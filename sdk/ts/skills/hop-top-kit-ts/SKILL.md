---
name: hop-top-kit-ts
description: >-
  Build CLIs and services in TypeScript or JavaScript with the kit SDK
  `@hop-top/kit`: createCLI, output formats, layered config, MCP serving,
  safety guards, LLM adapters. Use when code imports `@hop-top/kit`, when
  adding kit to a Node project, or when a task needs a kit capability in
  TypeScript and you must check this port covers it. Not for choosing a
  language: the router skill does that.
license: MIT
metadata:
  version: "0.5.0-alpha.12" # x-release-please-version
---

# hop-top-kit-ts

kit's TypeScript port: package `@hop-top/kit`, CommonJS, Node 22.12 or newer
(`engines` in its package.json). Commander-based. Public API is subpath
imports only (see Imports).

## Use another port when

- The task needs the `kit` binary, or a capability listed below as lacking:
  switch to Go or run the binary as a sidecar. Choose with the
  [router](https://github.com/hop-top/kit/blob/main/skills/hop-top-kit/SKILL.md)
  and [other ports](references/other-ports.md).
- The project or the user is in another language (Python only, say): stay in
  that language when its port covers the task. Counts across ports are in
  [other ports](references/other-ports.md); do not pick TypeScript for
  coverage alone.

<!-- skills-sync:begin coverage -->
Tier: supported.

- CLI and console. Lacks: Markdown rendering, Config path commands, Fuzzy matching, Process status.
- Config, storage and secrets. Has only: Layered config, XDG paths, SQLite JSON store.
- Serving and transports. Lacks: Service command group, Transport service seam, Unix socket, Served authentication, Served observability, Bridge payloads.
- Events, jobs and runtime. Has only: Event bus, Telemetry.
- Guardrails and trust. Lacks: Breaker, Event policy engine, Operating mode, Audit log, Identity, Utilities, Avatars.
- AI and agents. Lacks: Command reflection, Agent CLI invocation, Extensions.
- Conformance and integrations. Has only: nothing.
<!-- skills-sync:end coverage -->

Per-capability status and evidence: [coverage](references/coverage.md). Find
the capability there before promising it. Matrix cell `partial`: the Evidence
names the gap; stay only if the task avoids it. `unverified`: the module
exists and nothing pins it to Go; tell the user and test the behaviour
(check Telemetry here before relying on it). `none`: use Go or the sidecar.

`kit init` scaffolds a TypeScript project from the Go binary
(`kit init <name> --runtime ts`, see
[kit init](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/kit-init.md));
this port cannot scaffold, and the binary needs no Go toolchain when taken
from a release or Homebrew (see its
[README](https://github.com/hop-top/poly-kit/blob/next/cmd/kit/README.md)).

## Install and first program

`pnpm add @hop-top/kit`. `assets/snippets/quickstart/` is the smallest
program: `createCLI` from `@hop-top/kit/cli`, one command, rows through
`dispatch` from `@hop-top/kit/output`, so `--format`, `--cols`, `-o` and
`--template` work with no more code. Its expected output is checked in.

- `createCLI` returns `{ program, theme }`, not the Commander command.
- Async actions: end with `await program.parseAsync()`, not `parse()`.
- `ColumnSpec` needs `header` equal to `key`; `--cols` selects and reorders.
- Layered config: `load` from `@hop-top/kit/config`; see the CLI reference.
- Tests: in the kit repo, `make test-ts-sdk` runs the snippets. In your
  project, use your own test runner.
- Version: `scripts/detect-port.sh` prints the pinned kit version. The
  coverage block and `references/` are a snapshot at this skill's
  `metadata.version`; the current table is linked from other ports.

## Imports

- Only keys of the `exports` map in kit's package.json (inside `node_modules`)
  resolve, as `@hop-top/kit/<sub>`; a file under `src/` with no key does not.
  The package README names subpaths the map lacks: trust package.json.
- The root `@hop-top/kit` is a barrel of namespaces
  (`import { output } from '@hop-top/kit'`; `completion-values` becomes
  `completionValues`). Prefer subpaths: they load one module.
- Off the barrel on purpose, import by full subpath: `sqlstore` and the
  upgrade drivers `configfile`, `fsdir`, `sqlite`, `postgres`. `sqlstore` and
  `sqlite` load native `better-sqlite3`, `fsdir` loads `tar`; `postgres`
  keeps the root and `upgrade` free of any database client.
- Upgrade migrations: `Migrator` from `@hop-top/kit/upgrade`, a driver per
  backend. Postgres: `@hop-top/kit/upgrade/drivers/postgres`, you pass the
  `pg` Pool; kit depends on no Postgres client.
- Install with scripts disabled (`--ignore-scripts`) leaves `better-sqlite3`
  unbuilt: `sqlstore` and the sqlite driver fail at load.
- Subpath names by area: cli, output, completion, completion-values, alias,
  tui, errcorrect, log, progress, stream; config, xdg, sqlstore; serve, mcp,
  api, rpc, netpolicy, auth; safety, scope, provenance, telemetry, id,
  upgrade; llm, anthropic, openai, routellm, toolspec, aim, compliance.

## Gotchas

- Delegation gates (`--confirm`, `--max-ops`, `--policy`, `--dry-run`) are
  reserved root flags; a leaf named `delete`, `remove`, `rm`, `destroy`,
  `purge` or `drop` is gated as destructive and blocked on MCP unless
  annotated.
- MCP exposure is default-closed, and `commanderBridge` needs your `run`.
- Telemetry records nothing without granted consent and a mode other than off.
- `--offline` guards `globalThis.fetch`; own clients wrap `guardFetch`.
- Importing `anthropic`, `openai` or `routellm` registers their URI schemes.

## Open when

| File | Open when |
|------|-----------|
| `references/coverage.md` | a capability's status or Evidence is needed (is it pinned to Go?) before promising it |
| `references/other-ports.md` | switching port, or the router and ports table are needed |
| `references/cli-and-console.md` | wiring commands, output flags, completion, aliases, logging, layered config or XDG paths |
| `references/serving-and-transports.md` | serving a service lifecycle, MCP, REST or ConnectRPC |
| `references/guardrails-and-trust.md` | adding safety, scope, netpolicy, telemetry, TypeID or upgrade migrations |
| `references/ai-and-agents.md` | calling an LLM, routing models, emitting a toolspec or running compliance |
| `assets/snippets/quickstart/` | starting a CLI that renders rows in several formats |
| `assets/snippets/mcp/` | exposing a command tree as MCP tools |
| `scripts/detect-port.sh` | the kit version a project pins is unknown |
