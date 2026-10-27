# redact

## What it answers

What text may leave the process: secrets, tokens and PII stripped from log lines, error messages, agent prompts and payloads. A port of `go/core/redact`, held to the same vectors (`contracts/redact-v1`). Feature `redact`; no I/O, wasm-safe, `Redactor: Send + Sync`.

## Use it when

- redact with the default policy (gitleaks v8.30.1 + Presidio PII) → `redact::shared().apply(s)`
- configure your own copy (strategy, observers, extra rules) → `redact::embedded()?`, then `set_replacement`, `add_rule`, `on_match`
- scrub a value you hold (a client secret) → `add_rule(id, &regex::escape(secret), "")`
- build from data or dump it → `Redactor::from_policy(&p)`, `Redactor::policy()`, `redact::embedded_policy()`
- audit without replacing → `scan(s)` (gitleaks detection); the regions `apply` replaces → `spans(s)`
- read rule internals → `rules()`, `Rule::{id, pattern, regex, secret_group, entropy, keywords, tags, spec}`

## Quick start

```rust
use hop_top_kit::redact::{self, Replacement};

let mut r = redact::embedded()?;
r.set_replacement(Replacement::Tag);
let line = r.apply("aws_access_key_id = AKIA…"); // Cow: borrowed when clean
```

## Contract

- Single pass: spans from every rule on the original text, overlapping and adjacent ones merged (earliest start wins, then the longest, then the rule added first), replaced once. No rule sees another's placeholder.
- gitleaks rule semantics: keyword gate on the input lower-cased as Go does, secret group, inclusive entropy floor, global and per-rule allowlists.
- Two modes: `scan` is exact gitleaks detection; `apply` and `spans` fail closed (stopwords never exempt, a regex must match the whole target, and a distinctive rule, `Rule::distinctive()`, skips the keyword gate and entropy floor). Distinctive means every match starts with a literal of 3+ characters; the classification matches Go's for every default rule. Paths and commits never hold; path-scoped rules run.
- Patterns are Go RE2 and keep Go's meaning: `\d`, `\s`, `\w`, `\b` are ASCII, `\Q…\E` and a `{` that opens no repeat are literal (see `syntax.rs`). A pattern the `regex` crate cannot express fails `Rule::compile` or `from_policy` loudly; nothing is skipped.
- The default policy is `rules.json`, byte-identical to the contract's, exported by Go. Unlike Go there is no `KIT_REDACT_RULES_PATH` override; load another policy with `from_policy`.
- `Replacement::Custom` panics degrade to `Mask`. Observers see the secret: count it, never persist it.
- `apply` takes `&str` only; Go's `ApplyBytes` has no port. Stats carry no last-match time (no clock on wasm32).
- A 4KB line costs about 72µs and a short field about 1µs (release, M1 Pro): the keyword gate is one `RegexSet` pass, each rule's own literal search does the rest.

## Neighbours

- `go/core/redact`: the canonical implementation and corpus tooling.
- `contracts/redact-v1`: the shared vectors; runner `tests/redact_conformance.rs`.
- `telemetry/redact.rs`: the telemetry sink's fixed placeholder pass, a separate cross-SDK contract.
