# src

TypeScript implementation source files.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`id/`](id/README.md) | TypeID primitive, cross-language wire form | you mint or parse entity ids |
| [`mcp/`](mcp/README.md) | dual-spec MCP surface, framework-free handler | you serve a Commander tree to MCP clients |
| [`output/`](output/README.md) | formatter registry, output flag suite, error envelope | a command renders structured data |
| [`output/formatters/`](output/formatters/README.md) | built-in json, yaml, table, csv, text | you need one formatter or add one |
| [`router/`](router/README.md) | RouteLLM scorers, `Controller`, Hono handler (not exported) | you route prompts between strong and weak models |
| [`telemetry/`](telemetry/README.md) | mode gate, install id, consent, redactor, `Client` | you emit usage telemetry |
| [`triton/`](triton/README.md) | KServe v2 HTTP client (not exported) | you call a Triton-hosted model |
| [`tui/`](tui/README.md) | status, badge, spinner, progress, prompts | a TTY needs styled widgets |
| `gen/` | generated Connect stubs, do not edit | never; regenerate instead |
| top-level modules (`cli.ts`, `serve.ts`, `scope.ts`, ...) | single-file ports, one per package export | see [`../README.md`](../README.md), section "Other modules" |
