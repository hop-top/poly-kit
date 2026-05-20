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

TS lacks struct tags; pass an explicit `ColumnSpec[]` list to `dispatch`
when you want headers/keys to differ or want priority ordering for
table layout:

```ts
import type { ColumnSpec } from '@hop-top/kit/output';

const userCols: ColumnSpec[] = [
  { header: 'ID',    key: 'id',    priority: 9 },
  { header: 'Name',  key: 'name',  priority: 8 },
  { header: 'Notes', key: 'notes', priority: 2 },
];

await dispatch(cmd, users, { columns: userCols });
```

When no `columns` is passed, headers are derived from the first row's
own enumerable keys.

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
  `--no-hints`, themed help, version, hidden completion command.
- `@hop-top/kit/xdg` — XDG Base Directory paths.
- `@hop-top/kit/config` — config-file loading.
- `@hop-top/kit/sqlstore` — embedded SQLite key/value store.
- `@hop-top/kit/upgrade` — semver upgrade detection.
- `@hop-top/kit/llm` / `routellm` — LLM client + routing helpers.
- `@hop-top/kit/alias` — alias resolution + completion.
- `@hop-top/kit/uri` — thin facade over `@hop-top/uri` for URI parsing,
  action resolution, completions, registries, and OS handler metadata.
- `@hop-top/kit/tui` — TUI toolkit (parity, anim, prompts).

See package.json `exports` for the full list.

## URI facade

The URI module delegates to `@hop-top/uri`; it does not reimplement the URI
contract. It mirrors the kit Go URI command intent for SDK consumers:

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

The harness at `hops/main/sdk/tests/cross-lang/` (planned in T-0709)
diffs envelopes across SDKs. As of this revision the harness is not
landed; TS-side wiring is deferred until the directory exists.

## License

MIT.
