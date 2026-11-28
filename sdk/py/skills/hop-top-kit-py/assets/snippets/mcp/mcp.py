"""Expose a command as an MCP tool and list it without opening a socket.

Needs the `mcp` extra: pip install 'hop-top-kit[mcp]'.
"""

import typer

from hop_top_kit.cli import create_app
from hop_top_kit.mcp import Bridge, Command, Request, Result, mount_mcp

app, _theme = create_app(name="mytool", version="0.1", help="does things")

root = Command(
    name="mytool",
    children=[
        Command(
            name="ping",
            short="Ping the server",
            run=lambda flags: Result(stdout="pong\n"),
            annotations={"kit/side-effect": "read"},
        ),
    ],
)
# mount_mcp returns an ASGI callable: hand it to uvicorn or a Starlette
# route. handle() is the same surface as a pure function.
surface = mount_mcp(Bridge(root))


@app.command("tools")
def tools() -> None:
    """Print the tools/list reply."""
    reply = surface.handle(Request(body=b'{"jsonrpc":"2.0","id":1,"method":"tools/list"}'))
    typer.echo(f"{reply.status} {reply.body.decode()}")


if __name__ == "__main__":
    app()
