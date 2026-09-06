# @hop-top/kit (TypeScript)

Shared CLI utilities for hop-top tools — CommonJS edition.

Mirrors the Go `kit` package surface: a Commander-based CLI factory,
output formatting with a registry/Formatter contract, theming, hint
plumbing, XDG paths, config files, an embedded SQLite store, upgrade
checks, LLM helpers, alias resolution, and a TUI toolkit.

## Install

```sh
pnpm add @hop-top/kit
```

Subpath imports follow the package's `exports` map (see
`package.json`): `@hop-top/kit/cli`, `@hop-top/kit/output`,
`@hop-top/kit/xdg`, `@hop-top/kit/uri`, …

## Output formatting

The `output` module ships a Formatter + Registry contract identical to
the Go `go/console/output` package, plus a Commander integration layer
that wires the standard flag suite onto your program.

### Built-in formatters

| Key     | Extensions     | Options                                                                           |
| ------- | -------------- | --------------------------------------------------------------------------------- |
| `json`  | `.json`        | `indent` (int, default 2)                                                         |
| `yaml`  | `.yaml`, `.yml`| `flow-level` (int, default -1)                                                    |
| `table` | (none)         | (none)                                                                            |
| `csv`   | `.csv`         | `delimiter` (string), `no-header` (bool), `quote-all` (bool), `crlf` (bool)       |
| `text`  | `.txt`         | `style` (enum kv\|lines\|paragraph), `separator` (string, kv only)                |

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
| Built-in formats | `table`, `json`, `yaml`, `csv`, `text` (+ `human`) | same five | `table`, `json`, `yaml` only |
| Ordered columns on the template path | `.Cols` | `cols` | `{*}` (php); none (rs) |

Go's inability to express `header != key` is the *reason* rule 3 binds
every runtime: no SDK may carry a capability the reference cannot mirror.

### Conformance status

TS satisfies all five rules, as does Go across all five formats. The
fixtures under `sdk/tests/cross-lang/` execute the contract against every
runtime. Two gaps remain open and matter when writing portable code:

- **`csv` and `text` do not exist in rs or php.** Only `table`, `json`
  and `yaml` are available in all five runtimes today. The fixtures
  record this as `rs-php-no-csv-text`.
- **rs has no ordered-column affordance on the `--template` path.** Go
  exposes `.Cols` and py and ts expose `cols`; php has a `{*}`
  placeholder yielding pre-joined values. The spelling for rs is an open
  decision.

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

## Other modules

- `@hop-top/kit/cli` — `createCLI(cfg)` builds a Commander root program
  with the hop-top contract: `--format`, `--quiet`, `--no-color`,
  `--no-hints`, `--offline`, themed help, version, hidden completion
  command.
- `@hop-top/kit/netpolicy` — enforcement behind the `--offline` global.
  `createCLI` guards `globalThis.fetch`, so a leaf that never consults
  the marker is still refused; loopback stays reachable. Callers that
  inject their own transport wrap it with `guardFetch`, and socket-level
  clients (`node:net`, SQL drivers, gRPC) consult `isOffline()`
  themselves. Match the refusal with `isOfflineError(err)`.
- `@hop-top/kit/id` — TypeID primitive (cross-language; see
  [ADR 0001](../../docs/adr/0001-typeid-primitive.md)). Source:
  [`src/id/`](src/id/).
- `@hop-top/kit/xdg` — XDG Base Directory paths.
- `@hop-top/kit/config` — config-file loading.
- `@hop-top/kit/sqlstore` — embedded SQLite key/value store.
- `@hop-top/kit/upgrade` — semver upgrade detection.
- `@hop-top/kit/llm` / `routellm` — LLM client + routing helpers.
- `@hop-top/kit/alias` — alias resolution + completion.
- `@hop-top/kit/uri` — thin facade over `@hop-top/cite` for URI parsing,
  action resolution, completions, registries, and OS handler metadata.
- `@hop-top/kit/tui` — TUI toolkit (parity, anim, prompts).
- `@hop-top/kit/mcp` — dual-spec MCP surface (see [MCP surface](#mcp-surface)).
- `@hop-top/kit/serve` — the serve hierarchy and service lifecycle (see
  [Serve](#serve)).

See package.json `exports` for the full list.

## Serve

`<tool> serve` supervises every configured and enabled service;
`<tool> serve <service>` selects exactly one. Both forms share one
lifecycle, so a service started by the selector observes the same
readiness, shutdown, and exit semantics as the same service started by
the supervisor.

The normative contract is
[`docs/contracts/serve-lifecycle.md`](../../docs/contracts/serve-lifecycle.md);
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
`@modelcontextprotocol/server`, both pinned at `2.0.0`. They are already
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

- [Serve MCP from any SDK](../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
  — the polyglot adopter guide
- [Expose your CLI over MCP](../../docs/adopters/guides/expose-cli-over-mcp.md)
  — the Go reference surface, in depth

## URI facade

The URI module delegates to `@hop-top/cite` (`^0.1.0`); it does not
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
`sdk/docs/telemetry-event-schema.md` for the cross-language contract.

### Cross-SDK contract harness

The harness at `sdk/tests/cross-lang/` diffs envelopes across SDKs.
As of this revision the TS-side telemetry wiring into it is deferred.

## License

MIT.

<!-- release: track @hop-top/cite ^0.1.0 -->
