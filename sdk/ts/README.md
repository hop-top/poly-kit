# @hop-top/kit (TypeScript)

Shared CLI utilities for hop-top tools, CommonJS edition. Mirrors the Go
`kit` package surface: a Commander-based CLI factory, output formatting
with a registry/Formatter contract, theming, hint plumbing, XDG paths,
config files, an embedded SQLite store, upgrade checks, LLM helpers,
alias resolution, and a TUI toolkit.

## Install

```sh
pnpm add @hop-top/kit
```

Subpath imports follow the package's `exports` map (see `package.json`):
`@hop-top/kit/cli`, `@hop-top/kit/output`, `@hop-top/kit/xdg`,
`@hop-top/kit/uri`, and so on.

## Quick start

```ts
import { createCLI } from '@hop-top/kit/cli';
import { registerOutputFlags, dispatch } from '@hop-top/kit/output';

const { program } = createCLI({
  name: 'mytool', version: '1.0.0', description: 'does things',
});
registerOutputFlags(program);

program.command('list').action(async () => {
  await dispatch(program, [{ id: '1', name: 'Alice' }]);
});

program.parse();
```

`createCLI` gives the root the hop-top contract: `--format`, `--quiet`,
`--no-color`, `--no-hints`, `--offline`, themed help, version, and a
hidden completion command.

## Modules

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`src/output/`](src/output/README.md) | formatter registry and the Commander flag suite | a command renders one payload as table, json, yaml, csv or text |
| [`src/mcp/`](src/mcp/README.md) | dual-spec MCP surface over a bridged command tree | MCP clients must call your commands as tools |
| [`src/telemetry/`](src/telemetry/README.md) | consent-gated usage events, redaction, sinks | you record usage under user consent |
| [`src/id/`](src/id/README.md) | TypeID primitive | you mint or parse prefixed identifiers |
| [`src/tui/`](src/tui/README.md) | TUI toolkit (parity, anim, prompts) | you build an interactive terminal surface |
| [`src/router/`](src/router/README.md) | RouteLLM strong/weak model routing and its Hono handler | a prompt must pick a model in-process |
| [`src/triton/`](src/triton/README.md) | Triton Inference Server client, KServe v2 over HTTP | you call a model hosted on Triton |

Non-directory subpaths: `cli` (CLI factory), `serve` (serve hierarchy
and service lifecycle), `netpolicy` (`--offline` enforcement, guards
`globalThis.fetch`), `xdg` (base directory paths), `config`
(config-file loading), `sqlstore` (embedded SQLite key/value store),
`upgrade` (semver upgrade detection), `llm` / `routellm`, `alias`,
`uri` (facade over `@hop-top/cite`), `api`, `rpc`, `aim`, `scope`,
`safety`, `provenance`, `stream`, `progress`, `auth`, `errcorrect`.
See `package.json` `exports` for the full list.

## Contract

- Five built-in formatters (`json`, `yaml`, `table`, `csv`, `text`), the same set every kit SDK ships.
- Column order comes from an explicit `ColumnSpec[]`; `--cols` reorders as well as selects; `header` must equal `key`.
- `serve <service>` starts a named service even when `services.<name>.enabled` is false; a supervisor run resolving to zero services exits 2.
- MCP exposure is default-closed: `defaultPolicy()` blocks every destructive leaf on every remote surface.
- Telemetry is default-denied: `Client.record()` is a no-op without both a granted consent decision and a non-`off` mode.

## See also

- [TypeScript API reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/ts-api-reference.md):
  every module in depth, output formatting rules and worked examples,
  serve lifecycle, the MCP surface, the URI facade, telemetry envelope
  and redactor
- [Serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-from-any-sdk.md), [serve lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md), [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md)

MIT.

<!-- release: track @hop-top/cite ^0.1.0 -->
