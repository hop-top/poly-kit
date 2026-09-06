# Config Reference

> Hierarchical configuration loading and validation
> (`hop.top/kit/go/core/config`): the `-c` / `--config` wiring, typed
> writes with `Set` vs `SetValue`, `ParseScalar` coercion rules, and
> signal-driven hot reload with `Reloadable[T]`.

## Who this is for

Go authors of kit-built CLIs loading a typed config struct from layered
files, env and overrides, writing values back from a `config set`
command, or reloading on `SIGHUP` without restarting.

## Kit CLI integration

Kit-powered CLIs should not register their own `-c` / `--config` flag. The
console root already owns that global as a repeatable flag, where each token is
either an extra config file path or a dotted `key=value` override.

Adopters should keep the command-side wiring to the same few lines:

```go
paths, overrides, err := root.ConfigArgs()
if err != nil { return err }
err = config.Load(&cfg, config.Options{
    UserConfigPath:   userPath,
    ProjectConfigPath: projectPath,
    ExtraConfigPaths: paths,
    Overrides:        overrides,
})
```

If a product has a compatibility wrapper around this package, expose one
adapter there and pass `root.ConfigArgs()` through directly. Avoid reading
`viper.GetString("config")` or re-parsing the flag in each binary; that loses
repeatable config layers and key overrides, and can collide with Kit's built-in
flag registration.

## Writing values

Use `SetValue` when the value has a Go type; use `Set` only when the value
is genuinely a string.

```go
// typed write — emits keyword_threshold: 0.9
config.SetValue("keyword_threshold", 0.9, config.ScopeUser, opts)

// string write — emits name: "release"
config.Set("name", "release", config.ScopeUser, opts)
```

`Set(key, value string, scope, opts)` writes every scalar with the yaml tag
`!!str`. yaml.v3 then quotes anything that would otherwise resolve to a
non-string, so `Set("keyword_threshold", "0.9", ...)` writes
`keyword_threshold: "0.9"`. A consumer unmarshalling that key into a
`float64` fails to decode. Because config typically loads in
`PersistentPreRunE`, one such write breaks every later invocation of the
binary — including diagnostic commands — and recovery means hand-editing
the file.

`SetValue(key string, value any, scope, opts)` infers the yaml tag from the
Go type instead:

| Go type   | yaml tag  | emitted     |
|-----------|-----------|-------------|
| `float64` | `!!float` | `k: 0.9`    |
| `int`     | `!!int`   | `k: 123`    |
| `bool`    | `!!bool`  | `k: true`   |
| `nil`     | `!!null`  | `k: null`   |
| `string`  | `!!str`   | `k: abc`, and `k: "0.9"` when the text would otherwise resolve to a non-string |

### CLI path: raw arg to YAML

A CLI receives every value as a string, so it has no Go type to hand
`SetValue`. `ParseScalar(s string) any` bridges that gap — it converts the
raw arg to a typed value, which then feeds `SetValue`:

```go
// kit config set keyword_threshold 0.9
func runSet(key, raw string, opts config.Options) error {
    return config.SetValue(key, config.ParseScalar(raw), config.ScopeUser, opts)
}
```

```
raw arg "0.9"  →  ParseScalar → float64(0.9)  →  SetValue → keyword_threshold: 0.9
raw arg "true" →  ParseScalar → true          →  SetValue → verbose: true
raw arg "prod" →  ParseScalar → "prod"        →  SetValue → env: "prod"
```

### Type coercion

`ParseScalar` recognises floats, ints, bools, and null (`null` / `~`) only.
Everything else stays a string. The narrow surface is deliberate: a bare
`yaml.Unmarshal` of the arg would also convert `0x1F` to an int and
`2024-01-01` to a timestamp, which is lossy and surprising for a value the
user typed literally. `ParseScalar` leaves both as strings.

Inference is never allowed to change the value. A number too large to
represent exactly therefore stays a string rather than rounding through
`float64`: `9223372036854775808` does not fit in an `int`, and writing it
as `9.223372036854776e+18` would put a different number in the file than
the one typed. The same applies to oversized float spellings like `1e400`.

`yes`, `on`, `off`, and `no` also stay strings, but the two write paths
emit them differently:

| written via | emitted    | safe under YAML 1.1? |
|-------------|------------|----------------------|
| `SetValue`  | `k: "yes"` | yes — quoted         |
| `Set`       | `k: yes`   | no — bare token      |

`SetValue` routes through `yaml.Node.Encode`, which double-quotes exactly
these YAML 1.1 lookalikes, so the file is unambiguous to any reader.

`Set` hand-builds a `!!str` node with no style, and yaml.v3 emits the bare
token: under the YAML 1.2 core schema `yes` is already a string, so no
quoting is required. yaml.v3 decodes it back as a string and Go-to-Go
round-trips stay consistent — but a parser applying YAML 1.1 semantics will
read `yes` as a boolean. Prefer `SetValue` when the file may be read by a
non-yaml.v3 consumer.

Dropping the tag entirely is not a safe blanket fix either: without a tag,
inference happens at emit time and the caller loses control over whether a
value lands as a string or a number. Caller-supplied type intent is the
design.

### Migrating existing `Set` callers

Callers writing numerics or booleans through `Set` are producing quoted
scalars today, and consumers decoding those keys into non-string fields
fail. The fix is one line at each call site:

```go
// before — writes threshold: "0.9"
config.Set("threshold", raw, config.ScopeUser, opts)

// after — writes threshold: 0.9
config.SetValue("threshold", config.ParseScalar(raw), config.ScopeUser, opts)
```

`Set` is unchanged and remains correct for values that are strings.
Already-written files keep their quoted values until the key is rewritten.

## Hot reload

`Reloadable[T]` wraps a typed config snapshot in an `atomic.Pointer[T]`
and exposes `Snapshot() *T` for lock-free reads. `Reload(newOpts)`
re-runs `Load`, partitions the struct into mutable / immutable fields,
refuses to apply changes to immutable fields, and atomically swaps the
held pointer on success.

```go
var cfg AppConfig
if err := config.Load(&cfg, opts); err != nil { return err }
r := config.New(&cfg, opts, config.WithReloadPublisher(pub))

// Readers use Snapshot(); never cache *AppConfig directly.
endpoint := r.Snapshot().Endpoint

// SIGHUP-driven reload (typical production wiring):
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
go r.WatchSignal(ctx, syscall.SIGHUP)
```

### Mutable vs immutable: the `reload:"true"` tag

A field is mutable across reloads only when explicitly opted in:

```go
type AppConfig struct {
    ListenAddr string `yaml:"listen_addr"`               // immutable
    Endpoint   string `yaml:"endpoint" reload:"true"`    // mutable
}
```

The default-immutable bias is intentional: hot-reloading an unknown
field surface is more dangerous than refusing to reload one. Reload
returns `*ErrImmutableChanged` (without swapping the snapshot) when any
non-tagged field differs between the held and freshly-loaded snapshots.
A struct field tagged `reload:"true"` short-circuits — every nested
field beneath it is treated as mutable.

Embedded structs traverse recursively. Anonymous embeds with no yaml
tag inline at the parent level (matching yaml's "inline" behavior).

Maps and slices are leaves: a single tag governs the whole value, and
diffing uses `reflect.DeepEqual`. Per-element opt-in is out of scope.

### Atomic snapshot swap

Readers calling `Snapshot()` are never blocked by an in-flight reload.
They observe either the pre-reload pointer or the post-reload pointer —
never a partial state. Reload calls are serialised through an internal
mutex.

Consumers must treat each returned `*T` as immutable: a future reload
replaces it with a new pointer rather than mutating the live snapshot.
A typical reader takes `r.Snapshot()` once per operation.

### Bus events

Reload outcomes publish on two topics (when a publisher is attached via
`WithReloadPublisher`):

| Topic                                  | Payload                | When                              |
|----------------------------------------|------------------------|-----------------------------------|
| `kit.config.snapshot.reloaded`         | `ReloadedPayload`      | snapshot swapped successfully     |
| `kit.config.snapshot.reload_failed`    | `ReloadFailedPayload`  | immutable veto OR Load failure    |

`ReloadFailedPayload.Reason` distinguishes `immutable_changed` from
`load_error`. Both payloads include the ordered `SourcePaths` Load
considered (system, user, project, then ExtraConfigPaths) so
subscribers can attribute the change.

Topics may be overridden per adopter via `WithReloadTopics` or
`WithReloadTopicPrefix`. The default prefix `kit.config.snapshot`
satisfies `bus.ValidateTopic`.

### Signal watcher

`WatchSignal(ctx, sigs...)` blocks the calling goroutine, calling
`Reload(currentOpts)` on every signal. Errors from Reload are dropped
on purpose — the bus failure event is the operator-facing channel. The
signal set is caller-supplied: production wiring uses `syscall.SIGHUP`,
tests use `SIGUSR1` / `SIGUSR2`.

See ADR-0016 for the design context behind these choices.

## Related pages

- [`go/core/config/README.md`](../../../go/core/config/README.md): package README
- [`go/core/config/pkl/README.md`](../../../go/core/config/pkl/README.md): PKL schema, wizard, completion and validation
- [inspect-config-paths.md](../guides/inspect-config-paths.md): which file wins, and why
- [xdg.md](xdg.md): where config, data, cache and state files resolve
- [go-primitives.md](go-primitives.md#i-need-configuration-and-paths): configuration and paths index
