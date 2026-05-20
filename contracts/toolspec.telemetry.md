# toolspec `telemetry:` block

Authoritative reference for the `telemetry:` block of a kit toolspec.

## Purpose

Declares a binary's opt-in to kit's consent-gated telemetry stack.
Presence of the block plus `enabled: true` opts the binary into
compliance Factor #13 (`ConsentingTelemetry`). Absence (or
`enabled: false`) is the safe default — the static check skips and
the existing 12-factor regression suite stays green.

## Schema

```yaml
telemetry:
  enabled: true
  categories: [invocation, error]
  sinks: [bus, jsonl]
  consent_command: "<bin> telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, "<APP>_TELEMETRY_MODE"]
  prompt_version: "v1"
  redact_rules: kit-default
```

## Field reference

- `enabled` (bool) — `true` opts the binary into Factor #13 static +
  runtime checks. `false` keeps the block as documentation while
  static check returns `skip`.
- `categories` ([]string) — event categories the binary emits.
  Canonical values: `invocation`, `error`, `lifecycle`. Non-empty
  when `enabled: true`.
- `sinks` ([]string) — emitter sinks. Canonical values: `bus`,
  `jsonl`.
- `consent_command` (string) — top-level subcommand path that owns
  the consent subtree (e.g. `"spaced telemetry"`).
- `consent_subcommands` ([]string) — must enumerate
  `{status, enable, disable, reset, inspect}`; each maps to a
  command in the toolspec tree.
- `kill_switch_envs` ([]string) — env names that suppress emission.
  Must include `DO_NOT_TRACK` and at least one mode env
  (`<APP>_TELEMETRY_MODE` or `KIT_TELEMETRY_MODE`).
- `prompt_version` (string) — version tag for the first-run prompt;
  load-bearing field name shared with the persisted consent
  decision. Aliases like `consent_version` are rejected.
- `redact_rules` (string) — ruleset id. `kit-default` references the
  canonical ruleset shipped by `kit-telemetry`; custom paths
  permitted (runtime check verifies the ruleset fired).

## Canonical example

```yaml
telemetry:
  enabled: true
  categories: [invocation, error]
  sinks: [bus, jsonl]
  consent_command: "spaced telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, SPACED_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
```

