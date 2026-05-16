"""commands/toolspec_cmd.py — load and validate spaced.toolspec.yaml."""
from __future__ import annotations

import os
import sys

import typer

sys.path.insert(
    0,
    os.path.join(os.path.dirname(__file__), "../../../../sdk/py"),
)
from hop_top_kit.toolspec import (  # noqa: E402
    load_toolspec,
    validate_toolspec,
    Command as TSCmd,
)

app = typer.Typer(help="Toolspec validation.", add_completion=False)


def _count_commands(cmds: list[TSCmd]) -> int:
    n = 0
    for c in cmds:
        n += 1
        if c.children:
            n += _count_commands(c.children)
    return n


@app.command("toolspec")
def toolspec() -> None:
    """Load and validate spaced.toolspec.yaml."""
    yaml_path = os.path.join(
        os.path.dirname(__file__), "../../spaced.toolspec.yaml",
    )

    spec = load_toolspec(yaml_path)
    errors = validate_toolspec(spec)

    print()
    print(f"  Name     : {spec.name}")
    print(f"  Version  : {spec.schema_version}")
    print(f"  Commands : {_count_commands(spec.commands)}")

    if errors:
        print(f"  Errors   : {len(errors)}")
        for e in errors:
            print(f"    - {e}")
    else:
        print("  Status   : valid")
    print()
