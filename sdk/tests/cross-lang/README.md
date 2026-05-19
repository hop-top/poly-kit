# Cross-language telemetry contract harness (T-0709)

Drives each SDK's `record()` path against a shared deterministic fixture,
captures the per-language JSONL output, and diffs each envelope against
`expected/envelope.json` after normalising out per-run / per-language
volatile fields.

## What this proves

ADR-0038 ships four polyglot SDKs (py / ts / rs / php) that all promise
the same envelope shape and the same deterministic redactor placeholders
(`<redacted:email>`, `<redacted:ipv4>`, `<redacted:ipv6>`,
`<redacted:token>`, `$HOME`). This harness is the byte-level proof: if
two SDKs disagree on a placeholder, a key name, a redaction depth, or a
mode-dependent payload strip, the harness fails with a unified diff.

It does NOT cover:

- Sink durability (rotation, HTTPS retry shape).
- Async / queue behaviour under load.
- The HTTPS sink wire format (we use the jsonl sink for determinism).
- Anon-mode payload strip (the test asserts the redactor; redactor only
  runs in Full mode).

## Layout

```
fixtures/
  install_id.bytes  # 32 raw bytes pre-seeded for every SDK
  consent.yaml      # granted decision pre-seeded
  input.json        # deterministic event + attrs with PII shapes
expected/
  envelope.json     # post-redaction envelope (sans volatile fields)
runners/
  py/record.py      # python3 runner
  ts/record.cjs     # node runner (consumes the built CJS bundle)
  rs/...            # standalone cargo project depending on the SDK by path
  php/record.php    # php runner
run.sh              # orchestrator — temp dir, env, dispatch, diff
```

## Run it

From this directory:

```sh
./run.sh            # every detected language
./run.sh py ts      # subset
```

Per-language skips when prerequisites are missing:

| Lang | Requires                                                                  |
|------|---------------------------------------------------------------------------|
| py   | `python3`, `pyyaml` (auto-skipped otherwise)                              |
| ts   | `node`, `hops/main/sdk/ts/dist/telemetry/index.js` (run `npm run build`)  |
| rs   | `cargo`                                                                   |
| php  | `php`, `hops/main/sdk/experimental/php/vendor/autoload.php` (composer)    |

The harness exits 0 when every language that ran passed; skips do NOT
fail. CI runs with all four toolchains installed.

## Normalisation

The orchestrator strips these fields before diffing because they vary
per-run or per-language and are NOT part of the contract this harness
asserts:

- `occurred_at` (varies per run)
- `sdk_lang` (varies per language — `"py"`, `"ts"`, `"rs"`)
- `sdk_version` (each SDK ships its own crate / npm / pypi version)
- PHP-only aliases: `ts` (→ occurred_at), `sdk` (→ sdk_lang)

`install_id` (PHP) is renamed to `installation_id` (canonical) so the
key-level diff is meaningful.

## Known parity gaps (surfaced — NOT fixed by this task)

These appear in the diff output today; T-0709 documents them so the
follow-up parity work can target the exact discrepancies:

- **PHP omits `schema_version`** from its envelope entirely (every other
  SDK ships `schema_version: "1"`).
- **PHP envelope keys diverge**: `event`, `ts`, `install_id`, `mode`,
  `sdk`, `attrs` vs the py/ts/rs canonical `schema_version`, `sdk_lang`,
  `sdk_version`, `installation_id`, `mode`, `occurred_at`, `event`,
  `attrs`. The orchestrator normalises a subset of these for diff
  purposes; the gaps remain visible in the raw JSONL under the temp
  dir.
- **PHP IPv6 regex uses a different shape** (lookbehind-based) than the
  py/ts/rs `\b...\b` boundary. Edge cases may diverge; the fixture's
  fully-expanded IPv6 hits both.

These are real cross-lang bugs. Fixes belong in the SDK source — out of
scope for the harness itself.

## Adding a new language

1. Create `runners/<lang>/...` that:
   - Reads `XDG_STATE_HOME`, `XDG_CONFIG_HOME`, `KIT_TELEMETRY_SINK`,
     `KIT_TELEMETRY_SINK_FILE` from the env (orchestrator sets these).
   - Reads `fixtures/input.json`.
   - Calls the SDK's `record()` + `shutdown()` / `flush()`.
   - Exits 0.
2. Add a `check_<lang>` precondition function to `run.sh`.
3. Add `<lang>` to `SUPPORTED_LANGS`.
4. Wire the dispatch case in `run_lang()`.
