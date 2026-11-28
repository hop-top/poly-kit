---
name: hop-top-kit-py
description: Build a CLI or service in Python on kit with the hop-top-kit package (import hop_top_kit), covering Typer app factory, output formats, layered config, serve lifecycle, MCP surface, guardrails, migrations. Use when Python code imports hop_top_kit or the user asks for kit in Python, when choosing between kit modules in this port, or when checking whether this port covers a capability before building on it. Not for choosing a language; use the hop-top-kit router for that.
license: MIT
compatibility: Python 3.11 or newer; pip install hop-top-kit
metadata:
  version: "0.5.0-alpha.10" # x-release-please-version
  min_version: "0.5.0-alpha.10"
---

# hop-top-kit-py

Python port of kit: Typer-based CLI factory, output formatters, XDG paths, config files, serve
lifecycle, a dual-spec MCP surface, guardrails, telemetry, LLM clients, versioned migrations.
Distribution `hop-top-kit`, import package `hop_top_kit`. Go is the reference; this port covers part.

## Use another port when

- The task needs the `kit` binary, `kit init` scaffolding or a capability the coverage below lacks:
  Go (or run the binary as a sidecar), see [other ports](references/other-ports.md).
- The project or team is not Python, or the language is still open: load the
  [router skill](https://github.com/hop-top/kit/blob/main/skills/hop-top-kit/SKILL.md) and pick
  there; do not start a Python service for a Rust or Go team.

<!-- skills-sync:begin coverage -->
Tier: supported.

- CLI and console. Lacks: Markdown rendering, Config path commands, Fuzzy matching, Process status.
- Config, storage and secrets. Has only: Layered config, XDG paths, SQLite JSON store.
- Serving and transports. Has only: Serve lifecycle, Command surface bridge, MCP server, Served middleware.
- Events, jobs and runtime. Has only: Event bus, Telemetry.
- Guardrails and trust. Lacks: Breaker, Event policy engine, Operating mode, Audit log, Identity, Utilities, Avatars.
- AI and agents. Lacks: Command reflection, Agent CLI invocation, Extensions.
- Conformance and integrations. Has only: nothing.
<!-- skills-sync:end coverage -->

## Check before promising

Find the capability by name in [coverage](references/coverage.md) (it links the current matrix). Then:

- `partial`: the line names the gap. Stay if the task does not need it; otherwise Go or the sidecar
  for that part.
- `unverified`: a module exists, nothing pins it to Go. Fine for a Python-only tool; say so to the
  user and test the behaviour. Cross-tool behaviour that must match Go: use Go.
- `none`: no module here. Go, the sidecar, or build it yourself and say so.

## Install

`pip install hop-top-kit`, Python 3.11 or newer. The base install pulls Typer, Rich, PyYAML and
Pydantic. Extras (from the package metadata):

| Extra | Install when |
|-------|--------------|
| `mcp` | you serve MCP: `hop_top_kit.mcp` imports the protocol packages; the base install does not |
| `telemetry-https` | telemetry goes to a remote collector (`httpx`) |
| `dev` | you run the SDK's own tests (includes `mcp`) |

The `upgrade-pg-test` extra in the source tree is the SDK's own test dependency, not a driver.

## Default path

[quickstart](assets/snippets/quickstart/quickstart.py) is a runnable CLI with `list`,
`--format json|yaml|csv|text`, `--cols`, `-o file`. Copy it, rename, add commands. Test with
`typer.testing.CliRunner`. Output, errors and exit codes:
[CLI and console](references/cli-and-console.md).

## Traps

- `create_app(name=, version=, help=)` returns `(typer.Typer, Theme)`, not an app. Unpack it.
- Pass `version` without a `v`; kit prints `<name> v<version>`.
- Call `register_output_flags(app)` before registering commands: it patches `app.command`.
- Import from submodules (`hop_top_kit.output.dispatch`, `hop_top_kit.serve`); the package root
  re-exports only a few names (`create_app`, `config_dir`, `load_config`, `open_store`, `new_id`).
- Names are snake_case; Go `Options` types are dataclasses in their module.
- Async split: the migration `Migrator.run` is synchronous (takes a `threading.Event`);
  `hop_top_kit.serve` runs on asyncio; the MCP surface is an ASGI callable, not WSGI.
- Destructive-looking leaves (`delete`, `remove`, `rm`, `destroy`, `purge`, `drop`) are gated by the
  root: `--confirm` with no terminal means no.
- No Django (or any web framework) integration exists. Kit config is a plain dict loader; wiring it
  into framework settings is yours.
- Postgres migrations need the caller's connections; see
  [guardrails and trust](references/guardrails-and-trust.md).

<!-- skills-sync:begin version -->
## Versions

Written for 0.5.0-alpha.10 # x-release-please-version
Expects 0.5.0-alpha.10 or newer.

Installed: `pip show hop-top-kit` or `uv pip show hop-top-kit`; uv prints the PEP 440 spelling (a,
b, rc for -alpha., -beta., -rc.). `scripts/detect-port.sh` shows what the manifest pins. Older than
expected: tell the user, upgrade (`pip install -U --pre hop-top-kit`). Newer than written for: this
copy may be stale; update it, then re-read it. Update by install channel, then restart the harness:
Claude Code plugin `claude plugin marketplace update hop-top`, then
`claude plugin update hop-top-kit-py@hop-top` (marketplace repository `hop-top/skillset`);
`npx skills` from a GitHub source `npx skills update -g -y` (project scope
`npx skills update -p -y`), from any other source run the same `npx skills add` again; gym
`gym update`, then `gym train`; by hand: take `skills/hop-top-kit-py` again from `hop-top/kit-py` at
`main`.
<!-- skills-sync:end version -->

## Open when

| File | Open when |
|------|-----------|
| `references/coverage.md` | before promising any capability; each status with its Evidence |
| `references/other-ports.md` | the task needs another port, the binary, or a Go-only capability |
| `references/cli-and-console.md` | building commands, output, errors, aliases, completion, TUI |
| `references/config-storage-secrets.md` | layered config, XDG directories, the SQLite store |
| `references/serving-and-transports.md` | `serve`, service lifecycle, exposing commands over MCP |
| `references/events-jobs-runtime.md` | in-process events or usage telemetry |
| `references/guardrails-and-trust.md` | confirmation gate, path scope, offline mode, migrations, TypeID |
| `references/ai-and-agents.md` | LLM providers, tool calls, routing, the 12-factor check |
| `assets/snippets/quickstart/quickstart.py` | starting any new CLI |
| `assets/snippets/mcp/mcp.py` | exposing a command as an MCP tool |
| `scripts/detect-port.sh` | the language of a project is unknown |
