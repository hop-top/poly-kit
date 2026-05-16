"""URI command wiring for spaced."""
from __future__ import annotations

import json

import typer

from hop_top_kit import uri

app = typer.Typer(help="Inspect spaced custom URI scheme metadata", add_completion=False)

_POLICY = {
    "default_namespace_segments": 2,
    "scheme_namespace_segments": {"spaced": 2},
    "vanity_aliases": [
        {"from": "spaced://ift-5", "to": "spaced://hop-top/spaced/IFT-5"},
        {"from": "spaced://starship", "to": "spaced://hop-top/spaced/IFT-5"},
        {"from": "spaced://starman", "to": "spaced://hop-top/spaced/Starman"},
    ],
    "action_routes": {
        "mission.inspect": {
            "command": "spaced",
            "args": ["mission", "inspect", "{id}"],
        },
    },
}


def _policy() -> object:
    return uri.Policy.from_mapping(_POLICY)


def _handler_spec() -> object:
    return uri.HandlerSpec(
        vendor="hop-top",
        app="spaced",
        language="py",
        scheme="spaced",
        app_path="spaced",
        display_name="spaced",
    )


@app.command("parse")
def parse_cmd(
    input: str = typer.Argument(..., help="spaced:// URI"),
    as_json: bool = typer.Option(False, "--json", help="Print JSON"),
) -> None:
    """Parse a spaced:// URI."""
    parsed = uri.parse(input, _policy())
    if as_json:
        typer.echo(json.dumps(parsed.__dict__, indent=2))
        return
    typer.echo(
        f"scheme={parsed.scheme} namespace={parsed.namespace} "
        f"id={parsed.id} action={parsed.action}"
    )


@app.command("resolve")
def resolve_cmd(
    input: str = typer.Argument(..., help="spaced:// URI"),
    as_json: bool = typer.Option(False, "--json", help="Print JSON"),
) -> None:
    """Resolve a spaced:// URI action without executing it."""
    policy = _policy()
    parsed = uri.parse(input, policy)
    plan = uri.resolve(parsed, policy)
    if as_json:
        typer.echo(json.dumps(plan.__dict__, indent=2))
        return
    typer.echo(" ".join([plan.command, *plan.args]))


@app.command("complete")
def complete_cmd(
    input: str = typer.Argument(..., help="Partial spaced:// URI"),
) -> None:
    """Print vanity URI completion candidates."""
    registry = uri.new_registry_with_policy(_policy())
    for candidate in uri.complete(registry, input=input):
        typer.echo(f"{candidate.from_}\tcanonical: {candidate.to}")


@app.command("handler-id")
def handler_id_cmd() -> None:
    """Print the spaced URI handler ID."""
    typer.echo(uri.handler_id(_handler_spec()))
