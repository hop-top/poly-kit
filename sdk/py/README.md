# hop-top-kit (Python SDK)

Python implementation of the hop-top kit library: a Typer-based CLI
factory, output formatting with a `Formatter` Protocol and registry, XDG
paths, config files, a dual-spec MCP surface, a serve lifecycle, a URI
facade and consent-gated telemetry.

## Install

```bash
pip install hop-top-kit
```

Optional extras, so adopters who do not use a surface do not carry it:

| Extra | Pulls in | Install when |
|-------|----------|--------------|
| `mcp` | `mcp>=2.0,<3`, `mcp-types>=2.0,<3` | you serve the Model Context Protocol |
| `telemetry-https` | `httpx>=0.27` | you emit telemetry to a remote NDJSON collector |
| `dev` | pytest, ruff, click | you develop against the SDK |

## Quick start

```python
import typer
from hop_top_kit.cli import create_app
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec

app = create_app(name="mytool", version="1.0.0", help="does things")
register_output_flags(app)

@app.command("list")
def list_items(ctx: typer.Context) -> None:
    rows = [{"name": "alpha", "count": 1}, {"name": "beta", "count": 2}]
    cols = [ColumnSpec(header="name", key="name", priority=9),
            ColumnSpec(header="count", key="count", priority=7)]
    dispatch(ctx, rows, columns=cols)
```

```bash
mytool list                 # table
mytool list --format json
mytool list --cols count    # --cols reorders as well as selects
mytool list -o out.csv      # extension infers the formatter
```

## Modules

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`hop_top_kit/output/`](hop_top_kit/output/README.md) | `Formatter` Protocol, registry, built-ins and the Typer flag suite | a command renders one payload as table, json, yaml, csv or text |
| [`hop_top_kit/mcp/`](hop_top_kit/mcp/README.md) | dual-spec MCP surface over a bridged command tree (extra `mcp`) | MCP clients must call your commands as tools |
| [`hop_top_kit/telemetry/`](hop_top_kit/telemetry/README.md) | consent-gated usage events, redaction, sinks | you record usage under user consent |
| [`hop_top_kit/id/`](hop_top_kit/id/README.md) | TypeID primitive, cross-language | you mint or parse prefixed identifiers |
| [`hop_top_kit/tui/`](hop_top_kit/tui/README.md) | TUI toolkit | you build an interactive terminal surface |

Non-directory modules: `hop_top_kit.cli` (CLI factory),
`hop_top_kit.serve` (serve hierarchy and service lifecycle, cross-language,
see [the contract](../../docs/contracts/serve-lifecycle.md)),
`hop_top_kit.uri` (facade over `hop-top-cite`), `hop_top_kit.safety`
(the Factor 10 `--force` TTY check).

## Contract

- Five built-in formatters (`json`, `yaml`, `table`, `csv`, `text`), the same set every kit SDK ships.
- Column order comes from the `columns=` `ColumnSpec` list; `--cols` reorders as well as selects; `header` must equal `key` or `ColumnSpec` raises `ValueError`.
- Zero rows emits nothing, not even a bare header row.
- MCP exposure is default-closed: `default_policy()` leaves `allow_destructive_on` empty, and empty means block-all.
- Telemetry is default-denied: nothing emits without both a granted consent decision and a non-`off` `KIT_TELEMETRY_MODE`.

## See also

- [Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md):
  the MCP mount, the URI facade, output formatting rules and worked
  examples, custom formatters, the telemetry envelope
- [Python CLI API reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-api-reference.md), [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md)

<!-- release: track hop-top-cite >=0.1.0 -->
