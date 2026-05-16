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
app = create_app(name="mytool", version="1.0.0", help="does things")

@app.command()
def list():
    ...

if __name__ == "__main__":
    app()
```

## Verify the result

```bash
mytool --help          # styled help
mytool --version       # "mytool 1.0.0"
mytool --help-all      # also shows hidden management groups
```

---

## Reference

### create_app

```python
def create_app(
    *,
    name: str,
    version: str,
    help: str,
) -> typer.Typer
```

Returns a Typer app pre-configured to the hop-top CLI contract:

- `add_completion=False` — no `--install-completion`.
- `no_args_is_help=True` — bare invocation shows help.
- `-v, --version` prints `<name> <version>` and exits.
- Root callback with `invoke_without_command=True`.

### Command groups

#### GroupConfig

```python
@dataclass
class GroupConfig:
    id: str        # unique identifier (e.g. "management")
    title: str     # display title (e.g. "MANAGEMENT COMMANDS")
    hidden: bool   # True = excluded from default --help
```

#### HelpConfig.groups

```python
@dataclass
class HelpConfig:
    groups: list[GroupConfig] = field(default_factory=list)
```

Default groups when none specified:

| id | title | hidden |
|----|-------|--------|
| `commands` | COMMANDS | False |
| `management` | MANAGEMENT COMMANDS | True |

#### set_command_group

```python
def set_command_group(name: str, group_id: str) -> None
```

Assigns a registered command to a named group. Commands without
assignment default to the `commands` group.

```python
app = create_app(name="mytool", version="1.0.0", help="...")

@app.command()
def config():
    """Manage configuration."""
    ...

set_command_group("config", "management")
```

#### `--help-all`

Root-level eager option. When passed, the help formatter includes
commands from all groups, including hidden ones.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Output

Package: `hop_top_kit.output`

Provides `render_table`, `render_json`, `render_yaml` for consistent
output formatting.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go equivalent
- [`ts-api-reference.md`](ts-api-reference.md) — TypeScript equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
