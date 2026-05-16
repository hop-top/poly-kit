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

| Function                  | Description                     |
|---------------------------|---------------------------------|
| `New(v *viper.Viper)`     | logger at InfoLevel             |
| `WithLevel(v, level)`     | logger at explicit level        |

#### Viper keys

| Key        | Effect                                   |
|------------|------------------------------------------|
| `quiet`    | raises level to WarnLevel (suppresses INFO + DEBUG) |
| `no-color` | disables ANSI escape sequences           |

`quiet` only raises; never lowers. If the caller requests
ErrorLevel, `quiet` does not downgrade to WarnLevel.

### TypeScript *(planned: `@hop-top/kit/log`)*

Will wrap `pino` with kit transport.

```ts
import { createLogger } from "@hop-top/kit/log"

const log = createLogger({ quiet: false, noColor: false })
log.info("starting", { port: 8080 })
```

#### Factory *(planned)*

```ts
createLogger(opts?: { quiet?: boolean; noColor?: boolean })
```

- `quiet` suppresses info + debug (same as Go).
- `noColor` strips ANSI from transport output.
- Extensible via pino transports.

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

| Level | Prefix | Color        | Hex       |
|-------|--------|--------------|-----------|
| ERROR | `ERRO` | Cherry (red) | `#ED4A5E` |
| WARN  | `WARN` | Yam (amber)  | `#E5A14E` |
| INFO  | `INFO` | Squid (muted)| `#858183` |
| DEBUG | `DEBU` | Smoke (dim)  | `#BFBCC8` |
| FATAL | `FATA` | Cherry (red) | `#ED4A5E` |

ERROR and FATAL prefixes are bold; others are not.

## Output format

```
LEVEL msg key=val key=val\n
```

All output goes to stderr. Structured key=value pairs follow the
message on the same line.

## Cross-language parity

| Feature         | Go     | TS       | Python   |
|-----------------|--------|----------|----------|
| Quiet mode      | yes    | planned  | planned  |
| No-color        | yes    | planned  | planned  |
| Level colors    | yes    | planned  | planned  |
| Stderr output   | yes    | planned  | planned  |
| Transport ext.  | n/a    | pino     | structlog|

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go CLI factory
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — `--quiet`, `--no-color` contract
