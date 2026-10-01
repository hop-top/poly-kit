# CLI Parity Guide

kit enforces identical CLI behaviour across Go, TypeScript,
and Python — the three languages with a CLI layer. Every tool
built with kit/cli (or its TS/Py equivalents) must satisfy the
same contract.

The experimental Rust and PHP SDKs do not wire the global-flag
layer yet. Where a section names them, it says what each port
ships today; elsewhere "all three languages" means Go,
TypeScript and Python.

## Global Flags

| Flag | Purpose |
|------|---------|
| `-v, --version` | Print `<name> <version>` and exit |
| `-h, --help` | Show help for current command |
| `--format <fmt>` | Output format: table, json, yaml |
| `--quiet` | Suppress non-essential output |
| `--no-color` | Disable ANSI colour |
| `--help-all` | Show help including hidden groups |
| `--offline` | Disable network access. Highest-precedence override; flips off any per-command opt-in (`--push`, `--sync`, peer discovery, upgrade check). Enforced beneath the language's default HTTP client (Go: `net/http` transport; Python: the `urllib` opener chain; TS: `globalThis.fetch`; PHP: the Guzzle handler stack) for HTTP(S) — Rust has no default client, so there it is enforced by `GuardedClient`, the crate's only `reqwest` construction path; callers that open sockets directly (raw `net.Dial`/`node:net`/`socket`, SQL drivers, gRPC) or inject their own transport are covered only when they route their dialer through the language's guard helper (Go: `netpolicy.GuardDial`); a dependency that dials its own socket and exposes no dialer hook cannot be reached, and must consult the offline marker itself. Loopback is exempt. Logging-class egress — telemetry, and any remote-logging or crash-reporting sink — is also exempt: `--offline` stops traffic the user asked for, it is not a second consent gate on diagnostics. A refused request fails with an error naming only its method and destination; see [Offline refusals](#offline-refusals). |


## Offline refusals

A request refused under `--offline` fails with an error, never a silent
skip. Every port prints the same shape:

```text
GET https://api.example.com:8443/v1/models: network disabled by --offline
```

- The destination is `scheme://host[:port]/path` only. Query, fragment
  and userinfo are dropped, not masked: no `?key=...`, no `user:pass@`,
  no placeholder. An opaque URL (`mailto:`, `data:`) prints its scheme
  alone; a target the port cannot parse prints `<unparseable URL>`.
- Match the refusal by type, not by message text: Go
  `errors.Is(err, netpolicy.ErrOffline)`, TypeScript `isOfflineError(err)`,
  Python `OfflineError` (an `OSError`, not a `URLError`), PHP
  `OfflineException`, Rust `NetError::as_offline()`.
- Keep credentials out of URLs; send them in headers. The refusal hides
  them, but the layers around it do not: Go's `net/http` wraps the
  refusal in a `*url.Error` that quotes the full request URL, a Rust
  `reqwest::Response` keeps the full URL in `url()` and in its body
  errors, and any logger or retry wrapper that prints the URL leaks it
  the same way.

Per-port detail: [Go](../../../go/core/netpolicy/README.md#contract),
[TypeScript](../reference/ts-api-reference.md#modules),
[Python](../reference/py-sdk.md#offline-enforcement),
[PHP](../reference/php-sdk.md#offline-enforcement),
[Rust](../reference/rs-sdk.md#offline-enforcement). Rust and PHP do not
register `--offline` yet, having no global-flag layer: set the marker
from your own flag, as each page's "CLI flag" section shows.

## Reading `-V` and `--quiet`

Commands read the parsed verbosity through an accessor, not by walking
the parser's context. `--quiet` and `-V` combine without error and
`--quiet` wins: the logger level floors at the parity contract's
`verbosity.quiet_override`.

| Port | Verbose count | Quiet |
|------|---------------|-------|
| Go | `root.VerboseCount()` | `root.IsQuiet()` |
| TypeScript | `verboseCount(cmd)` | `isQuiet(cmd)` |
| Python | `verbose_count()` | `is_quiet()` |
| Rust | none | none |
| PHP | none | none |

Go's `VerboseCount` keeps the raw count under `--quiet`, and the logger
applies the override. The TypeScript and Python counts read 0 under
`--quiet`. Rust and PHP have no accessor: neither port wires the global
flag layer yet (Rust's `cli` module is empty; PHP's `Cli` class is a
placeholder).

## Help Subcommand

No advertised `help` subcommand; users discover help via the
`-h`/`--help` flag only. The default `help` command emitted by
Cobra (Go) and Typer (Python) is suppressed and hidden in all
three languages.

A hidden `help <topic>` form is recognized as a muscle-memory
fallback. It is a Go-side affordance, outside the parity contract
itself — the contract's canonical surface is `-h`/`--help` plus
the `--help-<id>` flags, which all three languages implement. In
Go the operand is a command path (`mytool help fleet add` shows
that command's help) or a group ID, which is rewritten internally
to the equivalent `--help-management` flag; a command wins a name
it shares with a group, and an operand naming neither is a usage
error (exit 2). This form is **not** listed in `--help` or
`--help-all` output by design; see
[`help-rendering.md`](../reference/help-rendering.md)
§"`help <topic>` subcommand" for the full rationale.

## Completion

Disabled or hidden entirely. Tools ship completions via a
separate mechanism (not through the framework's default).

## Error Handling

- Errors print to stderr
- Non-zero exit code on error
- No stack traces in user-facing output

## Command Groups

Commands are organized into named groups. Groups control
how commands appear in `--help` output.

### Default Groups

| Group | ID | Visible | Purpose |
|-------|----|---------|---------|
| COMMANDS | `commands` | Yes | Primary user-facing commands |
| MANAGEMENT | `management` | No | Config, toolspec, diagnostics |

### Assigning Commands to Groups

Developers assign each subcommand to a group at
registration time. Unassigned commands default to the
COMMANDS group.

### Hidden Groups and `--help-all`

Groups with `Hidden: true` are excluded from default
`--help` output. The `--help-all` flag overrides this
filter, revealing all groups and their commands.

### Parity Requirement

All three languages must produce the same group layout:

- Same group IDs and titles
- Same commands in each group
- Same hidden/visible behaviour
- `--help-all` available in all languages

This ensures users see identical help output regardless
of which language a tool is built with.

## Module Dependencies and Transitive Imports

kit's Go packages are built on the Charm stack
(`charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`,
`charm.land/bubbles/v2`, `charm.land/fang/v2`,
`charm.land/glamour/v2`, `charm.land/log/v2`). Adopters
should expect Charm packages to appear in their `go.sum`
even when their own code never imports Charm directly.

### What bleeds

Per Go's module graph rules, importing any kit package
that touches Charm pulls every Charm transitive into the
adopter's `go.sum`. The Charm-touching kit packages today
include:

| Package | Charm dependency |
|---------|------------------|
| `hop.top/kit/cli` | `fang/v2`, `lipgloss/v2` |
| `hop.top/kit/log` | `log/v2`, `lipgloss/v2` |
| `hop.top/kit/markdown` | `glamour/v2`, `lipgloss/v2` |
| `hop.top/kit/output` | `lipgloss/v2` |
| `hop.top/kit/ps` | `lipgloss/v2` |
| `hop.top/kit/tui` | `bubbletea/v2`, `bubbles/v2`, `lipgloss/v2` |
| `hop.top/kit/wizard` | `bubbletea/v2`, `lipgloss/v2` |

Because `hop.top/kit/cli` is the framework entry point
for nearly every kit tool, virtually every adopter
inherits the full Charm transitive set regardless of
which other kit packages they import. The `tui` package
adds `bubbles/v2` (and components like `spinner`,
`table`, `textinput`, `list`, `help`) on top.

### Why it happens

Go's module resolution does not prune by package — it
resolves at module granularity. Importing one symbol
from a module loads `go.sum` entries for every
transitive dependency of every package in that module,
even unused ones. This is intentional (it makes builds
reproducible) and not a kit-specific problem.

### What to do about it

- **Use only the components you need**: importing
  `hop.top/kit/cli` already pulls `fang/v2` and
  `lipgloss/v2` transitively. Adding `hop.top/kit/tui`
  on top adds `bubbles/v2` and friends. Skip `tui` if
  you don't need TUI components.
- **Accept the `go.sum` lines**: they are checksum
  records, not runtime dependencies. The compiled
  binary only includes packages actually imported.
  `go.sum` size has no effect on binary size.
- **Vendor if you must**: `go mod vendor` keeps the
  unused transitive sources in `vendor/` but the build
  still only links what is imported.
- **Run `go mod why <pkg>`**: to confirm whether a
  Charm package is actually reachable from your code
  or merely listed as a transitive checksum.

### Public API exposes Charm types

Kit's TUI primitives intentionally expose Charm types in
their public signatures (`spinner.Model`, `lipgloss.Style`,
`tea.Cmd`, `help.KeyMap`, `table.Styles`, etc.). This is
a design choice: kit's TUI is a thin themed layer over
bubbletea, not an abstraction. Adopters need the Charm
types to compose components into their own bubbletea
programs.

If you want a TUI library that hides Charm entirely,
kit/tui is not it. Build your own façade or use a
non-Charm TUI library.
