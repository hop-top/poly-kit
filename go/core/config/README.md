# config

hierarchical configuration loading and validation.

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
