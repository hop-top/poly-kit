# Python CLI API Reference

> Reference for `hop_top_kit.cli`. Mirrors the
> [Go reference](cli-api-reference.md) — same contract, native
> Typer types.

## Who this is for

Python authors building a tool with kit's CLI factory. If you are
adopting kit for the first time, start with the
[top-level README](../../../README.md#python-tools).

## Before you begin

```bash
pip install hop-top-kit
```

```python
from hop_top_kit.cli import create_app
```

## Recommended path

```python
app, theme = create_app(name="mytool", version="1.0.0", help="does things")

@app.command()
def list():
    ...

if __name__ == "__main__":
    app()
```

`create_app` returns a `(typer.Typer, Theme)` tuple, not a bare app.
Unpack both, or index `[0]` for the app alone.

## Verify the result

```bash
mytool --help          # styled help
mytool --version       # "mytool v1.0.0"
mytool --help-all      # shown once a hidden group is declared
```

The version line always carries a `v` prefix. Pass `version` without
one; kit adds it.

---

## Reference

### create_app

```python
def create_app(
    *,
    name: str,
    version: str,
    help: str,
    accent: str = "",
    no_color: bool = False,
    disable: Disable | None = None,
    globals: list[GlobalFlag] | None = None,
    help_config: HelpConfig | None = None,
) -> tuple[typer.Typer, Theme]
```

All parameters are keyword-only. Returns the Typer app and the
`Theme` derived from `accent` (the NEON palette default when empty).

The app is pre-configured to the hop-top CLI contract:

- `add_completion=False` — no `--install-completion`.
- `no_args_is_help=True` — bare invocation shows help.
- `-v, --version` prints `<name> v<version>` and exits.
- Root callback with `invoke_without_command=True`.

Supporting dataclasses:

```python
@dataclass
class Disable:          # zero value = all flags enabled
    format: bool = False
    quiet: bool = False
    no_color: bool = False
    hints: bool = False

@dataclass
class GlobalFlag:       # extra persistent root flag
    name: str
    usage: str
    short: str = ""
    default: str = ""
```

`no_color=True` is a deprecated shorthand for
`Disable(no_color=True)`; the `NO_COLOR` env var does the same.

### Command groups

#### GroupConfig

```python
@dataclass
class GroupConfig:
    id: str              # unique identifier (e.g. "management")
    title: str           # display title (e.g. "MANAGEMENT")
    hidden: bool = False # True = excluded from default --help
```

#### HelpConfig

Groups are declared on `HelpConfig`, passed to `create_app` as
`help_config`.

```python
@dataclass
class HelpConfig:
    disclaimer: str = ""
    section_order: list[str] = field(default_factory=list)
    show_aliases: bool = False
    groups: list[GroupConfig] = field(default_factory=list)
```

There are no default groups. Declare every group you intend to use;
commands with no assignment stay in the ungrouped `commands`
section.

#### set_command_group

```python
def set_command_group(name: str, group_id: str) -> None
```

Assigns a registered command to a named group. Commands without
assignment default to the `commands` group.

```python
from hop_top_kit.cli import GroupConfig, HelpConfig, create_app, set_command_group

app, theme = create_app(
    name="mytool", version="1.0.0", help="...",
    help_config=HelpConfig(
        groups=[GroupConfig(id="management", title="MANAGEMENT", hidden=True)],
    ),
)

@app.command()
def config():
    """Manage configuration."""
    ...

set_command_group("config", "management")
```

#### `--help-all`

Root-level eager option. When passed, the help formatter includes
commands from all groups, including hidden ones. It is registered
only when groups are configured; with none declared the flag does
not appear.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Output

Package: `hop_top_kit.output`

```python
def render(w: IO[str], format: str, v: Any) -> None
```

`render` looks the format up in the default registry and raises on
an unknown one. Built-in formats are `table`, `json`, `yaml`, `csv`
and `text`, the same `Format` literal the TypeScript port exposes.

```python
import sys
from hop_top_kit.output import render

render(sys.stdout, "json", {"id": 1, "name": "row"})
```

Also exported: `dispatch`, `Registry` / `new_registry` for custom
formats, `CLIError` with the `CODE_*` constants, and the
`*_error` constructors (`usage_error`, `not_found_error`,
`conflict_error`, and the rest) for structured error envelopes.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go equivalent
- [`ts-api-reference.md`](ts-api-reference.md) — TypeScript equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
