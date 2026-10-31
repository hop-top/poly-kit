# Utilities (`go/core/util`)

> Audience: Go adopters looking for the small helpers kit already
> ships before writing their own. Import `hop.top/kit/go/core/util`.
> The package is a grab-bag; this page groups the exported helpers and
> states each one's edge cases. Task index:
> [go-primitives](go-primitives.md).

## Retry with backoff

```go
err := util.Retry(ctx, util.RetryConfig{
    MaxAttempts: 5,                      // 0 = retry forever
    BaseDelay:   100 * time.Millisecond, // default 100ms
    MaxDelay:    30 * time.Second,       // default 30s
    Jitter:      true,
}, func() error { return call() })
```

| Symbol | Behavior |
|--------|----------|
| `Retry(ctx, cfg, fn)` | Calls `fn` until it returns nil. Returns the last `fn` error after `MaxAttempts`, or `ctx.Err()` if the context ends during a wait. |
| `RetryWithBackoff(ctx, fn)` | Unlimited attempts, 100ms base, 30s cap, jitter on. |
| `RetryConfig.Backoff(n)` | Delay before retry `n` (0-indexed): `BaseDelay * 2^n`, capped at `MaxDelay`; with `Jitter`, uniform in `[0, delay]`. |

- A zero `RetryConfig` has `Jitter` false. Only `RetryWithBackoff`
  turns it on for you.
- Every error is retried; there is no retryable/permanent split. Wrap
  `fn` and return nil to stop early on errors you will not retry.
- `fn` runs once before the first wait; the context is checked only
  while waiting.
- Backoff vectors are shared with the other language ports in
  [`contracts/retry-v1`](../../../contracts/retry-v1/README.md).

## Environment variables

Each reader returns the default when the variable is unset, empty, or
unparseable.

| Symbol | Notes |
|--------|-------|
| `EnvString(key, def)` | Empty counts as unset. |
| `EnvInt(key, def)` | `strconv.Atoi`. |
| `EnvBool(key, def)` | `1/true/yes/on` and `0/false/no/off`, case-insensitive; anything else gives `def`. |
| `EnvDuration(key, def)` | `time.ParseDuration` syntax (`30s`, `5m`). |

## Strings and hashes

| Symbol | Result |
|--------|--------|
| `Slug(s)` | Lowercase, `[a-z0-9-]` only, runs of other characters collapse to one hyphen, no leading or trailing hyphen. Non-ASCII is dropped, not transliterated: `"Hello, World! 2026"` gives `hello-world-2026`. |
| `Short(data, n)` / `ShortString(s, n)` | First `n` hex chars of SHA-256 (`n <= 0` means 8, capped at 64): `ShortString("hello", 8)` is `2cf24dba`. For compact stable labels, not for security. |

## Human-readable output

| Symbol | Behavior |
|--------|----------|
| `HumanDuration(d)` | One unit, truncated: `90m` renders `1h`; under 1s renders `0s`. Units `s m h d w mo y` (month = 30 days, year = 365). |
| `HumanBytes(n)` | Binary units, one decimal: `1536` renders `1.5 KB`; below 1024 renders `N B`. |
| `RelativeTime(t)` | `just now` (under a minute), `Nm ago`, `Nh ago`, `yesterday`, `Nd ago`; future times render `in <HumanDuration>`. Uses the current clock. |

## Time parsing

| Symbol | Direction | Accepts |
|--------|-----------|---------|
| `ParseSince(s)` / `ParseSinceAt(s, now)` | Backward | `yesterday`, `N unit(s) ago`, short `7d 24h 30m 2w 3M 1y` (`m` minutes, `M` months), natural phrases (`last week`), month names, ISO 8601 |
| `ParseUntil(s)` / `ParseUntilAt(s, now)` | Forward | `tomorrow`, `in N unit(s)`, short `+3d`, weekday names (next occurrence, never today), natural phrases (`next monday`), month names, ISO 8601 |

Pass `now` explicitly (`...At`) in tests. Month and year arithmetic
clamps to the last valid day instead of rolling over, matching the
Rust runtime: from 2026-01-31, `in 1 month` is 2026-02-28 and
`1 month ago` is 2025-12-31. An empty string is an error; counts must
be positive.

## Stored and displayed times

| Symbol | Behavior |
|--------|----------|
| `FormatStorageTime(t)` | UTC, RFC3339. Use for TEXT columns and wire formats so values sort lexicographically. |
| `ParseStorageTime(s)` | Accepts RFC3339 and RFC3339Nano, returns UTC. A failure means a corrupt row. |
| `LoadTimezone(name)` | `""` or `local` is `time.Local`, `UTC` is `time.UTC`, otherwise an IANA name; unknown names are an error to show the user. |
| `FormatInZone(t, loc, layout)` | `t.In(loc).Format(layout)`; nil `loc` is local. |
| `SameDayInZone(a, b, loc)` | Same calendar day in `loc` (nil is local); the right test for "today" buckets across UTC storage and display zones. |

## JSON Lines

```go
w := util.NewWriter(out)
_ = w.Write(Event{N: 1})        // one JSON object plus newline
_ = w.WriteRaw(rawBytes)        // pre-encoded line

err := util.Each(in, func(e Event) error { return handle(e) })
```

- `Reader.Read(&v)` / `Reader.ReadRaw()` return `io.EOF` at the end;
  `Each[T]` handles that and stops at the first error from your
  callback.
- A blank line is a decode error (`unexpected end of JSON input`), not
  skipped.
- The reader uses a `bufio.Scanner` with the default 64 KiB line
  limit; longer lines fail with `bufio.ErrTooLong`. The bytes from
  `ReadRaw` are valid until the next read; copy to keep them.
- `Writer` is not synchronized; guard it if several goroutines write.

## Pointers and must-wrappers

| Symbol | Behavior |
|--------|----------|
| `To(v)` | `&v` for any value, e.g. `util.To(5)`. |
| `Or(p, def)` | `*p`, or `def` when `p` is nil. |
| `Zero[T]()` | Pointer to the zero value. |
| `Do(v, err)` | Returns `v`, or panics with `must: <err>`. |
| `OK(err)` | Panics with `must: <err>` when non-nil. |

`Do` and `OK` are for init-time setup where failure is a programmer
error; do not use them on request paths.

## See also

- [go-primitives](go-primitives.md)
- [`go/core/util` package docs](../../../go/core/util/doc.go)
