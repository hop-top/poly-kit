# Telemetry

> **TL;DR.** kit ships an optional anonymous-usage telemetry pipeline. It is
> **off by default**. Nothing is sent until you grant consent. The only
> command that ever transmits anything is one you explicitly enabled. To
> turn it off forever: `kit telemetry disable`, or set `DO_NOT_TRACK=1`
> in your environment.

This page is for users and operators of kit-based CLIs (`kit` itself,
`spaced`, and any tool built on the kit framework). It covers what is
collected, how to opt in or out, how to audit shipped events, and how to
reset your anonymous identity.

If you're building a kit-based CLI and want to wire telemetry into your
own tool, see the engineer-level
[`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md)
instead.

## What we collect

Telemetry has three modes — `off`, `anon`, and `full`. Default is `off`.

### `anon` mode

When telemetry is enabled at the `anon` tier, each command emits one
event with these fields:

- **Command path** — e.g. `["kit", "init"]`, `["spaced", "launch"]`.
- **Exit code** — integer.
- **Duration** — wall-clock milliseconds.
- **Timestamp** — RFC 3339 UTC (`occurred_at`).
- **kit / app version** — the build version string.
- **SDK lang + version** — `"go"` for kit, `"py"` / `"ts"` / `"rs"` /
  `"php"` for sibling SDKs.
- **Anonymous installation_id** — a 64-character lowercase hex string,
  derived as `SHA-256(32 random bytes)`. The on-disk seed is **never**
  transmitted; only its hash flows over the wire. The seed lives at
  `<XDG_STATE_HOME>/kit/telemetry/installation_id` with mode `0600`.
  Rotating it severs any link to prior events.

### `full` mode

`full` mode adds, on top of the `anon` payload:

- **Command arguments** — after redact rules run.
- **Flag values** — after redact rules run. Flag **keys** are preserved
  verbatim; only **values** pass through redact.

`full` is opt-in beyond `anon`: enabling it requires a redact
configuration. Without redact rules the adopter binary refuses to start
the emitter.

### What we never collect — at any tier

- File paths (other than what survives redact when included as an arg).
- Environment variable values.
- File contents.
- Network destinations of unrelated traffic.
- Personal identifiers — email, name, account, hostname, username.
- stdout or stderr output of any command.

`stdout` and `stderr` are stripped by the emitter and have no way of
reaching the wire format.

> Surface attribution (CLI vs. HTTP vs. Lambda invocation surface)
> currently ships in `full` mode only; broader surface tagging is
> tracked separately.

## How to opt in or out

There are three layers of control, ordered by how broadly they apply.

### Per-invocation (one-off)

The adopter binary (e.g. `spaced`) exposes a `--telemetry` persistent
flag that overrides the persisted decision for **this command only**:

```
spaced --telemetry=off  launch     # disable for this invocation
spaced --telemetry=anon launch     # enable anon-mode for this invocation
spaced --telemetry=full launch     # enable full-mode (requires redact config)
```

This does not change persisted state. The next invocation falls back to
your stored consent.

### Persistent (the standard flow)

```
kit telemetry enable     # grants consent; persists state=granted
kit telemetry disable    # denies consent; persists state=denied
kit telemetry status     # show resolved state, identity, mode
```

`enable` and `disable` write a decision under
`<XDG_CONFIG_HOME>/kit/config.yaml` (mode `0600`, parent dir
`0700`). That file is the kit AppConfig; the persisted decision
lives under the `kit.telemetry.consent` partition and reads like:

```yaml
kit:
  telemetry:
    consent:
      state: granted              # granted | denied
      decided_at: "2026-05-19T20:58:14Z"
      prompt_version: 1
      decision_source: flag       # prompt | flag | env | config
```

Sibling top-level partitions in the same file (other than
`kit.telemetry.consent`) survive `enable` / `disable` / `reset`
untouched — adopter-owned config can co-exist.

> **Where do events ship to?** The collector URL, whether kit
> prompts on first run, and the default emission tier are
> **adopter-owned** decisions baked into the binary at build time.
> Operators cannot change them without a rebuild — that's by
> design. See [`telemetry-compliance.md` §7](../reference/telemetry-compliance.md#7-build-time-configuration-kit-options)
> for the build-time configuration tier.

A pre-refactor layout that stored the same shape under
`<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent`)
is read as a fallback; the next `enable` / `disable` migrates the
decision into `config.yaml`. The legacy file is left in place so any
hand-added sibling keys survive.

### Environment variables (CI, scripted, ephemeral)

```
DO_NOT_TRACK=1                # industry standard — non-overridable
KIT_TELEMETRY_MODE=off        # short-circuits to denied regardless of consent
SPACED_TELEMETRY_MODE=off     # app-prefixed; wins over KIT_TELEMETRY_MODE
KIT_TELEMETRY_CONSENT=granted # or "denied" — explicit consent override
```

`<APP>_TELEMETRY_MODE` (e.g. `SPACED_TELEMETRY_MODE`) is checked **before**
`KIT_TELEMETRY_MODE` so an embedding binary can override kit's default
without forcing users to unset both.

### Precedence chain

Resolved top-down on every invocation:

```
1. <APP>_TELEMETRY_MODE=off  or  KIT_TELEMETRY_MODE=off   -> denied
   (hard short-circuit; app-prefix env var checked first)
2. DO_NOT_TRACK=1                                          -> denied
   (NON-OVERRIDABLE)
3. --telemetry=on|off                                      -> granted | denied
4. --yes paired with --telemetry=on                        -> granted
5. KIT_TELEMETRY_CONSENT=granted|denied                    -> granted | denied
6. persisted consent decision                              -> as stored
7. default                                                 -> denied
```

`DO_NOT_TRACK=1` is **non-overridable**. Even an explicit
`--telemetry=on` on the same command line loses to it. Industry
convention — see [consoledonottrack.com](https://consoledonottrack.com)
— and we honor it absolutely. If you want telemetry on for a single
run while `DO_NOT_TRACK=1` is set, you must `unset DO_NOT_TRACK` first.

## How to audit shipped events

```
kit telemetry inspect             # show the next 10 spooled events (post-redact)
kit telemetry inspect --last 50   # show up to 50 spooled events
kit telemetry inspect --next 25   # synonym for --last in v1
```

Output is JSONL — one event per line — and the events shown are
**exactly** what would be (or was) shipped to the upstream collector.
The pre-redact payload is never reachable; redact runs before anything
hits the spool.

An empty spool prints an informational message:

```
No spooled telemetry events.
```

An empty spool is good news. It means either:

- Telemetry is disabled (no events were ever queued), or
- Every event flushed successfully and was cleared by the sink.

Spool files live at
`<XDG_STATE_HOME>/kit/telemetry/spool/YYYY-MM-DD.jsonl` and are subject
to a 16 MiB size cap; the oldest file is evicted first when the cap is
exceeded.

## How to reset your identity

```
kit telemetry reset            # prompts for confirmation
kit telemetry reset --yes      # skip confirmation
```

`reset` does two things atomically:

1. **Clears the persisted consent decision.** The next interactive
   (TTY) run re-prompts.
2. **Rotates the anonymous `installation_id`.** A new 32-byte seed is
   generated; the next emitted event carries a fresh
   `installation_id` with zero linkability to prior events.

Rotation is `os.Rename`-atomic — readers always see either the old or
the new identifier, never a partial write.

The kit-wide `--confirm` flag (`yes` / `no` / `auto` / `prompt`)
composes with the local `--yes` flag. Use whichever you prefer.

## Defaults and rationale

- **Default state is `denied`.** Silence is `denied`. Non-TTY is
  `denied`. Absent persisted decision is `denied`. This matches GDPR /
  PECR affirmative-consent requirements: consent must be opt-in,
  specific, and informed.
- **`DO_NOT_TRACK=1` is non-overridable.** Once set, no flag, no
  persisted state, and no env override grants telemetry. Industry
  convention. We honor it without qualification.
- **The first-run prompt fires only on TTY.** Scripted, piped, and CI
  invocations skip the prompt silently and write a `denied` decision
  with `decision_source=config`. Without a human at the keyboard, there
  can be no affirmative consent.
- **`prompt_version` bumps re-ask.** If we materially change what we
  collect — a new field, a new sink, a loosened redact contract — we
  bump the `prompt_version` constant, which forces a re-prompt on the
  next interactive run. The bumped version is visible in
  `kit telemetry status`. Cosmetic edits (typos, color, reflow) do
  **not** bump it.

## Inspecting state

`kit telemetry status` shows everything in one read:

```
$ kit telemetry status
consent:
  state: granted
  decided_at: 2026-05-19T09:14:22Z
  prompt_version: 1
  decision_source: prompt
identity:
  installation_id: 0e3b9a…f97c2
  path: ~/.local/state/kit/telemetry/installation_id
mode:
  current: anon
  app_prefix: spaced
```

`decision_source` answers "why am I in this state":

| value    | meaning                                                                |
|----------|------------------------------------------------------------------------|
| `prompt` | You answered the interactive TTY prompt.                               |
| `flag`   | You passed `--telemetry=on\|off` or ran `enable` / `disable`.          |
| `env`    | `KIT_TELEMETRY_CONSENT` was set in the environment.                    |
| `config` | A default was applied (non-TTY auto-deny, or seeded config).           |

`--format json` and `--format yaml` are available for scripting.

## See also

- [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md)
  — engineer-level depth: emitter API, sink internals, redact
  observation, polyglot SDK contract.

## References

- [consoledonottrack.com](https://consoledonottrack.com) — the
  `DO_NOT_TRACK` industry convention.
- GDPR Art. 4(11), Art. 7; PECR reg. 6 — affirmative-consent
  requirements.
