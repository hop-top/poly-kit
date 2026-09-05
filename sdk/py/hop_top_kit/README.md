# hop_top_kit

Python implementation of the kit library.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`id/`](id/README.md) | TypeID primitive, cross-language wire form | you mint or parse entity ids |
| [`mcp/`](mcp/README.md) | dual-spec MCP surface, ASGI callable | you serve a command tree to MCP clients |
| [`output/`](output/README.md) | formatter registry, output flag suite, error envelope | a command renders structured data |
| [`output/formatters/`](output/formatters/README.md) | built-in json, yaml, table, csv, text | you need one formatter or add one |
| [`telemetry/`](telemetry/README.md) | mode gate, install id, consent, redactor, `Client` | you emit usage telemetry |
| [`tui/`](tui/README.md) | Rich-backed status, badge, spinner, progress, prompts | a TTY needs styled widgets |
| top-level modules (`cli.py`, `serve.py`, `scope.py`, ...) | single-file ports, one per Go package | see [`../README.md`](../README.md), section "Modules" |
