"""Smallest kit CLI: an app with the output flag suite and one command."""

import typer

from hop_top_kit.cli import create_app
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec

# create_app returns (typer.Typer, Theme), not a bare app.
app, _theme = create_app(name="mytool", version="0.1", help="does things")
register_output_flags(app)


@app.command("list")
def list_items(ctx: typer.Context) -> None:
    rows = [{"name": "alpha", "count": 1}, {"name": "beta", "count": 2}]
    cols = [
        ColumnSpec(header="name", key="name", priority=9),
        ColumnSpec(header="count", key="count", priority=7),
    ]
    dispatch(ctx, rows, columns=cols)


if __name__ == "__main__":
    app()
