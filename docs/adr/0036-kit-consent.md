# ADR-0036: kit-consent — defaults, precedence, prompt_version, decision source

- Status: Accepted
- Date: 2026-05-19
- Track: kit-consent
- Related: [ADR-0035](./0035-kit-telemetry.md) (canonical ConsentHook
  interface, persisted-file path, event schema)

## Context

kit ships an optional telemetry pipeline (kit-telemetry, ADR-0035). That
pipeline refuses to emit unless a consent hook returns `granted`. The
hook is owned by kit-telemetry; this ADR pins how kit-consent — the
human-facing half — answers the hook, persists the decision, and
exposes the decision to the user.

Concrete questions this ADR answers:

1. What is the default state, and why?
2. Why is `DO_NOT_TRACK=1` non-overridable, even by an explicit
   `--telemetry=on`?
3. What does `prompt_version` mean, and when does it change?
4. What is `decision_source`, and why do we record it?
5. What is the full precedence chain for resolving consent at runtime?

This sits downstream of ADR-0035, which fixes the wire-level contract
(interface shape, persisted-file path under `<config>/telemetry/`,
event schema). kit-consent must not contradict any of those choices;
where ambiguity exists, ADR-0035 wins.

## Decision

### 1. Default is `denied`

Silence is denied. Non-TTY is denied. An absent persisted decision is
denied.

Rationale:

- **GDPR / PECR posture**: consent must be affirmative, specific, and
  informed. Defaulting to `granted` and waiting for the user to opt out
  fails all three prongs. Defaulting to `denied` and prompting the user
  on the first interactive run produces an affirmative, specific (the
  prompt names the categories collected), informed (link to the docs)
  decision.
- **Non-TTY cannot consent**: a process attached to a pipe or a CI
  runner has no human at the keyboard. We cannot show a prompt, and a
  prompt the user never saw cannot produce affirmative consent. Auto-
  deny is the only defensible behavior.
- **Reversibility is cheap**: a user who wants telemetry on after the
  fact runs `kit telemetry enable` once. The opposite mistake (telemetry
  on by default, user discovers it later) is expensive to walk back —
  reputational damage already done, events already shipped.

### 2. `DO_NOT_TRACK=1` is non-overridable

`DO_NOT_TRACK=1` returns `denied` even when paired with an explicit
`--telemetry=on` flag on the same invocation.

Rationale:

- **Industry convention**: consoledonottrack.com, Homebrew, gh, and a
  growing list of CLI tools honor `DO_NOT_TRACK` as a global opt-out.
  Users who set it expect uniform behavior across their tooling.
  kit-telemetry making `--telemetry=on` win would mean every kit
  invocation is a potential leak from a user's perspective.
- **Defensibility**: if a user complains "I set `DO_NOT_TRACK=1` and kit
  still phoned home", the only defensible answer is "we honor it
  absolutely". Any qualifier ("we honor it unless...") collapses the
  defense.
- **Predictability over flexibility**: the user who wants telemetry on
  for a specific run despite `DO_NOT_TRACK=1` has a clear path: `unset
  DO_NOT_TRACK && kit ...`. The user who set `DO_NOT_TRACK=1` expecting
  silence does not have a comparable escape hatch if we let flags win.

### 3. `prompt_version` semantics

`prompt_version` is an integer stamped onto every persisted decision.
It records "the version of the disclosure copy the user actually saw
when they made this decision".

Rules:

- Bump `prompt_version` when the prompt copy materially changes:
  - a new data category is collected (e.g. adding `os_version`),
  - a new sink or endpoint receives the data,
  - the redact contract loosens (e.g. retaining a previously-stripped
    field).
- Do NOT bump for cosmetic edits (typo fixes, reflow, color tweaks).
  Those do not change what the user is consenting to.
- On the next interactive (TTY) run after a bump: re-prompt. The
  prior state is shown as the highlighted default ("you previously
  said granted; the disclosure changed — please re-confirm").
- On non-TTY after a bump: persist a fresh `denied` decision stamped
  at the current `PromptVersion` with `decision_source = config`. We
  cannot prompt, and we cannot keep emitting under a stale "granted"
  the user has not seen the new disclosure for — so the safe posture
  is `denied`. We DO bump the persisted `prompt_version` forward so
  the file truthfully reflects "this decision is against the current
  disclosure copy, and we couldn't re-prompt".

  Rationale for bumping forward rather than preserving the prior
  prompt_version verbatim:
  - A persisted decision pinned to a stale prompt_version is
    informationally lossy. Downstream consumers (`kit telemetry
    status`, support tooling) cannot distinguish "user explicitly
    decided against the current copy" from "user decided against an
    older copy and we never re-prompted". Bumping the stored
    `prompt_version` while flipping `decision_source` to `config`
    makes the distinction trivially queryable: `source=prompt` means
    the user saw THIS copy; `source=config` against the current
    `PromptVersion` means the user was not asked at this version.
  - On the next TTY run, the resolver compares `persisted.prompt_version
    == PromptVersion` — bumping forward keeps that comparison sound.
    If the user enables on that TTY run (via prompt or flag), the
    persisted decision already carries the correct prompt_version
    baseline against which future bumps will trigger re-prompts.
  - Preserving a stale prompt_version on non-TTY would cause every
    subsequent non-TTY run to re-execute the same "stale" branch
    indefinitely, repeatedly rewriting an identical file with the
    same stale value. Bumping forward makes the operation idempotent
    after the first non-TTY rewrite.

This is the single mechanism by which a copy change forces re-consent.
Bumping it is a small but meaningful event — it should be visible in
changelogs and reviewed at the same level of care as the prompt copy
itself.

### 4. Decision-source taxonomy

Every persisted decision carries a `decision_source` field with one of
four values:

| value    | meaning                                                                  |
|----------|--------------------------------------------------------------------------|
| `prompt` | User answered the interactive TTY prompt.                                |
| `flag`   | User passed `--telemetry=on\|off` (or `enable` / `disable` subcommand).  |
| `env`    | `KIT_TELEMETRY_CONSENT` set in the environment at decision time.         |
| `config` | Default applied (e.g. non-TTY auto-deny, or seeded config).              |

Why record it:

- **Audit**: `kit telemetry status` shows the user (and support staff)
  exactly how the current decision was reached. "Why is my telemetry
  off?" has a one-line answer.
- **UX**: error messages can explain themselves. When `kit telemetry
  enable` refuses because `DO_NOT_TRACK=1` is set, the error references
  the env source: "denied because DO_NOT_TRACK=1 (env)".
- **Migration safety**: if we ever need to invalidate a class of
  decisions (e.g. "all `config`-sourced denials predating prompt
  version 3 should be re-prompted"), the source field makes the cohort
  queryable. Without it, we'd have to invalidate everyone.

`decision_source` and the persisted `state` together form the
`consent.Decision` value object. That value object is consumed
internally by `kit telemetry status` and `kit telemetry inspect`. It
does NOT cross the emitter boundary: kit-telemetry's `ConsentHook`
(see ADR-0035) returns a plain `bool`, derived from `state == granted`.

### 5. Precedence chain (final form)

```
1. KIT_TELEMETRY_MODE=off (or <APP>_TELEMETRY_MODE=off) -> denied
   (short-circuit; app-prefix env var checked BEFORE kit-prefix)
2. DO_NOT_TRACK=1                                       -> denied
   (NON-OVERRIDABLE; see section 2)
3. --telemetry=on|off                                   -> granted | denied
4. --yes (paired with --telemetry=on)                   -> granted
5. KIT_TELEMETRY_CONSENT=granted|denied                 -> granted | denied
6. persisted config decision                            -> as stored
7. default                                              -> denied
```

Notes:

- Step 1 is a hard short-circuit: if mode is `off`, no other input is
  inspected. The mode env var has two valid names: `<APP>_TELEMETRY_MODE`
  (the embedding application's prefix — e.g. `CMDSURF_TELEMETRY_MODE`)
  and `KIT_TELEMETRY_MODE`. The app-prefix wins where both are set, so
  an embedding binary can override kit's default at runtime without
  needing to unset the kit-level var.
- Step 5 (`KIT_TELEMETRY_CONSENT`) accepts only the values `granted` and
  `denied`. Legacy / alternative forms (`allow`, `deny`, `1`, `0`,
  `true`, `false`) are rejected with a diagnostic. This keeps the env
  vocabulary identical to the persisted `state:` vocabulary and the
  canonical schema from ADR-0035; one set of strings everywhere.
- Step 6 is where most steady-state invocations land: the user
  consented (or didn't) once, the decision persists, every subsequent
  invocation just reads it.
- Step 7 is the cold-start branch: brand-new install, never prompted,
  non-TTY. Default denied.

## Consequences

Positive:

- A user who sets `DO_NOT_TRACK=1` once gets uniform behavior across
  every kit-based tool, with no per-tool configuration.
- The `decision_source` field makes "why am I in this state" answerable
  in one read, both for users and support.
- The `prompt_version` knob gives us a calibrated way to force re-
  consent when we change what we collect, without re-prompting on every
  invocation.
- The precedence chain has no implicit fallbacks: every position is
  enumerated, every input is consulted in a documented order.

Negative:

- Default-denied costs us telemetry volume from users who would have
  said yes if asked. We accept that cost: it is the price of an
  affirmative-consent posture, and the loss is bounded (users see the
  prompt on first interactive run).
- `DO_NOT_TRACK` being non-overridable closes one escape hatch — a user
  who wants telemetry on for a single run while `DO_NOT_TRACK=1` is set
  must unset the env var first. We treat this as an acceptable trade
  against the alternative (carving exceptions weakens the convention).
- `prompt_version` bumps will re-prompt users who already consented.
  Done sparingly this is the feature; done liberally it becomes noise.
  Mitigation: changelog entry per bump, and a documented bump policy
  (this ADR) reviewers can hold us to.

Neutral:

- Persisted state is the long-lived input; flags and env are per-
  invocation overrides. Both layers exist by design, and the precedence
  chain reconciles them.

## Alternatives considered

**Default-granted with opt-out**. Rejected: fails affirmative-consent
prongs of GDPR/PECR; reputationally expensive to walk back. The
hypothetical volume gain does not survive the legal and trust costs.

**`DO_NOT_TRACK` as a soft default that flags can override**. Rejected:
breaks the industry convention users rely on; collapses the defensive
position; offers a flexibility no actual user has asked for.

**No `decision_source` field, infer from state alone**. Rejected:
loses the ability to explain "why am I denied" or scope a migration to
a specific cohort. The field is one int + one enum; the storage cost
is trivial.

**`prompt_version` as a string ("v2.1.0") rather than int**. Rejected:
the comparison we need is "stored < current"; integers make that a
direct compare. Tying it to a semver string couples us to release
cadence in a way we don't want.

**Per-category consent (granular flags for command, exit, version,
OS/arch)**. Rejected for v1: dramatically more prompt surface and
storage complexity for a use case we have no evidence users want. The
disclosure already names every category collected; users who want
selective control can opt out wholesale. We can revisit if real demand
appears.

## References

- ADR-0035 — kit-telemetry: canonical `ConsentHook.Granted(ctx) bool`
  interface, persisted-file path, event schema. This ADR is downstream
  of those decisions.
- Track plan: `.tlc/tracks/kit-consent/plan.md` (frontmatter task
  descriptions reflect this ADR's contracts). Pinning tasks
  T-0663..T-0670 cover the resolver, prompt, status/enable/disable/
  reset/inspect verbs, and this ADR (T-0673). The stale-prompt-version
  non-TTY behavior documented in §3 was reconciled against the
  shipped implementation in `cmd/kit/telemetry/prompt.go` (T-0665).
- consoledonottrack.com — industry convention reference for
  `DO_NOT_TRACK`.
- GDPR Art. 4(11), Art. 7; PECR reg. 6 — affirmative-consent
  requirements.
