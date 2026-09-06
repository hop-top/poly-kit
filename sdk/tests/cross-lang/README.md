# Cross-language contract harnesses

This directory hosts three independent harnesses over the same runners tree:

| Harness | Entry point | Pins |
|---------|-------------|------|
| Telemetry envelope | `./run.sh` | Envelope shape + redactor placeholders across py/ts/rs/php |
| Column ordering | `./run-order.sh` | The five column-order rules across go/py/ts/rs/php |
| Compliance verdict | `./run-compliance.sh` | Score, denominator + per-factor status (F13 included) across go/ts/py |

They share `fixtures/`, `expected/`, and `runners/` but have separate
fixture files, comparison paths, and orchestrators. See
[Column-ordering conformance](#column-ordering-conformance-harness) and
[Compliance conformance](#compliance-conformance-harness) below for the
other two.

---

# Telemetry contract harness

Drives each SDK's `record()` path against a shared deterministic fixture,
captures the per-language JSONL output, and diffs each envelope against
`expected/envelope.json` after normalising out per-run / per-language
volatile fields.

## What this proves

Four polyglot SDKs (py / ts / rs / php) all promise the same envelope
shape and the same deterministic redactor placeholders
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
  consent.yaml      # granted decision pre-seeded (kit.telemetry.consent
                    # canonical shape, copied to <XDG_CONFIG_HOME>/kit/config.yaml)
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
| ts   | `node`, `sdk/ts/dist/telemetry/index.js` (run `npm run build`)  |
| rs   | `cargo`                                                                   |
| php  | `php`, `sdk/experimental/php/vendor/autoload.php` (composer)    |

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

These appear in the diff output today; they are documented here so the
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

---

# Column-ordering conformance harness

`./run-order.sh` pins the five settled column-ordering rules across **all
five** runtimes — Go included, since Go is the reference implementation the
payload SDKs were aligned to.

```sh
./run-order.sh            # every detected runtime
./run-order.sh py ts      # subset
```

## The five rules

1. **Default order.** A caller-supplied ColumnSpec list drives column order
   and headers, in list order. Payload key order is the fallback used **only**
   when no ColumnSpec was supplied (Go: struct declaration order).
2. **`--cols` precedence.** `--cols` reorders as well as selects — the user's
   sequence wins, on the schema path and the fallback path alike.
3. **`header == key`, universally.** Validation and value lookup are the same
   operation on the same name. No SDK may carry a capability another runtime
   cannot mirror, and Go cannot express a header/key split via `table:""`.
4. **Empty payload.** Zero rows emits nothing — not even a bare header row.
   Emptiness is decided by ROW count, never header count.
5. **`priority`.** Accepted, stored, ignored by the payload SDKs; implemented
   as hide-on-overflow in Go only.

## What it compares, and why not bytes

Rendered bytes are **not** comparable across runtimes and never were: rs
renders tables through comfy-table (which pads cells) while py/ts/php use a
tabwriter shape, and php's YAML puts the sequence dash on its own line where
py/ts/rs inline it. None of that is contractual.

What *is* contractual is column **order**. So each runner:

1. renders the shared fixture through its own SDK,
2. **re-parses the bytes it just emitted** with an order-preserving reader,
3. reports the observed column sequence as an ordered array.

`compare_order.py` then diffs those observations against
`expected/ordering.json`. Re-parsing the runtime's own output is what makes
this a genuine observation of serialized key order rather than a restatement
of the input.

### The canonicalisation trap

`run.sh`'s `normalise_jsonl` canonicalises envelopes with
`json.dump(..., sort_keys=True)`. That is correct for the telemetry contract,
where key order carries no meaning — and **fatal here**, because key order is
the entire subject. Sorting would make every runtime look identical and the
suite would assert nothing.

`run-order.sh` therefore does **not** reuse that path. Sequences are compared
element-by-element and are never sorted. Membership checks and
order-insensitive deep-equality are deliberately absent: rs's original test
compared `Value` to `Value` (which ignores key order) and was inert against
the very bug it appeared to cover; `toEqual` on JS objects has the same
hazard. Neither is repeated.

## Layout

```
fixtures/ordering.json    # cases: spec, rows, --cols, formats
expected/ordering.json    # expected sequences + known parity gaps
compare_order.py          # ordered comparison, never sorts
run-order.sh              # orchestrator
runners/go/order/         # reference runtime
runners/py/order.py
runners/ts/order.cjs      # needs `pnpm build` in sdk/ts/
runners/rs/src/order.rs   # second binary in the shared runner crate
runners/php/order.php
```

## Prerequisites

| Lang | Requires |
|------|----------|
| go   | `go` |
| py   | `python3>=3.11` + pyyaml (`uv sync` in `sdk/py/`) |
| ts   | `node` + a **current** `sdk/ts/dist/output.js` (`pnpm build` in `sdk/ts/`) |
| rs   | `cargo` |
| php  | `php` + `vendor/autoload.php` (`composer install` in `sdk/experimental/php/`) |

The ts precondition rejects a *stale* bundle as well as a missing one: it
checks that `resolveEffectiveCols` and `columnName` are actually exported, so
a bundle predating the ordering work skips loudly instead of failing
confusingly.

Skips do not fail the harness, but a skipped runtime is reported as **skipped,
never as passed** — an unrun runtime is not a green runtime, and silent skips
are how this class of bug survived unnoticed in the first place.

## Format coverage is not uniform

| runtime | table | json | yaml | csv | text |
|---------|-------|------|------|-----|------|
| go      | yes   | yes  | yes  | yes | yes  |
| py      | yes   | yes  | yes  | yes | yes  |
| ts      | yes   | yes  | yes  | yes | yes  |
| rs      | yes   | yes  | yes  | —   | —    |
| php     | yes   | yes  | yes  | —   | —    |

Cases are tagged `portable` (table/json/yaml — every runtime) or `extended`
(csv/text — py/ts/go). rs and php report `unsupported` for the extended cases,
which surfaces as a known gap rather than a pass.

## What Go is excluded from, and why

Go participates in every ordering case a `table:""` struct can express, which
is all of them. Two carve-outs are contract clauses, not oversights:

- **`header != key` is not expressible in Go at all.** A `table:""` tag
  supplies the header while the lookup comes from the struct field, so there
  is no split to reject. That inexpressibility is precisely *why* rule 3
  exists, so Go satisfies it by construction and the runner reports `n/a`
  rather than a pass. The payload SDKs are the ones under test.
- **"ColumnSpec order differs from payload key order" collapses in Go.** A
  struct has exactly one field order, so the two notions coincide. The case
  still runs; passing means Go agrees with the order the payload SDKs derive
  from their ColumnSpec.

## Known parity gaps (surfaced — NOT fixed)

Recorded in `expected/ordering.json` under `known_gaps`. The harness prints
them and does not fail on them.

- **Go never reorders on `--cols`** (`go-cols-never-reorders`). Go *selects*
  the requested columns but emits them in struct-field declaration order
  regardless of the requested sequence, in every format. `filterColumns`
  (`go/console/output/projection.go:13-17`) says so in its own doc comment;
  `structToMap` (`:111`) does the same for json/yaml. This contradicts rule 2,
  which settled 4-to-1 with Go the sole outlier and noted "Only Go changes" —
  the Go-side change landed in none of the four payload-SDK tasks, so this is
  the unimplemented half of rule 2 rather than a new defect.
- **Go loses json/yaml key order** (`go-json-yaml-key-order`). `projectToMaps`
  builds a `map[string]any`, so `encoding/json` and `yaml.v3` emit keys
  alphabetically. Go's rule-1/rule-2 guarantee is table/csv/text-only. Go also
  switches key *names* from the `json:`/`yaml:` tags to the `table:` headers
  once `cols` is non-empty.
- **rs and php ship no csv/text formatter** (`rs-php-no-csv-text`). Both
  document these as a follow-up phase.
- **`priority` is Go-only** (`priority-go-only`). Complete hide-on-overflow in
  Go (`renderer.go:301-364`); accepted-and-ignored in the payload SDKs per
  rule 5. No fixture asserts it — pinning it would pin a capability four
  runtimes cannot mirror. Go's hiding is width-driven and `terminalWidth()`
  falls back to 200 columns off-TTY, so it does not perturb these fixtures.
- **py's formatter shape diverges structurally** (`py-formatter-shape`). py
  kept an optional 5th `columns` parameter with signature-gating, while
  rs/php/ts resolve precedence in dispatch and leave `render` at 4 params.
  Behaviorally identical — every ordering case passes on py with the same
  expected sequence as the 4-param runtimes, which is exactly what these
  fixtures are for.
- **Table bytes are not comparable** (`table-bytes-not-comparable`). See
  "What it compares, and why not bytes" above.

## Adding a case

1. Append to `cases` in `fixtures/ordering.json` (`spec`, `rows`, `cols`,
   `formats`, `go`).
2. Add the expected sequences to `expected/ordering.json`.
3. Run `./run-order.sh`. Every runner reads the fixture generically, so no
   runner needs editing unless the case needs a new row shape.

When adding an ordering case, make the expected order disagree with
alphabetical order *and* with declaration order where possible — an
expectation that happens to match either can pass for the wrong reason.

---

# Compliance conformance harness

`./run-compliance.sh` pins the **compliance verdict** across the three ports
that ship a compliance checker — Go, TypeScript, Python. Where the other two
harnesses cover the telemetry envelope and column order, this one covers what
the 12-factor checker actually decides about a toolspec.

```sh
./run-compliance.sh            # every detected runtime
./run-compliance.sh go py      # subset
```

## What it proves

The fixture (`fixtures/compliance.toolspec.yaml`) **opts into telemetry**
(`telemetry.enabled: true`), so a conforming port must run its F13
"Consenting Telemetry" check and report a denominator of **13**. A port that
never implemented F13 reports 12 and fails here even when all twelve of its
other factors agree — the denominator is checked on its own line precisely
because it is the half a factor-only comparison would miss.

Three fields are compared, and nothing else:

| Field | Why |
|-------|-----|
| `total` | The 12/13 denominator — opt-in must widen it |
| `score` | Count of passing factors |
| `factors` | Every factor's status, keyed by factor number |

Factor *names* are checked too, since the rendered report prints them.

## What it does not compare, and why not bytes

Report bytes are not comparable across ports and never were: Go marshals a
struct, TS an interface, Python a dataclass, and each carries its own elision
rules for empty `details` / `suggestion` fields. None of that is contractual.

Result **order** is also not the subject — `factors` is compared as a mapping
keyed by factor number, not as a list. The sibling ordering harness is where
sequence is the contract; conflating the two would make this suite fail on
cosmetic reordering while still passing a genuine verdict divergence.

Only the **static** pass runs (empty binary path). Runtime checks execute a
binary that no two ports could agree on, and F13 is a static check in every
port regardless.

## Layout

```
fixtures/compliance.toolspec.yaml   # opt-in toolspec, well-formed telemetry block
expected/compliance.json            # score, total, per-factor status + names
compare_compliance.py               # verdict comparison
run-compliance.sh                   # orchestrator
runners/go/compliance/              # reference runtime (F13 landed here first)
runners/py/compliance.py            # imports the in-tree SDK, not an installed copy
runners/ts/compliance.cjs           # consumes a bundle built from working-tree source
```

## Prerequisites

| Lang | Requires |
|------|----------|
| go   | `go` |
| ts   | `node` + `sdk/ts/node_modules` (`pnpm install` in `sdk/ts/`) |
| py   | `python3>=3.11` + pyyaml (`uv sync` in `sdk/py/`) |

The TS runner does **not** read `dist/`. `compliance` is not in the package
exports map and tsup does not build it, so there is no bundle to consume: the
orchestrator bundles `src/compliance.ts` with the SDK's own esbuild into a
temp file. That removes the stale-artifact hazard the ordering harness has to
guard against explicitly — this harness always tests the working tree.

Skips do not fail the harness, but a skipped runtime is reported as
**skipped, never as passed**.

## Changing the fixture

Every field in `fixtures/compliance.toolspec.yaml` is load-bearing for the
expected score — the twelve pre-F13 checks land on a deliberate mix of pass
and skip. If you change it, re-derive `expected/compliance.json` from the Go
runner (the reference implementation) and confirm ts and py still agree,
rather than editing the expected file to match whatever came out.
