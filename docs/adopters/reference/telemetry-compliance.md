# Telemetry Compliance (F13 — `ConsentingTelemetry`)

> **Adopter checklist for kit-powered binaries that opt into telemetry.**
> If your tool sets `telemetry.enabled: true` in its toolspec, every item
> on this page MUST be satisfied to pass `kit compliance check`. Skip the
> block (or leave `enabled: false`) and F13 returns `skip` — your binary
> still scores `12/12` on the original twelve factors.

This page is reference material for engineers wiring telemetry into a
kit-powered CLI. For end-user docs on what telemetry collects and how to
opt out, see [`adopters/guides/telemetry.md`](../guides/telemetry.md).

## 1. Purpose

`kit compliance check` enforces a 12-factor adopter contract on any
kit-powered binary. Factor #13 — `ConsentingTelemetry` — is the
opt-in 13th factor. It surfaces seven discrete sub-conditions a binary
must satisfy before it earns the right to ship a single telemetry
event. The factor exists because "loggable" (Factor #9 ObservableOps,
operator-facing) and "consenting" (user-facing trust contract) are
different concerns with different actors. Conflating them would let a
binary pass ObservableOps while silently violating the user-consent
contract — so F13 is its own row with its own seven Suggestions.

## 2. The seven sub-conditions

Each row below maps to one assertion `kit compliance check` runs. Most
have both a static arm (the toolspec must declare X) and a runtime arm
(the binary must behave like Y when invoked).

### a. Telemetry block well-formed

- **Static**: `toolspec.telemetry` exists with non-empty `categories`.
- **Runtime**: none.
- **Common failures**: omitted `categories`, typo'd field names
  (`telemetry_enabled` instead of `enabled`).
- **Fix**: copy the canonical block in §3 verbatim.

### b. Consent subcommands declared + mapped

- **Static**: `consent_subcommands` lists exactly
  `{status, enable, disable, reset, inspect}`. Each name resolves to a
  command in the binary's cobra tree under `consent_command`.
- **Runtime**: each subcommand exits 0 and emits structured output.
- **Common failures**: missing `inspect` (the audit subcommand), or a
  declared subcommand that returns "command not found".
- **Fix**: wire `kit/go/runtime/telemetry.RegisterConsentSubcommands(root)`
  — it installs all five for you.

### c. `DO_NOT_TRACK=1` suppresses emission

- **Static**: `DO_NOT_TRACK` is listed in `kill_switch_envs`.
- **Runtime**: with `DO_NOT_TRACK=1` set, run a command that would
  normally emit; assert zero events on the bus within 500 ms.
- **Common failures**: env var declared but the emitter doesn't read it.
- **Fix**: this is free if you emit via `telemetry.Emitter` —
  `consent.Resolve` honors `DO_NOT_TRACK` non-overridably. Don't
  hand-roll the check.

### d. `<APP>_TELEMETRY_MODE=off` (or `KIT_TELEMETRY_MODE=off`) suppresses emission

- **Static**: `kill_switch_envs` lists EITHER an app-prefixed env
  (`SPACED_TELEMETRY_MODE`, `MYAPP_TELEMETRY_MODE`) OR the canonical
  `KIT_TELEMETRY_MODE`.
- **Runtime**: same shape as (c). Compliance toggles whichever env
  the spec declares.
- **Common failures**: declaring neither; declaring `<APP>_TELEMETRY`
  (no `_MODE` suffix).
- **Fix**: same as (c) — use `consent.Resolve` and the precedence chain
  handles it.

### e. First-run prompt fires-or-skips per precedence

- **Static**: none.
- **Runtime**: compliance runs three scenarios in a fresh `rtEnv`:
  TTY with no persisted decision (must prompt), non-TTY with no
  decision (must default-deny silently), TTY with persisted decision
  (must NOT prompt).
- **Common failures**: prompting on non-TTY (breaks CI), failing to
  prompt on TTY (silent default), prompting twice in one session.
- **Fix**: wire `consent.Prompt(ctx)` at root cobra startup; the helper
  short-circuits non-TTY without prompting.

### f. Decision persisted with `prompt_version` field

- **Static**: `prompt_version` set in spec (the field name is locked;
  `consent_version` and aliases are rejected).
- **Runtime**: inspect the persisted config under a temp
  `XDG_CONFIG_HOME` after enable/disable; assert the JSON contains
  `prompt_version` (not an alias).
- **Common failures**: persisting `version: 1` (wrong field name);
  forgetting to bump `prompt_version` when the prompt copy changes
  materially.
- **Fix**: use `consent.FileStore` — it writes the canonical shape.
  Bump `prompt_version` only when you change what telemetry collects.

### g. `inspect` returns post-redact payload + audit topic fires

- **Static**: `redact_rules` set in spec (`kit-default` or a path to a
  ruleset file).
- **Runtime**: inject a PII-laden event, run the inspect subcommand,
  assert raw PII is absent AND redact placeholders are present AND
  the `kit.telemetry.redact.matched` bus topic fired. The audit-topic
  assertion is load-bearing — a no-op redactor would pass
  "raw PII absent" vacuously.
- **Common failures**: rolling a custom inspect that reads the
  pre-redact spool; redactor not actually wired into the emit path.
- **Fix**: use `telemetry.MustLoadRedactor()` + emit via
  `telemetry.Emitter.Record`. The kit-default ruleset publishes the
  audit topic automatically.

## 3. Canonical `toolspec.telemetry` block

```yaml
telemetry:
  enabled: true
  categories: [invocation, error]
  sinks: [bus, jsonl]
  consent_command: "<bin> telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, MYAPP_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
```

Field-by-field semantics:

| field | semantics | canonical value |
|-------|-----------|-----------------|
| `enabled` | If `false` (or omitted) F13 returns `skip`. | `true` once you're ready to ship |
| `categories` | Event classes the emitter can publish. Compliance accepts any non-empty list. | `[invocation, error]` |
| `sinks` | Where events flow. `bus` = in-process pub/sub; `jsonl` = on-disk spool. | `[bus, jsonl]` |
| `consent_command` | Root verb for the five consent subcommands. | `"<bin> telemetry"` |
| `consent_subcommands` | Must be exactly these five names. | `[status, enable, disable, reset, inspect]` |
| `kill_switch_envs` | Must include `DO_NOT_TRACK` AND at least one mode env. | `[DO_NOT_TRACK, MYAPP_TELEMETRY_MODE]` |
| `prompt_version` | Locked field name. Bumped only when collection changes materially. | `"v1"` |
| `redact_rules` | `kit-default` or a path to a custom ruleset. | `kit-default` |

## 4. Implementation checklist

The adopter checklist boils down to **"use the kit-\* packages as
designed, declare the toolspec block, you pass."** Per sub-condition:

a. **Declare the toolspec block.** Copy §3 verbatim into
   `<bin>.toolspec.yaml`.

b. **Wire consent subcommands.** In `main.go`:

   ```go
   telemetry.SetAppPrefix("myapp")
   telemetry.SetConsentHook(consent.NewHook(store))
   ```

   The helper installs all five consent subcommands under your root
   cobra command.

c. **Honor `DO_NOT_TRACK`.** Free with `telemetry.Emitter` +
   `consent.Resolve`. Don't hand-roll the kill-switch check — every
   hand-rolled implementation has shipped a regression.

d. **Honor `<APP>_TELEMETRY_MODE=off`.** Same as (c).

e. **Wire the first-run prompt.** Call `consent.Prompt(ctx)` at root
   cobra startup. Default-deny on non-TTY is built in.

f. **Persist the decision.** Use `consent.FileStore` — it writes the
   canonical shape with `prompt_version` at the right field name.

g. **Redact before egress.** Load via `telemetry.MustLoadRedactor()`
   and emit via `telemetry.Emitter.Record`. The kit-default ruleset
   publishes `kit.telemetry.redact.matched` to the bus on every match,
   which is what the runtime check subscribes to.

If you used the `kit init` scaffold or copied `examples/spaced/`, all
of the above is wired by default — flipping `enabled: true` in the
toolspec is the only step left.

## 5. Running `kit compliance check` against your binary

```
kit compliance check ./mybinary --toolspec mybinary.toolspec.yaml
```

Output reports pass/fail per factor. When F13 fails, each Suggestion
names the specific sub-condition and the canonical fix:

```
✗ ConsentingTelemetry
  Suggestion: kill_switch_envs is missing DO_NOT_TRACK.
              Add DO_NOT_TRACK to kill_switch_envs in mybinary.toolspec.yaml.
              See: docs/adopters/reference/telemetry-compliance.md §2(c).
```

The exit code is non-zero when any factor fails. Skipped factors do
not contribute to the failure count.

## 6. Adopting incrementally

Not ready to ship telemetry? Leave `telemetry.enabled: false` (or omit
the block entirely). F13 returns `skip` and `Report.Total` derives from
`len(results)`, so your binary scores **12/12** on the original twelve
factors. Existing tests stay green; you can add the telemetry block
later without rewriting any compliance scaffolding.

Mid-migration: it's fine to set `enabled: true` and fail F13 in CI
while you wire the kit-\* packages. F13 surfaces actionable Suggestions
per sub-condition — fix them in any order. Once the seven checks pass,
your binary scores **13/13**.

## 7. Build-time configuration (kit options)

Telemetry has two configuration tiers. The end-user surface
(`kit telemetry enable|disable|reset`, `KIT_TELEMETRY_*` env vars,
the persisted consent decision in `<XDG_CONFIG_HOME>/kit/config.yaml`)
is documented in [`adopters/guides/telemetry.md`](../guides/telemetry.md).
This section covers the adopter surface: kit options baked into
the binary at build time, immutable to the operator.

### Why a separate tier

Some decisions are properly the adopter's, not the operator's:

| Decision | Who owns it | Why |
|---|---|---|
| Collector URL | Adopter | Operator must not be able to point a production binary at an attacker-controlled collector |
| Whether kit may prompt for consent | Adopter | Some binaries have their own first-run wizard; others ship in CI-only contexts where prompting is wrong |
| Default emission tier on grant | Adopter | The redact config and the appetite for argv/flag values are properties of the binary, not the user |

Baking them into the binary keeps them out of `--help`, out of
`kit telemetry status`, and out of any user-writable config layer.

### The kit option

```go
// adopter's main.go
root := cli.New(cli.Config{Name: "spaced", /* ... */},
    cli.WithTelemetry(cli.TelemetryConfig{
        // Endpoint: usually left empty — see "Endpoint via ldflag" below.
        PromptOnFirstRun:   true,
        DefaultModeOnGrant: runtimetelemetry.ModeAnon,
    }),
)
```

`cli.TelemetryConfig` fields:

- **`Endpoint string`** — the HTTPS collector URL. Optional. When
  empty, callers fall back to `runtimetelemetry.ResolveEndpoint`,
  which honors the env override and the ldflag-injected default.
  Set this only when you have a wire-time reason to override both.
- **`PromptOnFirstRun bool`** — when `false` (default), kit NEVER
  fires its first-run consent prompt. Operators must opt in via
  env (`KIT_TELEMETRY_CONSENT=granted`) or by invoking `kit
  telemetry enable` themselves. Set `true` only when the binary
  intends the prompt to fire from a known interactive surface.
- **`DefaultModeOnGrant runtimetelemetry.Mode`** — the emission
  tier kit assumes when consent is granted but no explicit mode
  is set. Zero value (`ModeOff`) keeps the binary silent even
  after a grant — operators must additionally set
  `KIT_TELEMETRY_MODE=anon|full` to start emission. Most adopters
  want `ModeAnon`; set `ModeFull` only when your redact config can
  demonstrably handle argv/flag values without leaking secrets.

### Endpoint via ldflag (recommended)

The production collector URL belongs in a CI secret, not in a Go
source file. Bake it at link time:

```sh
go build \
  -ldflags="-X 'hop.top/kit/go/runtime/telemetry.DefaultEndpoint=$URL'" \
  -o spaced ./cmd/spaced
```

GitHub Actions sketch (in the adopter's `.github/workflows/release.yml`):

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - name: Build with baked endpoint
        env:
          KIT_TELEMETRY_ENDPOINT: ${{ secrets.TELEMETRY_ENDPOINT }}
        run: |
          go build \
            -ldflags="-X 'hop.top/kit/go/runtime/telemetry.DefaultEndpoint=${KIT_TELEMETRY_ENDPOINT}'" \
            -o spaced ./cmd/spaced
```

Resolution precedence at emit time (highest wins):

1. `KIT_TELEMETRY_ENDPOINT` env var — operator override
2. `cli.TelemetryConfig.Endpoint` — adopter wire-time override
3. `runtimetelemetry.DefaultEndpoint` — ldflag-injected build default
4. `""` — no endpoint configured; the jsonl sink stays the safe default

Dev builds without the ldflag leave `DefaultEndpoint` empty, so
local `go build` produces a binary that never tries to ship — exactly
the safe default. The same binary, rebuilt by CI with the ldflag,
ships to the configured collector. No source-file literal, no leak
into `go env`.

## 8. Cross-links

- [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md) —
  engineer-level depth: emitter API, sink internals, redact
  observation, polyglot SDK contract.
- [`adopters/guides/telemetry.md`](../guides/telemetry.md) — end-user
  docs: what we collect, how to opt out, how to audit, how to reset.
