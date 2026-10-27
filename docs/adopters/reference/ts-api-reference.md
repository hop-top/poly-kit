# TypeScript CLI API Reference

> Reference for `@hop-top/kit/cli`. Mirrors the
> [Go reference](cli-api-reference.md) — same contract, native
> Commander types.

## Who this is for

TypeScript authors building a tool with kit's CLI factory. If you
are adopting kit for the first time, start with the
[top-level README](../../../README.md#typescript).

## Before you begin

```bash
pnpm add @hop-top/kit
```

```ts
import { createCLI } from '@hop-top/kit/cli';
```

## Recommended path

```ts
const { program } = createCLI({
  name: 'mytool',
  version: '1.0.0',
  description: 'does things',
});

program.command('list').action(() => { /* ... */ });

program.parse();
```

`createCLI` returns `{ program, theme }`, not the Commander command
itself. Destructure `program`, or reach it as `result.program`.

## Verify the result

```bash
mytool --help          # styled help
mytool --version       # "mytool v1.0.0"
mytool --help-all      # also shows hidden management groups
```

The version line always carries a `v` prefix. Pass `version` without
one; kit adds it.

---

## Reference

### CLIConfig

```ts
interface CLIConfig {
  name: string;        // binary name (e.g. "mytool")
  version: string;     // semver, no leading "v" (e.g. "1.2.3")
  description: string; // one-line help description
  accent?: string;     // hex theme accent (e.g. "#FF0000"); default Neon

  // Opt out of built-in global flags. Omit to keep all of them.
  disable?: {
    format?: boolean;
    quiet?: boolean;
    noColor?: boolean;
    hints?: boolean;
    offline?: boolean;
  };

  // Command groups for partitioned help.
  groups?: Array<{
    id: string;
    title: string;
    hidden?: boolean;
  }>;

  // Extra tool-specific persistent flags on the root command.
  globals?: Array<{
    name: string;     // long name without "--"
    short?: string;   // single char
    usage: string;
    default?: string;
  }>;

  // Root --help layout overrides; defaults from contracts/parity.
  help?: {
    disclaimer?: string;
    sectionOrder?: string[];
    showAliases?: boolean;
  };
}
```

### createCLI

```ts
function createCLI(cfg: CLIConfig): CLIResult

interface CLIResult {
  program: Command; // configured Commander root
  theme: Theme;     // derived from cfg.accent, or the Neon default
}
```

`program` is a Commander `Command` pre-configured to the hop-top CLI
contract:

- No help/completion subcommands; `-h`/`--help` flag only.
- `-v, --version` prints `<name> v<version>` and exits.
- Global options: the `--format` suite (`--format`, `--format-opt`,
  `--format-help`, `--cols`/`--columns`, `--template`, `-o`/`--output`),
  plus `--quiet`, `--no-color`, `--no-hints` and `--offline`. Each is
  suppressible through `disable`.
- `showHelpAfterError` enabled.

### verboseCount and isQuiet

```ts
function verboseCount(cmd: Command): number
function isQuiet(cmd: Command): boolean
```

Call these from an action handler, passing the command (`this` in a
`function` action). Both walk up to the root, so a subcommand reads the
root's flags. A `preAction` hook on the root stores them.

- `verboseCount` is the stacked `-V` count. 0 before parsing.
- `isQuiet` reports `--quiet`. False before parsing and with
  `disable.quiet`.
- `--quiet` and `-V` combine without error; `--quiet` wins:
  `verboseCount` reads 0, and a logger built with `quiet: isQuiet(cmd)`
  runs at the parity contract's `verbosity.quiet_override` (warn).

```ts
import { isQuiet, verboseCount } from '@hop-top/kit/cli';

program.command('sync').action(function (this: Command) {
  if (verboseCount(this) > 0) process.stderr.write('resolving packages\n');
  if (!isQuiet(this)) process.stderr.write('fetching 3 packages…\n');
});
```

### Command groups

#### Declaring groups

Groups are declared inline on `CLIConfig.groups`; there is no
exported `GroupConfig` type to import.

```ts
groups?: Array<{
  id: string;       // unique identifier (e.g. "management")
  title: string;    // display title (e.g. "MANAGEMENT")
  hidden?: boolean; // omitted or false = shown in default --help
}>
```

There are no default groups. Declare every group you intend to use;
an unrecognised `groupId` leaves the command in the ungrouped
`commands` section.

#### setCommandGroup

```ts
function setCommandGroup(cmd: Command, groupId: string): void
```

Assigns a subcommand to a named group. Commands without assignment
default to `commands`.

```ts
const { program } = createCLI({
  name: 'mytool', version: '1.0.0', description: '...',
  groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
});
const configCmd = program.command('config').description('Manage configuration');
setCommandGroup(configCmd, 'management');
```

#### `--help-all`

Root-level boolean option. When passed, the help formatter includes
commands from hidden groups. It is registered only when at least one
declared group sets `hidden: true`; with no hidden group there is
nothing for it to reveal and the flag does not appear.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

## Package layout

`@hop-top/kit` is the CommonJS edition of the shared CLI utilities. It
mirrors the Go `kit` package surface: a Commander-based CLI factory,
output formatting with a registry/Formatter contract, theming, hint
plumbing, XDG paths, config files, an embedded SQLite store, upgrade
checks, LLM helpers, alias resolution, and a TUI toolkit.

Subpath imports follow the package's `exports` map (see
`package.json`): `@hop-top/kit/cli`, `@hop-top/kit/output`,
`@hop-top/kit/xdg`, `@hop-top/kit/uri`, …

### Modules

- `@hop-top/kit/cli` — `createCLI(cfg)` builds a Commander root program
  with the hop-top contract: `--format`, `--quiet`, `--no-color`,
  `--no-hints`, `--offline`, themed help, version, hidden completion
  command.
- `@hop-top/kit/netpolicy` — enforcement behind the `--offline` global.
  `createCLI` guards `globalThis.fetch`, so a leaf that never consults
  the marker is still refused; loopback stays reachable. Callers that
  inject their own transport wrap it with `guardFetch`, and socket-level
  clients (`node:net`, SQL drivers, gRPC) consult `isOffline()`
  themselves. Match the refusal with `isOfflineError(err)`; its message
  names method, scheme, host and path only, never query, fragment or
  userinfo.
- `@hop-top/kit/id` — TypeID primitive (cross-language; see
  [TypeID primitive](../../announcements/2026-05-typeid-primitive.md)). Source:
  [`sdk/ts/src/id/`](../../../sdk/ts/src/id/).
- `@hop-top/kit/xdg` — XDG Base Directory paths.
- `@hop-top/kit/config` — config-file loading.
- `@hop-top/kit/sqlstore` — embedded SQLite key/value store. Its types
  reference `better-sqlite3`: install `@types/better-sqlite3` (an optional
  peer dependency) or type-checking with `skipLibCheck: false` fails with
  TS7016.
- `@hop-top/kit/upgrade` — semver upgrade detection.
- `@hop-top/kit/llm` — LLM client, registry, fallback and typed errors;
  `@hop-top/kit/anthropic`, `@hop-top/kit/openai` and
  `@hop-top/kit/routellm` register their URI schemes on import (see
  [LLM adapters](#llm-adapters) and [LLM tool calling](#llm-tool-calling)).
- `@hop-top/kit/alias` — alias resolution + completion.
- `@hop-top/kit/uri` — thin facade over `@hop-top/cite` for URI parsing,
  action resolution, completions, registries, and OS handler metadata.
- `@hop-top/kit/tui` — TUI toolkit (parity, anim, prompts).
- `@hop-top/kit/mcp` — dual-spec MCP surface (see [MCP surface](#mcp-surface)).
- `@hop-top/kit/serve` — the serve hierarchy and service lifecycle (see
  [Serve](#serve)).
- `@hop-top/kit/output` — formatter registry and the Commander flag
  suite (see [Output formatting](#output-formatting)).
- `@hop-top/kit/telemetry` — consent-gated usage events (see
  [Telemetry](#telemetry)).

See package.json `exports` for the full list.

## Output formatting

The `output` module ships a Formatter + Registry contract identical to
the Go `go/console/output` package, plus a Commander integration layer
that wires the standard flag suite onto your program.

### Built-in formatters

| Key     | Extensions     | Options                                                                           |
| ------- | -------------- | --------------------------------------------------------------------------------- |
| `csv`   | `.csv`         | `delimiter` (string), `no-header` (bool), `quote-all` (bool), `crlf` (bool)       |
| `json`  | `.json`        | `indent` (int, default 2)                                                         |
| `table` | (none)         | (none)                                                                            |
| `text`  | `.txt`         | `style` (enum kv\|lines\|paragraph), `separator` (string, kv only)                |
| `yaml`  | `.yaml`, `.yml`| `flow-level` (int, default -1)                                                    |

### Wiring into a Commander CLI

```ts
import { Command } from 'commander';
import { registerOutputFlags, dispatch } from '@hop-top/kit/output';

const program = new Command('mytool');
registerOutputFlags(program);

program.command('list').action(async () => {
  await dispatch(program, [{ id: '1', name: 'Alice' }]);
});

program.parseAsync();
```

`registerOutputFlags` adds the full suite — opt-out per flag via
`{ disable: { format?, formatOpt?, formatHelp?, cols?, template?, output? } }`.

### Flag surface

- `--format <fmt>` — pick a registered formatter (default `table`).
- `--format-opt <kv...>` — repeatable key=value pairs validated against
  the active formatter's option specs (e.g. `--format-opt delimiter=';'`).
  Bool keys may omit `=value`.
- `--format-help [fmt]` — without an argument, list every registered
  formatter; with an argument, print that formatter's options table.
  Short-circuits before render.
- `--cols`, `--columns <cols...>` — restrict output to the named
  columns. Accepts comma-separated or repeated flags. Honored by all
  five built-ins.
- `--template <tpl>` — eta template applied to results. Mutually
  exclusive with `--cols`. Template input shape: `{ items, cols, data }`.
  See [eta syntax](https://eta.js.org/docs/intro/syntax) — note the
  EJS-style `<%= %>` tags differ from Go's `{{ }}` text/template syntax.
- `-o`, `--output <path>` — write to a file instead of stdout. Empty
  string or `-` means stdout. Extension inference: when `--format` is
  default and the path's extension matches a registered formatter, that
  formatter is selected (e.g. `-o report.csv` → csv). Mismatch is a
  hard error (e.g. `--format json -o report.csv`).

### Programmatic Formatter API

```ts
import {
  defaultRegistry,
  Registry,
  type Formatter,
} from '@hop-top/kit/output';

// Custom formatter:
const htmlFormatter: Formatter = {
  key: 'html',
  extensions: ['.html'],
  options: [],
  render(out, data, _opts, cols) {
    // `cols` is the resolved column list, already ordered — render it in
    // the order given. Empty means "fall back to payload key order".
    // ... render HTML to `out`
  },
};

// Replace a built-in or add a new one:
defaultRegistry.override(htmlFormatter); // or .register() to fail-loud on dup

// Isolated registry for tests/multi-CLI binaries:
const r = new Registry();
r.register(htmlFormatter);
```

### Column metadata

Go reads column order off struct field declaration order via `table:""`
tags. TS has no struct tags, so pass an explicit `ColumnSpec[]` list to
`dispatch` to pin **which columns appear and in what order** — the list
is what payload-shaped SDKs use in place of Go's field order:

```ts
import type { ColumnSpec } from '@hop-top/kit/output';

const userCols: ColumnSpec[] = [
  { header: 'id',    key: 'id',    priority: 9 },
  { header: 'name',  key: 'name',  priority: 8 },
  { header: 'notes', key: 'notes', priority: 2 },
];

await dispatch(cmd, users, { columns: userCols });
```

When no `columns` is passed, headers are derived from the first row's
own enumerable keys.

### Column ordering

1. **Default order.** With a `ColumnSpec[]` and no `--cols`, column order
   and headers come from the list, in list order. Payload key order is the
   fallback only when no list was supplied.
2. **`--cols` precedence.** `--cols` reorders as well as selects:
   `--cols status,name` renders `status` then `name`, whatever the list
   says. Same rule on the no-schema fallback path.
3. **`header` == `key`.** The two are the same name: it is the label, the
   value matched against `--cols`, and the property read off the row. Go
   cannot express a split through `table:""` tags, so no SDK offers one.
   `key` is retained for source compatibility and must equal `header`.
4. **Empty payload.** Zero rows emits nothing — not even a bare header
   row. Emptiness is decided by row count, never by header count.
5. **`priority`.** Accepted and stored, currently ignored. Hide-on-overflow
   is implemented in Go only; the payload SDKs will port it separately.

`dispatch` applies rules 1 and 2 before calling `render`, so the `cols`
argument a formatter receives is already the final, ordered column list —
empty only when it should fall back to payload key order. Custom
formatters get correct ordering by rendering `cols` in the order given;
they never see `ColumnSpec` and need no changes.

Worked example — the same rows under each rule:

```ts
const rows = [
  { count: 3, status: 'ready', name: 'alpha' },
  { count: 8, status: 'held',  name: 'beta'  },
];
const cols: ColumnSpec[] = [
  { header: 'name',   key: 'name',   priority: 9 },
  { header: 'count',  key: 'count',  priority: 7 },
  { header: 'status', key: 'status', priority: 5 },
];
```

With `columns: cols` and no `--cols`, the list drives order — note the
payload's own key order is `count, status, name` and is not what appears
(rule 1):

```
name   count  status
alpha  3      ready
beta   8      held
```

`--cols status,name` reorders as well as selects (rule 2), in `table`
and `json` alike:

```
status  name
ready   alpha
held    beta
```

```json
[
  {
    "status": "ready",
    "name": "alpha"
  },
  {
    "status": "held",
    "name": "beta"
  }
]
```

With no `columns` at all, payload key order is the fallback:

```
count  status  name
3      ready   alpha
8      held    beta
```

Capability differences against the Go reference:

| Capability | Go (reference) | ts / py | rs / php |
|---|---|---|---|
| Column order source | `table:""` tags, declaration order | `ColumnSpec[]` order | `ColumnSpec` list order |
| `priority` hide-on-overflow | implemented | accepted, stored, ignored | accepted, stored, ignored |
| `header != key` | inexpressible via `table:""` | rejected at construction | rejected at construction |
| json/yaml key order | follows the resolved order | follows the resolved order | follows the resolved order |
| `--cols` reorders | yes | yes | yes |
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | same five | same five |
| Ordered columns on the template path | `.Cols` | `cols` | `{*}` (pre-joined values, tab-separated) |

Go's inability to express `header != key` is the *reason* rule 3 binds
every runtime: no SDK may carry a capability the reference cannot mirror.

### Conformance status

TS satisfies all five rules, as does Go across all five formats. The
fixtures under `sdk/tests/cross-lang/` execute the contract against every
runtime.

All five built-in formats ship in every runtime: rs registers `csv` and
`text` in `src/output/builtins/` and php ships `CsvFormatter` and
`TextFormatter` under `src/Output/Formatter/Builtin/`. The
cross-language fixture set `sdk/tests/cross-lang/expected/ordering.json`
still carries the `rs-php-no-csv-text` skip entry, which predates those
formatters; until the fixtures are refreshed the extended csv/text cases
run on go/py/ts only and are reported as skipped for rs and php.

One difference matters when writing portable templates: on the
`--template` path Go exposes `.Cols` and py and ts expose `cols`, an
iterable of column names; rs and php expose a `{*}` placeholder that
yields this row's values pre-joined with tabs, in schema order. The two
are not the same affordance, and a shared spelling for the
minimal-renderer tier is an open decision.

The fixtures compare the **column order re-parsed from each runtime's own
output**, never raw bytes — table padding and YAML block style differ
legitimately between runtimes. Byte-level formatting parity is pinned by
each SDK's own unit tests instead. `csv` output agrees byte-for-byte
across go/py/ts in the default LF mode; the `crlf` option exposes known
quoting divergences.

### Backward compatibility

The legacy `render(w, format, v)` signature stays. It is now a thin
shim over `defaultRegistry.lookup(format).render(...)`. Existing
callers do not need to migrate.

```ts
import { render, JSON_FORMAT } from '@hop-top/kit/output';
render(process.stdout, JSON_FORMAT, { ok: true });
```

## Serve

`<tool> serve` supervises every configured and enabled service;
`<tool> serve <service>` selects exactly one. Both forms share one
lifecycle, so a service started by the selector observes the same
readiness, shutdown, and exit semantics as the same service started by
the supervisor.

The normative contract is
[`docs/contracts/serve-lifecycle.md`](../../contracts/serve-lifecycle.md);
this module implements the part of it §"Cross-language parity" makes
binding on every SDK. Go is the reference implementation.

```ts
import { createCLI } from '@hop-top/kit/cli';
import { ServiceRegistry, registerServe } from '@hop-top/kit/serve';

const registry = new ServiceRegistry();
registry.register({
  name: 'api',
  async start(signal, ready) {
    // Report ready once every acquisition that can fail has succeeded.
    ready();
    await new Promise<void>((r) =>
      signal.addEventListener('abort', () => r(), { once: true }));
  },
  ready: () => true,
  async stop() { /* drain, then release */ },
});

const { program } = createCLI({
  name: 'mytool', version: '1.0.0', description: 'My tool',
});
registerServe(program, { registry, configs: { api: { enabled: true } } });
program.parse();
```

### The override rule

`serve <service>` starts the named service **even when
`services.<name>.enabled` is false**, provided it is registered and its
configuration and policy validate. Enablement answers "does the
supervisor start this by default"; it is not an authorization decision,
and an operator naming a service has already made the decision the flag
exists to automate.

Under the supervisor form a disabled service is skipped silently and
does not affect the exit code. A supervisor invocation that resolves to
zero services exits 2 rather than 0: a process that exits 0 without
listening is indistinguishable from a successful start to systemd or a
container runtime.

### Configuration

| Key | Type | Default |
|-----|------|---------|
| `services.<name>.enabled` | bool | `false` |
| `services.<name>.ready_timeout` | duration | `30s` |
| `services.<name>.stop_timeout` | duration | `30s` |
| `services.failure_policy` | `fail-fast` \| `isolate` | `fail-fast` |
| `services.shutdown_timeout` | duration | `60s` |

`registerServe` takes the resolved blocks as `configs`; this port does
not resolve dotted keys itself, because `@hop-top/kit/config` merges
whole documents rather than resolving per key. The contract requires the
key *names*, not a particular resolution engine.

### Exit codes

| Situation | Code | Exit |
|-----------|------|------|
| Clean stop after a signal | `OK` | 0 |
| Invalid selection, invalid config, zero services | `USAGE` | 2 |
| Unknown service name | `NOT_FOUND` | 3 |
| Policy denied | `UNAUTHORIZED` | 5 |
| Start failure, runtime crash, shutdown budget exceeded | `GENERIC` | 1 |

### Lifecycle events

Six transitions are surfaced, on the bus when a `publisher` is wired and
through the `logger` either way:
`kit.serve.service.{started,ready_reported,failed,stopped}` and
`kit.serve.supervisor.{ready_reported,stopped}`. The service identifier
travels in the payload, never in the topic, so a subscriber is not
forced to re-bind when a tool gains a service.

### What this port does not do

The contract rules these out as Go-only; they are absent here by design,
not unimplemented: the REST/OpenAPI projection of the command tree, the
Unix socket service, `cmdreflect`-driven discovery, and the permission,
provenance, and audit surface. This SDK ships no HTTP or socket server,
so a service that listens is the adopter's to write.

## MCP surface

Serves the Model Context Protocol over a bridged command tree, one MCP
tool per runnable leaf. A single mount answers **both** revisions —
`2024-11-05` (handshake) and `2026-07-28` (stateless per-request
envelope) — choosing per request, because the newer revision has no
handshake to negotiate with.

Wire behaviour is pinned by the shared cross-language fixtures in
`sdk/tests/cross-lang/fixtures/mcp-wire.json`, compared as raw bytes.

### Use the v2 scoped packages

The protocol layer comes from `@modelcontextprotocol/core` and
`@modelcontextprotocol/server`, both pinned at `2.3.1`. They are already
dependencies of this package.

**`@modelcontextprotocol/sdk` — the v1 package — is deliberately not a
dependency.** It is legacy-era only: its `LATEST_PROTOCOL_VERSION` is
`2025-11-25`, and `2026-07-28` appears nowhere in it. It cannot serve
the modern era. It is also the name every search result reaches for, so
it is the most likely mistake here — code that imports it passes the
legacy fixtures and fails every modern one.

### Hosting: a framework-free handler

`createMcpHandler` returns a plain async function, not a server. kit
does not own your HTTP stack, and a pure request-to-response function is
testable against the fixtures with no socket open.

```ts
import { createMcpHandler, commanderBridge } from '@hop-top/kit/mcp';

const bridge = commanderBridge(rootCommand, {
  run: async (inv, cmd) => ({ stdout: await execute(cmd, inv.flags), exitCode: 0 }),
});

const handler = createMcpHandler(bridge, {
  serverInfo: { name: 'my-cli', version: '1.4.0' },
});

const res = await handler({ method: 'POST', headers, body });
// res: { status, headers, body }
```

`commanderBridge` requires a `run` callback — kit does not dictate how a
command's output is captured. Bind the handler to node:http, hono,
express, fastify, or a Worker.

### Options

`path` (`/mcp`), `serverInfo` (`cmdsurface` / `0.0.0`), `specVersions`
(both eras), `cacheHints` (`ttlMs` 0, `cacheScope` `private`),
`originAllowlist` (empty, no check), `confirmationKey`, and `policy`
(`defaultPolicy()`). The option *set* is normative across every kit SDK;
only the spelling is idiomatic.

An explicitly empty `specVersions`, a negative ttl, an unrecognized
cache scope, or an empty confirmation key all throw at mount time rather
than starting a server that quietly serves nothing.

### Safety

Exposure is gated by `policyAllowed(policy, cls, surface)`. The default
is deliberately closed: **no remote surface may invoke a destructive
leaf** — `defaultPolicy()` leaves `allowDestructiveOn` empty, and empty
means block-all. A blocked call returns an `isError` result at HTTP 200,
not a transport error: the call was understood and declined, not
malformed.

```ts
const policy = { allowDestructiveOn: ['mcp'], defaultEnabled: ['cli', 'lib', 'mcp'] };
```

Leaves are classified from `kit/*` annotations: `kit/side-effect`
(`destructive`, `destructive-local`, `destructive-shared`),
`kit/auth-required`, `kit/requires-confirmation`.

A `kit/auth-required` leaf runs only for a caller the mount's verifier
established: `createMcpHandler(bridge, { verifier })`, where
`verifier(req)` returns an `McpIdentity`, `null`, or a promise of
either. An `Authorization` header alone is refused with 401. The
verified `caller`, `tenant` and `scopes` reach the bridge on
`inv.meta`. See [Admit authenticated callers](../guides/serve-mcp-from-any-sdk.md#admit-authenticated-callers).

This gate is unrelated to the Factor 10 `safetyGuard` `--force` helper,
which is a CLI-time TTY check for delegation safety.

### Confirmation is header-only in this port

A `kit/requires-confirmation` leaf requires an `X-Confirm-Token` header.
`confirmationKey` is accepted and validated at mount time but **the
modern handler does not read it** — the MRTR elicitation round-trip that
Go, Python, Rust and PHP offer is not implemented here yet. Do not plan
an elicitation flow against this port today.

### Scope

Deprecated upstream features (Roots, Sampling, Logging, HTTP+SSE) are
unimplemented, matching the Go reference. `tasks/*` answers `-32601` and
no `extensions` map is advertised in `server/discover`, which is the
conformant way to not support it.

### Cross-references

- [Serve MCP from any SDK](../guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## URI facade

The URI module delegates to `@hop-top/cite` (`^0.1.2`); it does not
reimplement the URI contract. It mirrors the kit Go URI command intent
for SDK consumers:

```ts
import { parse, resolve, complete, handler, newRegistry } from '@hop-top/kit/uri';

const policy = {
  schemeNamespaceSegments: { tlc: 2 },
  actionRoutes: {
    'task.claim': {
      command: 'tlc',
      args: ['-C', '{namespace}', 'task', 'claim', '{id}'],
    },
  },
};

const parsed = parse('tlc://hop-top/kit/T-0001?cmd=task&verb=claim', policy);
const plan = resolve(parsed, policy); // command plan only; never executes

const registry = newRegistry(policy);
registry.register({ name: 'tlc', completer: async () => ['hop-top/kit/T-0001'] });
const suggestions = await complete(registry, 'tlc', 'tlc://T-');

const id = handler.id({
  vendor: 'hop-top',
  app: 'tlc',
  language: 'ts',
  scheme: 'tlc',
  appPath: '/usr/local/bin/tlc',
});
```

## Telemetry

`@hop-top/kit/telemetry` ships the SDK-side primitives for the kit
telemetry contract: a non-blocking `Client`, a best-effort PII /
secret redactor, and the consent + install-id readers needed to gate
emission.

**Default-denied**: a fresh install never emits. The `Client.record()`
call is a no-op unless the operator has run `kit consent grant` AND a
mode is selected via `KIT_TELEMETRY_MODE=anon|full` (or the prefixed
`<APP>_TELEMETRY_MODE` equivalent). Inspect what would ship with `kit
telemetry inspect`.

```ts
import { Client } from '@hop-top/kit/telemetry';

const client = new Client({
  sink: 'jsonl',                          // or 'https'
  sinkFile: '/tmp/kit-events.jsonl',      // jsonl sink only
  endpoint: 'https://telemetry.example',  // https sink only
  sdkVersion: '1.2.3',
});

// Fire-and-forget. Returns synchronously; never throws.
client.record('cmd.invoked', { command: 'launch', exit_code: 0 });

// Best-effort drain on process exit.
await client.shutdown(5_000);
```

### Configuration

All `ClientOptions` fields are mirrored by env vars (env wins when the
option is omitted):

| Field       | Env var                       | Default                                              |
| ----------- | ----------------------------- | ---------------------------------------------------- |
| `endpoint`  | `KIT_TELEMETRY_ENDPOINT`      | —                                                    |
| `sink`      | `KIT_TELEMETRY_SINK`          | `jsonl`                                              |
| `sinkFile`  | `KIT_TELEMETRY_SINK_FILE`     | `$XDG_STATE_HOME/kit/telemetry/events.jsonl`         |
| `queueSize` | `KIT_TELEMETRY_QUEUE_SIZE`    | `1024`                                               |

### Redactor

The default `redact()` pass scrubs emails, IPv4 / IPv6 addresses, `sk-`
/ `gh[pousr]_` / `xoxb-` token prefixes, and `$HOME` path prefixes
before the envelope hits a sink. Placeholders (`<redacted:email>`,
`<redacted:ipv4>`, `<redacted:ipv6>`, `<redacted:token>`) are
byte-parity with the py / rs / php SDKs so the cross-language contract
harness can diff outputs.

```ts
import { redact, redactString } from '@hop-top/kit/telemetry';

redactString('user@example.com from 10.0.0.1'); // → '<redacted:email> from <redacted:ipv4>'
redact({ ip: '8.8.8.8', count: 3 });            // → { ip: '<redacted:ipv4>', count: 3 }
```

A per-`Client` `redactor` callback runs BEFORE the default pass — use
it for adopter-specific allowlists or stricter rules. Throwing
callbacks are isolated (the event still goes through the default
pass).

### Envelope

Each emitted event is one NDJSON line with the shape:

```json
{
  "schema_version": "1",
  "sdk_lang": "ts",
  "sdk_version": "0.4.0",
  "installation_id": "<64-char hex sha256>",
  "mode": "anon",
  "occurred_at": "2026-05-19T12:00:00.000Z",
  "event": "cmd.invoked",
  "attrs": { "command": "launch", "exit_code": 0 }
}
```

The `event` + `attrs` extension is the TS / Py divergence from Go's
canonical envelope (Go pins a typed `Event` struct). See
[`sdk/docs/telemetry-event-schema.md`](../../../sdk/docs/telemetry-event-schema.md)
for the cross-language contract.

### Cross-SDK contract harness

The harness at `sdk/tests/cross-lang/` diffs envelopes across SDKs. The
TS telemetry runner is not wired into it as of this revision.

## LLM adapters

Import an adapter once for its side effect, then resolve a URI:

```ts
import '@hop-top/kit/anthropic';
import { createLLM } from '@hop-top/kit/llm';

const llm = createLLM('anthropic://claude-sonnet-4-5', {
  fallback: ['openai://gpt-4.1-mini'],
});
const st = llm.streamWithTools({ messages, maxTokens: 4096, thinking: { budgetTokens: 2048 } }, tools);
for await (const tok of st) process.stdout.write(tok.content);
const turn = st.outcome(); // content, thinking, toolCalls, finishReason, usage
```

| Import | Schemes | Default endpoint | Env key |
|--------|---------|------------------|---------|
| `@hop-top/kit/anthropic` | `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` |
| `@hop-top/kit/openai` | `openai`, `openrouter`, `xai`, `groq`, `together`, `fireworks`, `deepseek`, `mistral`, `lmstudio` | per scheme, as in Go | `<SCHEME>_API_KEY` (`lmstudio`: none) |
| `@hop-top/kit/routellm` | `routellm` | `http://localhost:6060/v1` | `ROUTELLM_API_KEY`, optional |

Each provider has `complete`, `stream`, `callWithTools` and
`streamWithTools`. `stream` and `streamWithTools` return a `ToolStream`:
text tokens, then one `{ done: true }`; `outcome()` is defined after it.

### URIs, keys and llm.yaml

Same as Go ([`llm.md`](llm.md)), pinned by `contracts/llm-v1`
(`parse_uri`, `key_endpoint`):

- `parseURI('scheme://[host:port/]model[?params]')`: the model may be
  empty; a first segment that splits as `host:port` is the host
  (`ollama://[::1]:11434/llama3`). The query splits on `&` and is not
  percent-decoded; `?flag` is `flag: ""`. Errors (`LLMError`): `empty
  URI`, `missing scheme in URI "..."`, `empty scheme in URI "..."`,
  quoting the input with credentials masked.
- `loadConfig(uri?)` reads `{configDir('hop')}/llm.yaml` (`default`,
  `fallback`, `providers.<scheme>`: `api_key`, `api_key_env`,
  `base_url`, `model`; other keys become `provider.extras`) on each
  call. Missing or malformed, it counts as empty.
- Key, highest first: the URI's `api_key`; llm.yaml `api_key`, then the
  variable `api_key_env` names; the scheme's variable (table above);
  `LLM_API_KEY` for a scheme whose adapter requires a key. Blank is
  unset everywhere. A scheme no adapter serves gets only its own
  llm.yaml key. No secret store.
- Base URL: the URI's `base_url`; its host (`http://host:port`);
  `LLM_BASE_URL`; llm.yaml `base_url`. URI: the argument;
  `LLM_PROVIDER`; llm.yaml `default`. Fallbacks: `LLM_FALLBACK`;
  llm.yaml `fallback`.
- `resolve(uri)` is `loadConfig` plus the adapter, so it resolves the
  key too (Go: `ApplyAPIKey`, then `Resolve`).
- `applyAPIKey(uri)` returns `uri` with the resolved key appended as
  `api_key` (unchanged when it has one or the scheme takes none); a
  required key found nowhere throws `MissingKeyError` (`envVars` lists
  every variable consulted).

- Anthropic: `Request.thinking` (`budgetTokens` ≥ 1024 and below
  `maxTokens`) streams thinking deltas as `Token.thinking`; replay
  `thinking` blocks on the assistant message unchanged.
  `cacheControl: { ttl }` (`""`, `"5m"`, `"1h"`) on a message or
  `ToolDef` marks a prompt-cache breakpoint. `Usage` carries
  `cacheReadTokens` / `cacheCreationTokens`; `usageCost(usage, price)`
  prices each kind per million tokens. `ANTHROPIC_BASE_URL`,
  `ANTHROPIC_AUTH_TOKEN` (bearer) and `ANTHROPIC_WORKSPACE_ID` are read
  from the environment, unless hermetic.
- Anthropic system messages, wherever they sit, join in order with a
  blank line (`"\n\n"`) into the top-level `system` field; empty ones
  are skipped. A `cacheControl` on a system message ends a text block
  there with that breakpoint, so each breakpoint caches exactly the text
  up to it. (Before, only the last system message was sent.)
- OpenAI: a host form (`openai://host:port/model`) reaches
  `http://host:port/v1`; a `base_url` param is used as given. A blank
  key sends no `Authorization` header.
- `routellm` delegates to the openai adapter at the RouteLLM server.
  The model field takes either shape the server advertises, as in Go:
  `routellm://mf:0.7` (router and threshold, sent as `router-mf-0.7`)
  or a tier name, `routellm://private`, sent verbatim for the server to
  resolve from its tier config. The text after the last colon decides:
  a finite float (Go `ParseFloat` syntax) makes a router pair, whose
  threshold must be in `[0, 1]`; anything else is a tier, colons
  included (`mf:abc` is a tier). An unknown tier fails at the server.
  `parseModelField()` returns `{ isTier, tier, routerName, threshold }`;
  `RouteLLMAdapter` exposes `tier` and `isTier()`.
- `routellm` base URL, as in Go: the provider base URL (URI `base_url`
  or host, `LLM_BASE_URL`, llm.yaml `base_url`) first, then `ROUTELLM_BASE_URL`, then extras
  `routellm.base_url`, then `http://localhost:6060`; `/v1` is appended
  once. Hermetic reads no `ROUTELLM_*` variable. `RouteLLMAdapter.baseURL`
  is the resolved API root. (Before, `ROUTELLM_BASE_URL` and extras
  outranked the URI.)

Params on any adapter URI: `tool_names` (`sanitize` | `strict`),
`max_body_bytes` (default 32 MiB) and `max_event_bytes` (one SSE event,
default 8 MiB).

Transport guarantees, the same as Go (except that Go's openai adapter
follows redirects and does not scrub a key a response echoes):

- A key goes over plain http only to loopback, and never to an endpoint
  with userinfo, a backslash, a percent-escape, whitespace or a control
  character (`KeyEndpointError`). A key not from the URI (llm.yaml or
  environment) reaches an endpoint only the URI names only when it is
  loopback or shares an origin with the scheme's default, its llm.yaml
  `base_url` or `LLM_BASE_URL`; `loadConfig`, `resolve` and
  `applyAPIKey` refuse it otherwise. The Anthropic bearer token and
  workspace header follow the same rule, `ANTHROPIC_BASE_URL` trusted
  too.
- Redirects are not followed (`HTTPStatusError`, status 3xx).
- Two retries for connection failures, 408, 409, 429 and 5xx
  (`x-should-retry` overrides); a `retry-after-ms` / `retry-after` wait
  is honored within (0, 60s], else exponential backoff from 0.5s.
- A body or event over its cap is `ResponseTooLargeError`, not retried.
  A stream that ends before its terminal event (`message_stop`;
  `[DONE]` or a finished choice on OpenAI) is `StreamTruncatedError`.
- Errors never carry a credential: keys are scrubbed from messages,
  causes and stacks.

| Error | When | `isFallbackable` |
|-------|------|------------------|
| `AuthError` | 401 | no |
| `PermissionDeniedError` (an `AuthError`) | 403, an Anthropic `permission_error` stream event | no |
| `ModelError` | 404 | no |
| `RateLimitError` | 429 | yes |
| `HTTPStatusError` | 5xx, overloaded stream event (529), a redirect | 5xx only |
| `APIError` | any other error response or stream event (`statusCode`, `type`) | no |
| `StreamTruncatedError`, `ResponseTooLargeError`, `KeyEndpointError`, `ToolNameError` | as above | no |

Bundles share one registry and one set of error classes, so
`instanceof` and `isFallbackable` hold across entry points.
`redactURI(uri)` masks credentials in a URI before you log it.

`PermissionDeniedError`: the key was accepted but may not use the
model, workspace or feature; retrying with it will not help. It extends
`AuthError`, so `instanceof AuthError` still catches every 401 and 403;
test `PermissionDeniedError` first to tell them apart.

### Stream events

Set `Request.streamEvents` and the anthropic adapter's `stream` and
`streamWithTools` also yield tool-call events and usage snapshots
(Go [Tool-call and usage events](llm.md#tool-call-and-usage-events)):

```ts
for await (const tok of llm.streamWithTools({ messages, streamEvents: true }, tools)) {
  if (tok.usage) enforceBudget(tok.usage);       // cumulative, never a delta
  else if (tok.toolCall?.kind === 'end') run(tok.toolCall.call!);
  else process.stdout.write(tok.content);
}
```

- A token carries one thing: `content`, `thinking`, `toolCall`, `usage`
  or `done`; `content` is `""` on all but text.
- `toolCall`: per call one `start` (`index`, `id`, original `name`), any
  `delta` (`argumentsDelta`, raw JSON fragments), one `end` (`call`,
  exactly `outcome().toolCalls[index]`). Parallel calls interleave by
  `index`. A call open when the stream fails gets no `end`.
- `usage`: when the provider reports it (message start, message delta);
  the last one equals `outcome().usage`.
- Unset, the stream yields exactly the tokens it did before. `complete`
  and `callWithTools` ignore it; the openai adapter does not emit events
  yet (Go's does).

### Tool choice

`Request.toolChoice` constrains tool use on `callWithTools` and
`streamWithTools` (Go [Tool choice](llm.md#tool-choice)):

| `mode` | Anthropic | OpenAI, `routellm` |
|--------|-----------|--------------------|
| `auto` | `{type: "auto"}` | `"auto"` |
| `any` | `{type: "any"}` | `"required"` |
| `none` | `{type: "none"}` | `"none"` |
| `tool` + `name` | `{type: "tool", name}` | `{type: "function", function: {name}}` |

`name` is the `ToolDef.name` as defined; it goes out under its wire
name. Unset sends no `tool_choice`. `checkToolChoice(choice, tools)`
rejects, and every adapter throws `LLMError` before sending, for an
unknown mode, no tools (including `complete` and `stream`), a missing
name with `tool`, a name with another mode, or a name no tool has.

### Hermetic mode

`{ hermetic: true }` on `createLLM`, `resolve` or `loadConfig` reads
nothing ambient: the provider comes from the URI alone (`api_key`,
`base_url`, host). No `LLM_PROVIDER`, `LLM_FALLBACK`, `LLM_API_KEY`,
`LLM_BASE_URL`, `<SCHEME>_API_KEY` (`ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, ...), `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
`ANTHROPIC_WORKSPACE_ID` or `ROUTELLM_*`, and no llm.yaml. Built
directly, an adapter takes
`ResolvedConfig.hermetic`. Go: [Hermetic mode](llm.md#hermetic-mode).

```ts
const llm = createLLM(`anthropic://claude-sonnet-4-5?api_key=${key}`, { hermetic: true });
```

### Lifecycle payloads

The TypeScript client publishes no bus events: `onRequest` and
`onResponse` run in process with the full request and response (for
`callWithTools`, the response now carries `usage`, `finishReason` and
`thinking`). To forward a call to a bus, log or peer, send the
metadata-only payload Go publishes on `kit.ai.request.started` and
`kit.ai.response.received`
([no content](llm.md#request-and-response-payloads-carry-no-content)):

```ts
import { requestStartedPayload, responseReceivedPayload } from '@hop-top/kit/llm';

requestStartedPayload('tool_call', req, tools.length);
// { op, model?, messages, roles, parts?, content_bytes, tools, max_tokens,
//   has_temperature, thinking, stop_sequences, extension_keys? }
responseReceivedPayload('tool_call', resp, resp.toolCalls, ms);
// { op, finish_reason?, usage, content_bytes, tool_calls, duration_ms }
```

Neither holds message text, tool names or arguments, extension values
or media; `content_bytes` is their UTF-8 size. Keys are the wire
spelling the llm-v1 `bus_*` vectors pin.

## LLM tool calling

`@hop-top/kit/llm` mirrors the Go `llm.Message`: `role`, `content`,
optional `parts`, and tool-call linkage. The next request replays the
model's calls, then one result per call, linked by id:

```ts
const resp = await client.callWithTools({ messages }, tools);
messages.push({ role: 'assistant', content: resp.content, toolCalls: resp.toolCalls });
for (const call of resp.toolCalls) {
  messages.push({ role: 'tool', content: await run(call), toolCallId: call.id });
}
```

| Field | Role | Meaning |
|-------|------|---------|
| `toolCalls` | `assistant` only | calls the model made; `content` may be `""` |
| `toolCallId` | `tool` only | the `ToolCall.id` this result answers; result text in `content` |
| `parts` | `user` | `ContentPart[]` (`text`, `image`, ...); used instead of `content` when non-empty |

- `ToolCall.arguments` is the parsed JSON value or a JSON-encoded
  string; adapters send a string as is and encode anything else.
- `checkToolLinkage(message)` enforces the role rules and rejects a tool
  result with `parts`; call it first in a custom adapter.
- Media for a part comes from `inlineSource(bytes, mime)`,
  `urlSource(url, { maxBytes })` or `fileSource(path)`.
- A media URL is treated as untrusted. The anthropic adapter (images,
  PDFs) and the openai adapter (images) send the URL to the provider and
  never read it. `urlSource(url).read()`, used everywhere else, fetches
  under a guard: `http` or `https` only, no userinfo, every address the
  host resolves to public (loopback, private, link-local such as
  `169.254.169.254`, shared, documentation, reserved and multicast
  refused, v4-mapped and NAT64 IPv6 judged by their IPv4; the socket
  connects to the vetted address, so DNS cannot rebind it), no proxy,
  no redirects (`HTTPStatusError`), 30s, at most `maxBytes` (32 MiB;
  `MediaTooLargeError` past it), and `--offline` applies. Refusals are
  `MediaURLRefusedError`. No switch allows private hosts: read local
  media yourself and pass `inlineSource` or `fileSource`. Same rules as
  Go: [Media URLs](llm.md#media-urls).
- Returned calls carry `arguments` parsed (raw text when the provider
  sent invalid JSON).
- The openai adapter (and `routellm` through it) sends `tool_calls`
  (`id`, `type: "function"`, `function` with string `arguments`) and
  `role: "tool"` + `tool_call_id`. The anthropic adapter sends
  `tool_use` / `tool_result` blocks and merges consecutive results into
  one user turn. Both throw `LLMError` instead of
  sending plain text for: either field on the wrong role, a tool result
  without `toolCallId` or with `parts`, a call without an `id`,
  arguments that are not valid JSON, `parts` on a non-`user` role, and
  `audio` / `video` parts.
- Messages without the new fields serialize as `{role, content}`, as
  before.

Go contract and per-provider wire mapping:
[Returning tool results](llm.md#returning-tool-results).

### Tool names

Providers restrict tool names; ids such as `pane.open` are fine as
`ToolDef.name`. The rules and the sanitizer match every kit SDK
([tool-names.md](../../../contracts/llm-v1/tool-names.md)):

| Rule | Accepted names |
|------|----------------|
| `ANTHROPIC_TOOL_NAMES` | `^[a-zA-Z0-9_-]{1,128}$` |
| `OPENAI_TOOL_NAMES` (openai schemes, `routellm`) | `^[a-zA-Z0-9_-]{1,64}$` |
| `GEMINI_TOOL_NAMES` | `^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$` |

```ts
import { OPENAI_TOOL_NAMES, ToolNames, sanitizeToolName } from '@hop-top/kit/llm';

sanitizeToolName(OPENAI_TOOL_NAMES, 'pane.open');   // 'pane_open'
const names = ToolNames.build(OPENAI_TOOL_NAMES, 'sanitize', tools);
names.defs(tools);          // tools under their wire names
names.calls(resp.toolCalls) // calls back under the original names
```

- `sanitizeToolName` works on UTF-8 bytes: each run of other bytes
  becomes one `_`; a name still too long, or with no ASCII letter or
  digit, is cut and ends in `_` + 8 hex of its SHA-256 (`node:crypto`).
- `ToolNames.build` throws `ToolNameError` (an `LLMError`, never
  fallbackable) for the first bad tool: `reason` is `empty`,
  `invalid_utf8` (a lone surrogate), `invalid` (strict only),
  `duplicate` or `collision`; `toolName` is the tool, `other` the
  earlier tool a collision maps onto. (`name` is the class name, as on
  every `Error`.)
- `?tool_names=strict` (`toolNameModeOf(params)`) sends names as given
  and fails an invalid one before sending; `sanitize` is the default;
  any other value throws when the adapter is built.
- Every entry point of the anthropic, openai and `routellm` adapters
  applies the mode: tool definitions and replayed `toolCalls` go out
  under wire names; returned calls, streamed outcomes and history come
  back under the original names.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
- [`serve-mcp-from-any-sdk.md`](../guides/serve-mcp-from-any-sdk.md) — MCP hosting across every SDK
- [`sdk/ts/README.md`](../../../sdk/ts/README.md) — the package README
