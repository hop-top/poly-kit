# Log API Reference

> Thin, opinionated logger wrapping ecosystem-standard libraries.
> Reads `quiet` and `no-color` from app config. Themed level
> prefixes. Output to stderr.

## Who this is for

Authors of kit-based tools who want a logger that respects the
shared CLI contract (`--quiet`, `--no-color`) without writing the
wiring themselves.

## Recommended path

```go
import "hop.top/kit/go/console/log"

l := log.New(v)                          // InfoLevel
l.Info("starting", "port", 8080)
```

`v` is the `*viper.Viper` already wired by `cli.New`. Log goes to
stderr with themed level prefix.

---

## Reference

### Go (`hop.top/kit/go/console/log`)

Wraps `charm.land/log/v2`. Config via `*viper.Viper`.

#### Constructors

| Function                      | Description                                        |
|-------------------------------|----------------------------------------------------|
| `New(v *viper.Viper)`         | logger at the contract's zero-verbosity level (info) |
| `WithVerbose(v, verbose int)` | logger at the level implied by a `-V` repeat count |
| `WithLevel(v, level)`         | logger at an explicit level                        |

Verbosity counts resolve through `contracts/parity/parity.json`
(`verbosity.levels`): `0` = info, `1` = debug, `2` = trace.

`TraceLevel` is kit-local (`log.DebugLevel - 1`); `charm.land/log/v2`
has no Trace of its own.

#### Viper keys

| Key        | Effect                                   |
|------------|------------------------------------------|
| `quiet`    | raises level to the contract's `verbosity.quiet_override` (currently `warn`) |
| `no-color` | disables ANSI escape sequences           |

`quiet` only raises; never lowers. If the caller requests
ErrorLevel, `quiet` does not downgrade to WarnLevel.

### TypeScript (`@hop-top/kit/log`)

Wraps `pino` with a kit transport. Variadic key-value API, same as Go.

```ts
import { createLogger, withVerbose } from "@hop-top/kit/log"

const log = createLogger({ quiet: false, noColor: false })
log.info("starting", "port", 8080)

const v = withVerbose({ verbose: 1 })   // -V count → level via parity.json
```

#### Options

```ts
type LogLevelName = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal'

interface LoggerOptions {
  quiet?: boolean;            // raise to verbosity.quiet_override (warn)
  noColor?: boolean;          // strip ANSI from level prefixes
  level?: LogLevelName | 'silent'; // explicit threshold
  output?: { write(chunk: string): unknown }; // default process.stderr
  format?: 'text' | 'json';   // default 'text'
  formatter?: (rec: LogRecord) => string; // custom line renderer
}

interface LogRecord {
  level: LogLevelName;
  msg: string;
  fields: Record<string, unknown>; // key-values in call order
  kind?: 'success';                // set on success() lines
}
```

- `createLogger` defaults to debug; `withVerbose` resolves the
  `-V` count through the parity contract.
- `level` wins over both defaults. `'silent'` drops every line.
  `quiet` only raises the level, never lowers it (same as Go
  `WithLevel`). `level` and `'silent'` are TS options, not parity
  verbosity entries.
- `output` redirects rendered lines (e.g. a test sink). The line
  format stays the same. Mirrors Go's `(*log.Logger).SetOutput`.
- `format: 'json'` writes one object per line, in the same shape as Go
  charm/log's `JSONFormatter` (see [Output format](#output-format)).
  You can only select it with this option; there is no env var or
  flag.
- `formatter` is the TS extension point for house styles. The
  returned string is written as-is, with `\n` appended if missing.
  It wins over `format` and `noColor`, and level filtering still
  applies. Glyph prefixes go here, for example with the parity status
  symbols:

  ```ts
  const glyphs = { info: "ℹ", warn: "▲", error: "●" }
  createLogger({
    formatter: (r) =>
      `${r.kind === "success" ? "✓" : glyphs[r.level] ?? "·"} ${r.msg}`,
  })
  ```

#### `success`

`log.success("deployed", "env", "prod")` logs at **info**. It is
filtered exactly like `info`, so it is not a new level. Text output
uses the `SUCC` label in the theme success colour (Guac `#52CF84`,
not bold). JSON output adds `"kind":"success"` and keeps
`"level":"info"`.

### Python *(planned: `hop_top_kit.log`)*

Will wrap `structlog` with `KitRenderer`.

```python
from hop_top_kit.log import create_logger

log = create_logger(quiet=False, no_color=False)
log.info("starting", port=8080)
```

#### Factory *(planned)*

```python
create_logger(quiet: bool = False, no_color: bool = False)
```

- `quiet` suppresses info + debug.
- `no_color` disables ANSI.
- Extensible via structlog processors.

## Level theme

All runtimes use the hop.top palette:

| Level | Prefix | Color         | Hex       |
|-------|--------|---------------|-----------|
| ERROR | `ERRO` | Cherry (pink) | `#FF388B` |
| WARN  | `WARN` | Yam (amber)   | `#FFB587` |
| INFO  | `INFO` | Squid (muted) | `#858392` |
| DEBUG | `DEBU` | Smoke (dim)   | `#BFBCC8` |
| TRACE | `TRAC` | Smoke (dim)   | `#BFBCC8` |
| FATAL | `FATA` | Cherry (pink) | `#FF388B` |
| SUCCESS (TS `success()`, info) | `SUCC` | Guac (green) | `#52CF84` |

ERROR, WARN and FATAL prefixes are bold; INFO, DEBUG and TRACE are
not. Hex values come from `charmtone`; the names are the source of
truth, the hexes track the palette.

## Output format

```
LEVEL msg key=val key=val\n
```

Output goes to stderr unless redirected (Go `SetOutput`, TS
`output`). Structured key=value pairs follow the message on the
same line.

JSON (TS `format: 'json'`, Go `SetFormatter(log.JSONFormatter)`):

```
{"level":"info","msg":"start","port":8080}
{"level":"info","msg":"deployed","kind":"success","env":"prod"}
```

Level strings are `trace|debug|info|warn|error|fatal`. Lines have no
time, pid or hostname fields.

## Cross-language parity

| Feature         | Go     | TS       | Python   |
|-----------------|--------|----------|----------|
| Quiet mode      | yes    | yes      | planned  |
| Verbosity (-V)  | yes    | yes      | planned  |
| No-color        | yes    | yes      | planned  |
| Level colors    | yes    | yes      | planned  |
| Stderr output   | yes    | yes      | planned  |
| Custom output   | `SetOutput` | `output` | no  |
| JSON format     | `SetFormatter`¹ | `format` | follow-up |
| Explicit level  | `WithLevel` | `level` | follow-up |
| Silent level    | follow-up | `'silent'` | follow-up |
| `success()`     | follow-up | yes  | follow-up |
| Custom formatter| n/a    | `formatter` (TS only) | processors |
| Transport ext.  | n/a    | pino     | structlog|

¹ charm/log's own setter on the returned logger; kit sets no formatter
and does not expose a kit-level option. Kit's `TraceLevel` has no
charm level string yet, so Go JSON output for trace lines is a
follow-up too.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go CLI factory
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — `--quiet`, `--no-color` contract
