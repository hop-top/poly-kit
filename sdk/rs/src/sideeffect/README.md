# sideeffect

## What it answers

How one seam serves both a `--dry-run` preview and test mocking for the four categories that cover essentially all
kit CLI mutations: `Fs`, `Http`, `Bus`, `Exec`. Where an output value came from is not this module's job, nor is
whether a path may be touched at all.

## Use it when

- inject the seam → take `&dyn sideeffect::Fs` / `Http` / `Bus` / `Exec`
- run for real → `real::Fs`, `real::Http::new()?`, `real::Bus::new(adapter)`, `real::Exec`
- preview instead of acting → `dryrun::Fs::new().with_writer(w)`, `dryrun::Http::new(inner)`, `dryrun::Bus`, `dryrun::Exec`
- assert calls in tests → `testfake::Fs::new()`, `.allow(pred)`, `calls()`, `assert_called`, `assert_not_called`
- branch on the flag from library code → `is_dry_run(&ctx)`, `with_dry_run(&ctx, true)`
- tag published events → `DryRunPublisher::new(Some(inner))`

## Quick start

```rust
use std::sync::Arc;
use hop_top_kit::bus::Context;
use hop_top_kit::sideeffect::{dryrun, is_dry_run, real, with_dry_run, Fs};

fn pick_fs(ctx: &Context) -> Arc<dyn Fs> {
    if is_dry_run(ctx) {
        Arc::new(dryrun::Fs::new())
    } else {
        Arc::new(real::Fs)
    }
}

let ctx = with_dry_run(&Context::background(), true);
pick_fs(&ctx).write_file("/tmp/x".as_ref(), b"hi", 0o644).unwrap(); // prints, writes nothing
```

## Contract

- Features: `sideeffect` is `bus` plus nothing of its own, wasm-safe: the traits, the dry-run flag, `dryrun`,
  `testfake`, `DryRunPublisher`. `sideeffect-real` adds `real::{Fs, Bus, Exec}` (native only);
  `sideeffect-http` adds `real::Http` through the netpolicy guarded client (`api`), so `--offline` refuses it
  ([`HttpError::is_offline`]); an allowlist refusal is [`HttpError::is_host_not_allowed`]. Authority: the crate [feature table](../../README.md#features).
- Go is canonical for the seams, the dryrun text and the policy of what is intercepted. Differences: `Http` is
  async over plain `Request` / `Response` data, `Exec` takes a `Command`, payloads are `serde_json::Value`s, and the
  real bus publishes on a kit bus `Adapter`.
- Reads (`std::fs::read`, metadata, GET, HEAD) pass through to std unchanged; dry-run does not pretend reads are unsafe.
- `dryrun::Http` forwards GET, HEAD and the empty method to the client it wraps; every other verb prints
  `[dry-run] would <VERB> <url> (<n>-byte body)` and answers a synthetic 201 with a `{}` body. The printed URL drops userinfo and fragment and replaces every query value with `REDACTED` (keys, host, port and path kept); Go prints it as given.
- The dry-run `Bus` tags `mechanism=dry_run` only on payloads that carry qualifiers: qualifier keys at the top level
  (a `#[serde(flatten)]` `Qualifiers`) or a nested `qualifiers` object. An all-empty flattened `Qualifiers` serialises
  to nothing and is not detected; use the nested form or set another qualifier. An existing non-empty `mechanism`
  is kept. Payloads are owned values, so tagging is guaranteed, unlike Go's pointer-only reflection.
- `testfake` accepts every call unless an `allow` predicate is registered and none match; an unexpected call
  panics (Go calls `t.Fatalf`) and is not recorded. Queued answers (`expect*`) are consumed in order, then the default
  applies: success, a 200 with `{}` for HTTP, empty bytes for `output`.
- Dry-run guarantees no real writes, mutating HTTP calls or subprocesses **routed through these seams**. It does not
  chase spawned children, make compound state correct behind synthetic responses, or see a call site that bypasses them.
- Not ported yet: wiring the `--dry-run` flag and the per-command policy tiers. They follow the CLI root port; until
  then a caller installs the flag with `with_dry_run` and picks the implementation itself.

## Neighbours

- `hop_top_kit::bus`: the `Context` the flag travels on, the `Qualifiers` slot the tag occupies, the `Adapter` the real bus publishes on
- `hop_top_kit::netpolicy`: the guard behind `real::Http`

## See also

- [Side-effect reference](../../../../docs/adopters/reference/sideeffect.md): the Go design, flag mechanics and policy tiers
