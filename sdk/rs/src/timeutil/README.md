# timeutil

## What it answers

How are human time expressions such as `since 2h` or `until friday` resolved
into absolute instants the same way in every port? Durations for service
timeouts are parsed by `hop_top_kit::serve` config.

## Use it when

- a `--until` flag accepts `tomorrow`, `in 3 days`, `+3d`, `friday`, `next monday` → `parse_until(s)`
- a `--since` flag accepts `yesterday`, `3 days ago`, `3d`, `last week` → `parse_since(s)`
- a test needs a fixed reference instant → the `*_at(s, now)` variants
- an absolute date arrives as ISO 8601 or `1 May 2026` → either parser accepts it

## Quick start

```rust
use hop_top_kit::timeutil::{parse_since_at, parse_until_at, TimeError};
use jiff::Timestamp;

let now: Timestamp = "2026-04-19T12:00:00Z".parse().unwrap();

let until = parse_until_at("in 3 days", now).unwrap();
assert_eq!(until, "2026-04-22T12:00:00Z".parse().unwrap());

let since = parse_since_at("2h", now).unwrap();
assert_eq!(since, "2026-04-19T10:00:00Z".parse().unwrap());

assert_eq!(parse_until_at("in 0 days", now), Err(TimeError::InvalidCount("0".into())));
```

## Contract

- Feature `timeutil` pulls in `jiff`, `interim`, `thiserror`. Instants are `jiff::Timestamp`. Authority: the crate
  [feature table](../../README.md#features).
- Short units are case-sensitive: `m` is minutes, `M` is months.
- Counts must be positive; `in 0 days`, `+0d` and `-3 days ago` are errors.
- A bare weekday means the next occurrence, so today's weekday resolves seven days out.
- Month arithmetic clamps: `2026-01-31` plus one month is `2026-02-28`, never March.
- Deterministic forms are parsed natively; only `next monday`, `last week` and month-name dates
  fall through to `interim` in the US dialect.
- Range: instants are `jiff::Timestamp`, so years -9999 to 9999 (the last day of 9999 excluded);
  anything past it is `TimeError::OutOfRange` or `Unrecognized`, never a panic.
- Parity: none recorded; Go `go/core/util` (`until.go`, `since.go`) is the reference, and
  `tests/timeutil.rs` ports its tables case for case.

## Neighbours

- `hop_top_kit::serve` (src/serve/): `ready_timeout` and friends are durations, not calendar expressions
- `hop_top_kit::cli` (src/cli.rs): where a `--since` / `--until` flag is mounted before its value reaches here

## See also

- [`go/core/util/until.go`](../../../../go/core/util/until.go) and
  [`since.go`](../../../../go/core/util/since.go), the Go references
