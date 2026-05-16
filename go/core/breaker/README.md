# breaker

Runtime circuit breakers for kit-based tools. Bounds the runtime
blast radius of any operation — file writes, exec spawns, HTTP
calls, token spend — that path policy alone (`kit/scope`) cannot
catch.

Where `kit/scope` answers *where can I touch?*, `kit/breaker`
answers *how much, how fast, how often before I stop?*.

Thin wrapper over [failsafe-go][failsafe] (MIT). The two policies
failsafe doesn't ship — `MaxBytes` (Volume) and `MaxOps` (Count) —
are implemented natively under [`policy/`](policy/).

[failsafe]: https://github.com/failsafe-go/failsafe-go

## When to use which policy

| Symptom / use case | Policy / option | Notes |
|--------------------|-----------------|-------|
| Bound calls per minute / hour | `MaxPerMinute(n)` / `MaxPerInterval(n, d)` | Bursty (period-windowed), not Smooth — see ADR |
| Bound concurrent in-flight ops | `MaxConcurrent(n)` | Failsafe Bulkhead (counting semaphore) |
| Per-call deadline | `Timeout(d)` | Only meaningful for `Executor().Run/Get`, not `Allow` |
| Cumulative bytes cap (downloads, log writes, S3 egress) | `MaxBytes(n)` | Kit-side Volume policy; reads via `Record(_, n)` |
| Cumulative op cap (file writes, LLM calls, exec spawns) | `MaxOps(n)` | Kit-side Count policy; reads via `Record(_, _)` |
| Trip after N consecutive failures | `WithCircuit(CircuitOpts{FailureThreshold: N})` | Default 5; uses failsafe CircuitBreaker |
| Recover after delay | `ResetAfter(d)` or `WithCircuit{Delay: d}` | Default 30s; HalfOpen probe after delay |
| Route to alternate path on trip | `OnTrip(Degrade)` + `Fallback(fn)` | Without `Fallback`, Degrade silently behaves like Halt |
| Log-only (migration mode) | `OnTrip(Warn)` | Discouraged outside migration / known soft-failure modes |
| Custom kit/log destination | `Logger(l)` | Accepts `*charm.land/log/v2.Logger`; defaults to `kitlog.New(viper.GetViper())` |

## API surface

| Concern | Symbol |
|---------|--------|
| Construct + register | `breaker.New(name string, opts ...Option) Breaker` |
| Check before doing work | `b.Allow() error` (returns `ErrBrokenCircuit` if open) |
| Update counters + state machine | `b.Record(success bool, n int64)` |
| Manually open | `b.Trip(reason string)` |
| Manually close + zero counters | `b.Reset()` |
| Read state | `b.State()` (`Closed` / `Open` / `HalfOpen`) |
| Read stats | `b.Stats()` (`Trips`, `LastTripAt`, `LastTripReason`, `Counters`) |
| Drop down to failsafe | `b.Executor() failsafe.Executor[any]` |
| Look up by name | `breaker.Lookup(name)` |
| All breakers (sorted) | `breaker.List()` |
| Reset everything | `breaker.ResetAll()` |
| Read everything | `breaker.Snapshot()` |
| Test cleanup | `breaker.Unregister(name)` (pair with `t.Cleanup`) |
| Load YAML | `breaker.FromConfig(tool)` / `breaker.MustFromConfig(tool)` |
| Build one from a parsed map | `breaker.Apply(name, cfg map[string]any)` |

Wrap helpers (in `wrap.go`):

| Helper | Use case |
|--------|----------|
| `Wrap(b, fn) func() error` | Hook / handler passed around |
| `WrapErr(b, fn) error` | Single-shot, no closure alloc |
| `WrapValue[T](b, fn) (T, error)` | Generic value return |
| `WrapCtx(b, ctx, fn) error` | Context-aware single shot |
| `WrapBytes(b, fn) ([]byte, error)` | Records `n=len(out)` for `MaxBytes` |
| `WrapWriter(b, w) io.Writer` | Per-Write Allow + Record |
| `WrapReader(b, r) io.Reader` | Per-Read Allow + Record (EOF is success) |
| `WrapHTTP(b, rt) http.RoundTripper` | Per-RoundTrip Allow + Record `Content-Length` |

## Examples

### File writes — cap bytes + ops

Pseudocode (replace `mytool` with real package):

```go
b := breaker.New("file-writes",
    breaker.MaxOps(10_000),                 // 10k file writes total
    breaker.MaxBytes(1<<30),                // 1 GiB total
    breaker.MaxPerMinute(100),              // burst-cap
    breaker.OnTrip(breaker.Halt),
)

w := breaker.WrapWriter(b, f)               // f is the underlying *os.File
n, err := w.Write(buf)                      // Allow + Record automatic
```

### Exec spawns — concurrency + rate cap

```go
b := breaker.New("exec-spawns",
    breaker.MaxConcurrent(4),
    breaker.MaxPerMinute(30),
)

err := breaker.WrapCtx(b, ctx, func(ctx context.Context) error {
    return exec.CommandContext(ctx, "convert", args...).Run()
})
```

### LLM calls — cost cap + degrade

```go
b := breaker.New("llm-calls",
    breaker.MaxOps(500),                    // hard cap on calls
    breaker.MaxBytes(2_000_000),            // ~500k tokens (~4 bytes each)
    breaker.MaxConcurrent(4),
    breaker.Timeout(30*time.Second),
    breaker.OnTrip(breaker.Degrade),
    breaker.Fallback(func(ctx context.Context) error {
        return useCachedResponse(ctx)
    }),
)

resp, err := breaker.WrapValue(b, func() (string, error) {
    return llm.Complete(ctx, prompt)
})
```

### HTTP client — wrap a RoundTripper

```go
b := breaker.New("github-api",
    breaker.MaxPerMinute(60),               // GitHub unauthenticated cap
    breaker.WithCircuit(breaker.CircuitOpts{
        FailureThreshold: 5,
        Delay:            60 * time.Second,
    }),
)

client := &http.Client{Transport: breaker.WrapHTTP(b, http.DefaultTransport)}
```

## Bus topics

Lifecycle events are emitted on the kit bus when (and only
when) `WithPublisher` is wired. Without it the breaker still
logs via kit/log but publishes nothing.

| Topic                              | When |
|------------------------------------|------|
| `kit.core.breaker.tripped`         | manual `Trip(reason)` |
| `kit.core.breaker.opened`          | auto transition → Open |
| `kit.core.breaker.closed`          | auto transition → Closed |
| `kit.core.breaker.half_opened`     | auto transition → HalfOpen |

Override the prefix to namespace events under your application:

```go
b := breaker.New("api",
    breaker.WithPublisher(pub),
    breaker.WithTopicPrefix("myapp.core.breaker"),
)
// emits: myapp.core.breaker.{tripped,opened,closed,half_opened}
```

`WithTopics` overrides individual actions.

## breaker.yaml schema

Tools and ops declare breakers in YAML. Loaded via
`breaker.FromConfig("mytool")` from `~/.config/<tool>/breaker.yaml`
(per-user) merged on top of `/etc/xdg/<tool>/breaker.yaml`
(system-wide).

```yaml
breakers:
  file-writes:
    on_trip: halt              # halt | degrade | warn
    max_per_minute: 100
    max_bytes: 1073741824      # 1 GiB
    max_ops: 10000
    reset_after: 5m
  exec-spawns:
    on_trip: warn
    max_per_minute: 30
    max_concurrent: 4
  llm-calls:
    on_trip: degrade
    max_concurrent: 4
    timeout: 30s
    circuit:
      failure_threshold: 5
      success_threshold: 2
      delay: 30s
```

| Key | Maps to | Notes |
|-----|---------|-------|
| `on_trip` | `OnTrip(Halt|Degrade|Warn)` | Default `halt` |
| `max_per_minute` | `MaxPerMinute(n)` | Bursty rate limiter |
| `max_concurrent` | `MaxConcurrent(n)` | Bulkhead |
| `timeout` | `Timeout(d)` | Go duration string (`30s`, `2m`) |
| `max_bytes` | `MaxBytes(n)` | Kit Volume policy |
| `max_ops` | `MaxOps(n)` | Kit Count policy |
| `reset_after` | `ResetAfter(d)` | Circuit breaker delay |
| `circuit.failure_threshold` | `WithCircuit{FailureThreshold}` | Default 5 |
| `circuit.success_threshold` | `WithCircuit{SuccessThreshold}` | Default 1 |
| `circuit.delay` | `WithCircuit{Delay}` | Default 30s |

Unknown keys are an error (typo guard).

## CLI examples

`kit breaker` — inspect runtime fuses without a debugger.

```sh
kit breaker list                   # name, state, trips, last reason
kit breaker list --format json     # machine-parseable
kit breaker show file-writes       # full Stats + per-counter values
kit breaker reset file-writes      # close one breaker
kit breaker reset --all --yes      # close all (audit log)
```

Exit codes: `0` ok, `1` not-found, `2` usage error.

## Composing with kit/scope

`kit/scope` and `kit/breaker` are deliberately independent — neither
imports the other. Tools touching the FS should consult both:

```go
// scope says "is this path allowed?"
if err := scope.Default().Enforce(path, scope.Write); err != nil {
    return err
}

// breaker says "have I exhausted my budget?"
return breaker.WrapCtx(b, ctx, func(ctx context.Context) error {
    return os.WriteFile(path, data, 0o644)
})
```

The two answer different questions. Use scope to bound *what*; use
breaker to bound *how much*. A new fs-touching primitive is
expected to integrate with both — see [`AGENTS.md` Guardrails][ag].

[ag]: ../../../AGENTS.md#guardrails

## Why we wrap failsafe-go

- failsafe-go covers ~70% of what kit/breaker needs (CircuitBreaker,
  RateLimiter, Bulkhead, Timeout, Retry, Fallback, Hedge,
  AdaptiveLimiter, AdaptiveThrottler) with battle-tested
  implementations.
- MIT-licensed, ~2.2k stars, active. Single transitive surface
  (`golang.org/x/sync`, `golang.org/x/time` — stdlib-tier).
- Hand-rolling these would be ~10× more code with worse-tested
  algorithms.
- The two missing policies (Volume, Count) are cumulative-counter
  semantics that don't fit failsafe's per-call model — kit
  implements them natively in [`policy/`](policy/).

Full rationale + alternatives considered (sony/gobreaker v2,
cep21/circuit, afex/hystrix-go) live in ADR-0006.

## Limitations

- **Single process.** `Lookup` / `List` / `ResetAll` only see
  breakers registered in *this* process. Cross-process introspection
  needs IPC or a shared store — out of scope. Tracked for a future
  `kit-breaker-cluster`.
- **In-memory state.** Counters and trip history are lost on
  process exit. `Reset()` also zeroes counters by design (operator
  intervention = clean slate). A future flag could preserve them.
- **Polyglot ports reimplement.** failsafe is Go-only. TS + Python
  ports (`kit-breaker-polyglot`) ship native implementations that
  load the same `breaker.yaml`. Parity contract:
  `contracts/parity/breaker-schema.json`.
- **No `kit breaker watch` subcommand yet.** Use `watch -n 1 'kit
  breaker list'` in a shell. A bubbletea-driven live tail is
  follow-up.
- **`Allow()` runs a noop fn through the executor.** Per-call cost
  is one failsafe executor pass (~50–100 ns + kit wrapper
  overhead). Acceptable for any op that does real I/O downstream.
