# console

Human-facing CLI and TUI behavior: how a kit-built tool parses flags, renders
output, reports progress, runs services and talks to the terminal.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`alias/`](alias/README.md) | command alias store backed by a YAML file | users want `tool x` to expand to a longer invocation |
| [`cli/`](cli/README.md) | cobra + fang + viper root command factory, policy gate, conformance | you are building a new CLI root or adding a leaf |
| [`hay/`](hay/README.md) | fuzzy resolution of user input against a corpus, staged lookup | a command takes a name that may be abbreviated or ambiguous |
| [`log/`](log/README.md) | charm log wrapper configured from viper (`quiet`, `no-color`) | a command needs leveled logging on stderr |
| [`markdown/`](markdown/README.md) | markdown to styled terminal text via glamour | a command prints help or docs written in markdown |
| [`output/`](output/README.md) | `--format` dispatch, structured error envelope, formatters | a command emits a result or an error a machine may read |
| [`progress/`](progress/README.md) | per-phase progress events, human lines or JSONL | a command runs long enough to need phase reporting |
| [`ps/`](ps/README.md) | cross-tool process status convention | a tool manages asynchronous or long-running work |
| [`serve/`](serve/README.md) | service contract, registry, resolution and exit codes for `serve` | a tool runs one or more long-lived services |
| [`stage/`](stage/README.md) | shared `stage show\|set\|why\|list` subcommand tree | a tool exposes its operating stage and the rules behind it |
| [`tui/`](tui/README.md) | bubbletea components, AppShell, dialog, styles | a command needs an interactive screen |
| [`uri/`](uri/README.md) | `uri` command tree over `hop.top/cite`: parse, route, complete, OS handler metadata | a tool handles a custom URI scheme |
| [`wizard/`](wizard/README.md) | multi-step setup flows with a headless mode | a command asks the user a sequence of questions |

## Conventions

- `cli` may import `transport/*` and `serve`; `transport/*` and
  `ai/cmdreflect` must not import `cli`. Cobra annotations are read through
  `cli/cmdmeta`, a leaf with zero kit imports.
- `serve` imports no transport package; it holds the contract only.
- Layering rules and the cycle probe:
  [architecture.md](../../docs/contributors/architecture/architecture.md#import-layering).
