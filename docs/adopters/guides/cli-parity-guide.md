# CLI Parity Guide

kit enforces identical CLI behaviour across Go, TypeScript,
and Python. Every tool built with kit/cli (or its TS/Py
equivalents) must satisfy the same contract.

## Global Flags

| Flag | Purpose |
|------|---------|
| `-v, --version` | Print `<name> <version>` and exit |
| `-h, --help` | Show help for current command |
| `--format <fmt>` | Output format: table, json, yaml |
| `--quiet` | Suppress non-essential output |
| `--no-color` | Disable ANSI colour |
| `--help-all` | Show help including hidden groups |
| `--offline` | Disable all network. Highest-precedence override; flips off any per-command opt-in (`--push`, `--sync`, peer discovery, upgrade check). |
| `--profile <name>` | Active aps profile. Selects identity (credentials, default org, git author). Defaults to `$APS_PROFILE`. |
| `--instance <name>` | Backend instance. Names a bundle of service endpoints in `$XDG_CONFIG_HOME/<tool>/instances.yaml`. |

### `--instance` semantics

A pod is *where a service runs*. An instance is *which service URLs my
client points at*. The flag is purely a resolver — it never provisions,
boots, or mutates anything; it just looks up a YAML record and hands
the resolved fields to subcommands that opt in.

**Single-valued, not repeatable.** `--instance` is a `string` flag.
Passing it twice (`--instance a --instance b`) keeps the last value;
there is no list semantic. If you need to fan out across multiple
backends, run the binary multiple times — the resolver is cheap.

**Per-command consumption.** Not every subcommand reads `--instance`.
A subcommand that touches a backing service (e.g. `kit serve`,
`kit engine ...`) honors the resolved bundle as the *default*; an
explicit per-command flag (`--endpoint`, `--addr`) always wins.
Local-only subcommands (e.g. `kit toolspec spec`) ignore it.

**Schema is per-tool.** kit's `Instance` struct has different fields
than aps's. Same flag name, same resolver shape (via `core.Resolve`),
schemas owned by each tool. The YAML files at
`~/.config/kit/instances.yaml` and `~/.config/aps/instances.yaml` are
independent.

## Help Subcommand

No advertised `help` subcommand; users discover help via the
`-h`/`--help` flag only. The default `help` command emitted by
Cobra (Go) and Typer (Python) is suppressed and hidden in all
three languages.

A hidden `help <group>` form is recognized as a muscle-memory
fallback — `mytool help management` is rewritten internally to
the equivalent `--help-management` flag. This form is **not**
listed in `--help` or `--help-all` output by design; see
[`help-rendering.md`](../reference/help-rendering.md) §"`help <id>`
subcommand" for the full rationale.

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
