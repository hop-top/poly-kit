# ai

Packages that describe a CLI to machines and let machines act through it:
reflection, tool specifications, permission policy, LLM clients and
extension points.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`cmdreflect/`](cmdreflect/README.md) | single cobra reflector producing one `Descriptor` per command | you need a fact about a command that both the manifest and help rendering must agree on |
| [`ext/`](ext/README.md) | extension contract, manager, hook bus, discovery, dispatch, config | you add plugin points to a kit-powered tool |
| [`llm/`](llm/README.md) | provider-agnostic LLM client, routing, provider drivers | you call a model or route between providers |
| [`toolspec/`](toolspec/README.md) | `ToolSpec` and `Manifest` data types, safety vocabulary, registry | you model a tool's commands, flags, risk and workflows |
| [`toolspec/adapters/`](toolspec/adapters/README.md) | format adapters (kit-manifest, mcp) and MCP policy gate | you render the manifest or enforce policy on an MCP call |
| [`toolspec/cli/`](toolspec/cli/README.md) | `<tool> spec` subcommand and cobra walker | your kit-powered CLI should publish its manifest |
| [`toolspec/policy/`](toolspec/policy/README.md) | side-effect x network permission table | you decide auto-allow, prompt or deny for a command |
| [`toolspec/sources/`](toolspec/sources/README.md) | help, completion, tldr, thefuck, llm, usp spec sources | you describe a tool kit does not own |

## Conventions

- `toolspec` stays pure data; I/O lives in `toolspec/sources/*` and `toolspec/cli`.
- Reflection happens once in `cmdreflect`; `toolspec/cli` and help rendering project from its descriptors.
- Sources are ordered by trust when chained; the tool's own `<tool> spec` manifest is authoritative.
- `ext/dispatch` and `ext/discover` execute binaries; import them only in binaries that mount plugins.
