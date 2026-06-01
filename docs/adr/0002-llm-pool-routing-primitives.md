# ADR 0002: LLM pool routing primitives

- **Status**: Accepted
- **Date**: 2026-05-31

## Context

Every kit-using tool that calls an LLM eventually has to answer the
same operational question: "given this request, which provider/model
should I send it to?" The decision threads four concerns at once —
capability (does the model do tool-calling? structured output?),
budget (cheap, balanced, premium), context window (will the prompt
fit?), and operator policy (which models are approved for this
deployment?). Today every adopter solves it ad hoc: a constant in
`tlc`, an env var dance in `foo`, a hand-maintained table elsewhere.

The shape of "ad hoc" is the problem. Each tool that re-derives this
logic:

- duplicates a model table that drifts the moment a vendor changes
  pricing or releases a new context-window tier;
- bakes ranking decisions into transport code so they can't be
  CI-tested in isolation;
- couples capability discovery (what does this model *do*?) to
  pricing intelligence (what does this model *cost*?), even though
  upstream sources (`models.dev`, vendor docs) already publish both
  together;
- gives operators no single place to declare "this deployment is
  allowed to use these models, full stop."

Upstream, `hop.top/aim` already owns the cross-cutting model
catalogue: it ships an XDG-cached, TTL-refreshed projection of the
`models.dev` JSON, with a polyglot consumer base (Go, TS, Python,
Rust, PHP). Recent upstream additions extended `aim.Model` with
`Cost *Cost` (USD per 1M input / output tokens), `StructuredOutput`,
and `Temperature` booleans; the matching `aim.Filter` tristate
`*bool` fields let consumers query for those capabilities directly.
That extension is the unlock — once pricing and capability flags
travel with the registry entry, a generic picker has everything it
needs without consulting any side table.

The remaining gap is routing logic on top of that catalogue. The
gap is uniform across adopters (`tlc`, `foo`, and the next four kit
consumers all need the same shape), so kit is the right home.

## Decision

Kit ships a small, deterministic set of LLM pool routing primitives
in `go/ai/llm/`, leaning on `hop.top/aim` for all model metadata.
The surface is intentionally narrow:

- A pluggable registry accessor (`llm.Default(ctx)`) so adopters
  share one `aim.Registry` and tests can inject mocks.
- A consumer-facing request descriptor (`RequestProfile{Filter,
  MaxInputTokens, MaxOutputTokens}`) plus a categorical
  `BudgetTier` enum (`BudgetCheap` / `BudgetBalanced` /
  `BudgetPremium`) parseable from CLI strings.
- A deterministic picker (`PickProvider`) with structured no-match
  errors and an env-gated `slog` trace.
- An optional operator-controlled pool (`Pool[]` in `llm.yaml` +
  `LLM_POOL_DISABLE` env + `ResolvePool` CLI hook) with a
  pool-aware picker variant (`PickProviderInPool`).

See `go/ai/llm/README.md` for the full usage examples; this ADR
captures *why* the surface looks the way it does.

### Why the primitives live in kit, not in consuming CLIs

Routing is cross-cutting. The same picker is used by `foo` today
and will be used by the next four tools that need an LLM call.
Splitting the picker across N CLIs means:

- N places to fix a ranking bug;
- N drifting interpretations of "what does balanced mean";
- N copies of the token-window filter, each with its own
  off-by-one;
- no shared baseline for CI tests that assert "given fixture
  registry X and budget Y, the chosen model is Z."

Hoisting routing into kit makes the decision algorithm a versioned,
testable artifact. A consumer that needs different behaviour wraps
`PickProvider` rather than reimplementing it.

### Why aim is the single source of truth for capability, cost,
### and context

The discipline here is **delegation over duplication**. `aim`
already owns:

- the cache (XDG, TTL-refreshed);
- the upstream JSON projection from `models.dev`;
- a polyglot consumer base — kit's Go/TS/Python/Rust/PHP bindings
  all consume the same registry shape.

Adding a kit-local model table — even a small one, even "just for
pricing" — would fork that data:

1. Two places update when a vendor cuts prices (or, more commonly,
   one place updates and the other silently rots).
2. Two places need maintenance contracts when a new model ships.
3. The kit-local table needs its own polyglot story or kit becomes
   Go-only for routing decisions.
4. Drift is not theoretical: every duplicated table this team has
   ever shipped has drifted within six months.

The cost of one PR upstream to `aim` to add a field is small. The
cost of N forks of a hand-maintained table is open-ended. So the
rule is: if the picker needs a fact about a model, that fact lives
on `aim.Model` (and on `aim.Filter` for query inputs). The
upstream `Cost`, `StructuredOutput`, `Temperature` additions exist
precisely so the picker doesn't have to.

This shows up concretely in the picker: `weightedPrice` reads
`m.Cost.Input` / `m.Cost.Output`; the no-match error reports
`m.Limit.Context` / `m.Limit.Output`; capability filtering is
`reg.Models(ctx, profile.Filter)` and that's it. Kit owns no
parallel catalogue.

### Why BudgetTier is a 3-value enum, not a raw dollar budget

Three categorical tiers — `cheap`, `balanced`, `premium` —
trade expressiveness for consumer-facing stability.

What we explicitly want:

- **Configs survive upstream pricing churn.** A profile that says
  `budget: cheap` keeps doing the right thing when `gpt-4o-mini`
  drops 50% next quarter; a profile that says
  `max_cost_per_1m_input: 0.50` either silently changes meaning
  or has to be edited in every deployment.
- **Operators reason about it.** "Cheap / balanced / premium" maps
  to a mental model that survives onboarding and code review. A
  numeric ceiling does not — reviewers can't tell `0.50` from
  `0.75` without a side table.
- **A/B-ing tiers is one-line.** Flip `BudgetCheap` to
  `BudgetPremium` in a config and observe; no math.
- **It binds cleanly to CLI surfaces.** `--budget cheap` parses;
  `--max-cost-per-1m-input 0.5` invites bikeshedding about units.

The tradeoff we accept: users with a precise dollar ceiling do not
have a knob today. That's a deliberate cut. If a future workload
needs that — say, a budget-capped batch job — it gets its own
surface (a dollar-bound `RequestProfile` field or a separate
`PickProviderWithinBudget`) rather than retrofitting the
categorical tier. Forward-compatible: see below.

### Why a deterministic picker, not LLM-driven selection

A "use an LLM to pick an LLM" router was considered and rejected.
Reasons:

- **Debuggability.** Same inputs → same outputs. When an operator
  asks "why did you call `gpt-4o-mini`?", the answer is the
  visible filter + budget + pool, not a model's guess.
- **Reproducibility.** CI fixtures pin a registry snapshot and
  assert exact winners. With an LLM in the loop, every change to
  the meta-model's prompt or weights re-shuffles outcomes.
- **Zero added latency.** A deterministic sort runs in
  microseconds. An LLM call adds a network round trip to every
  *other* LLM call.
- **No recursive cold-start.** "Which LLM picks an LLM?" is a
  question with no good answer when the picker is itself
  bootstrapping. Cost spirals (the meta-LLM picks the most
  expensive option because that's what it was trained on) are a
  real failure mode in adjacent prior art.
- **CI-testable in isolation.** Pure-function picker → table
  tests + property tests. An LLM-in-the-loop picker is an
  integration test on every commit.

The ranking algorithm is therefore explicit code: tier-specific
sort + alphabetical `(Provider, ID)` tiebreak. See `picker.go` for
the comparator definitions and `picker_test.go` for the locked
behaviour.

### Pool config precedence: file < env < CLI

Pool routing follows the standard three-layer precedence used
elsewhere in kit (mirroring `LoadConfig`'s file < URI < env merge):

1. **File** (`llm.yaml` `pool:` block) — the deployment's declared
   set of allowed models, version-controlled with the rest of the
   ops config.
2. **Env** (`LLM_POOL_DISABLE` — comma-separated aliases or
   `scheme:model` strings) — lets ops disable a specific entry
   without redeploying, e.g. when a vendor returns 429s for an
   hour.
3. **CLI** (`ResolvePool(entries, cliDisable)`) — one-off
   override for an interactive invocation; same shape as the env
   list so adopters can wire `--llm-pool-disable=...` directly to
   `ResolvePool`.

The direction of layering is conventional: less-specific config
yields to more-specific. Each layer can only *disable* members —
none can introduce a model that isn't in the file. That keeps the
file the single declarative source of "what's approved for this
deployment" and avoids ops-by-environment-variable surprises.

Empty pool means "accept everything in `aim`'s registry." This is
the no-pool default; adopters that don't care about gating opt
out by simply not writing the block.

### Composability: `PickProviderInPool` as evidence

The pool-aware picker is intentionally a separate top-level
function (`PickProviderInPool`) rather than a hidden mode of
`PickProvider`. That choice signals that *the picker is
composable*. Future filtering primitives — capability, region,
quota, A/B cohort — can be added as additional pre-filter
functions of the same shape:

```
FilterByPool(candidates, pool) → (survivors, eliminated)
FilterByRegion(candidates, region) → (survivors, eliminated)
FilterByQuota(candidates, quota) → (survivors, eliminated)
```

…and a thin wrapper composes whichever filters apply to the
adopter's deployment. The picker's ranking pipeline doesn't need
to know about any of them. `pool_filter.go` is the worked
example; the same template extends without forking
`picker.go`.

## Forward compatibility

The surface is small on purpose; here's how each axis extends
without breaking adopters.

### Adding a new BudgetTier

Add a constant to the `BudgetTier` block, extend `String()` /
`ParseBudgetTier`, add a branch to `rank` with its sort
comparator, and add a row to the README. The picker's input
contract (`RequestProfile`) is unchanged; adopters that don't use
the new tier are not recompiled.

### Adding a new capability filter axis

Add a field to `aim.Filter` upstream (and the matching
`aim.Model` field, if applicable). Kit's `RequestProfile.Filter`
is `aim.Filter` by value, so the new field is available
immediately — no kit change required. Update the `slog` trace
keys in `emitPickTrace` for observability, but adopters consume
the new axis the moment `aim` ships it.

### Replacing the picker entirely

`RequestProfile`, `BudgetTier`, and `*NoMatchError` are stable
types. `PickProvider` is a single public function. An adopter
that needs different ranking — say, latency-aware or
quota-aware — calls a different function or wraps `PickProvider`
without touching the input contract. `PickProviderInPool` already
demonstrates this pattern.

### Adding a new ranking heuristic without a new tier

Wrap `PickProvider`. Don't bury the heuristic inside the tier
switch — keep tier semantics stable so deployments can A/B and
reason about results. Heuristics that aren't broadly useful (very
specific batch-cost optimisation, e.g.) belong in adopter code,
not in kit.

## Acknowledged quirks

Honest disclosure of the places where the current shape made a
pragmatic call:

- **Balanced median rounds upper.** For even-sized survivor
  lists, `rank(survivors, BudgetBalanced)` returns
  `survivors[len/2]` — the upper-middle entry, not the average
  of the two middle entries. For a two-element survivor list,
  this picks the more expensive option. The alternative (averaging
  prices and finding the closest model) introduces a second sort
  pass for no perceptible accuracy gain on realistic catalogues.
  Documented in `go/ai/llm/README.md`'s picker section so
  consumers reading the output know what to expect.
- **Token-weighting is hard-coded `0.75 / 0.25`.** Cheap and
  Balanced rank by `0.75 * Cost.Input + 0.25 * Cost.Output`. This
  approximates a typical chat workload (more prompt tokens than
  completion tokens) and is not parameterised. Workloads with
  inverted ratios (long-form generation, agentic tool loops with
  short prompts) can wrap `PickProvider` with their own ranker.
  This is a magic number we accept rather than a parameter
  surface we promise to maintain across versions.
- **aim Registry caches to XDG disk even when constructed with
  `aim.WithSource`.** Tests that want isolated state must pass
  `aim.WithCacheOpts(aim.WithCacheDir(t.TempDir()))` or share the
  user's real cache directory. See `picker_test.go` for the
  pattern. This is an upstream `aim` behaviour, not a kit
  decision, but kit's tests document the workaround so adopters
  writing their own picker tests don't trip on it.
- **Nil-cost models rank as price 0 for Cheap.** A local
  Ollama-served model with no `Cost` metadata wins on Cheap. This
  is intentional (local / open-weight should beat paid APIs on
  cost) but worth knowing when reading trace output. Premium
  inverts the bias: nil-cost models lose the input-cost
  tiebreak.

## Out of scope (deliberate forward references)

These are real follow-ups that this ADR does *not* commit to:

- **No CLI for picker decisions.** Today only library callers
  invoke `PickProvider`. `foo` and others will integrate via the
  API; a generic `kit llm pick` subcommand is plausible but
  intentionally deferred until two or more adopters want the same
  shape.
- **No cost-range query syntax in `aim`.** `cost:<5` /
  `cost-between:1..10` is not a thing. Programmatic `Filter`
  construction plus the picker's post-filter rank is the only
  supported flow today.
- **No per-deployment override of price weights or median
  semantics.** The `0.75 / 0.25` token weighting and the
  upper-median behaviour are baked in. If two adopters
  independently ask for a knob, that becomes a follow-up ADR; one
  adopter wrapping `PickProvider` is the answer until then.
- **No formal pool schema in non-Go SDKs.** TS / Python / Rust /
  PHP bindings don't yet ship an equivalent pool reader. The
  schema is small enough that adopters in those languages can
  parse `llm.yaml` directly today; cross-language parity is a
  follow-up.

## Consequences

### Positive

- One picker, one set of CI tests, one place to fix routing
  bugs across every kit-using tool.
- Model facts (cost, context window, structured-output support)
  live on `aim.Model` and never drift against vendor docs because
  the table is upstream.
- Consumer surface (`RequestProfile`, `BudgetTier`) is small and
  stable; pricing churn doesn't break configs.
- Deterministic by construction; reproducible CI; no recursive
  LLM dependency.
- Pool gating gives operators a declarative allow-list with
  standard file < env < CLI precedence.
- Composability is demonstrated, not hypothesised:
  `PickProviderInPool` is the worked example for adding new
  filters without forking the picker.

### Negative

- Three categorical budget tiers don't serve users with a precise
  dollar ceiling. Accepted; future surface if needed.
- The picker's token weighting and median definition are hard-
  coded. Accepted; wrap if you need something else.
- The aim XDG-cache test gotcha is real and adopters writing
  picker tests will hit it. Mitigated by documenting the
  `WithCacheOpts` pattern in the README and in the kit test
  suite.

### Neutral

- The pool block is opt-in. Adopters who don't care don't pay
  any cognitive cost; `PickProvider` (the non-pool variant)
  remains the default entry point.
- `BudgetTier` ordering (`Cheap < Balanced < Premium`) is
  numeric on the wire (`iota`) but stable; `ParseBudgetTier`
  uses the string form so configs are not coupled to the numeric
  encoding.

## Alternatives considered

1. **Per-CLI model tables.** What we have today (de facto).
   Rejected: drift, maintenance burden multiplied by N
   adopters, no shared CI.
2. **LLM-driven router.** Use a meta-LLM to pick the working
   LLM. Rejected for debuggability, determinism, latency, and
   cold-start reasons — see "Why a deterministic picker" above.
3. **Raw dollar budgets.** `MaxCostPer1MInputTokens` on
   `RequestProfile`. Rejected: configs go stale with vendor
   pricing changes; operators can't reason about it; CLI surface
   invites bikeshedding. Categorical tiers were the explicit
   trade.
4. **Single pool list with no separate CLI / env / file
   layering.** Rejected: deployments need a declarative
   declaration in version control *and* a fast disable knob for
   incident response. The three layers are the same shape kit
   already uses for `LoadConfig`.
5. **Folding pool gating into `PickProvider` itself.** Rejected:
   makes the function take an optional `[]PoolEntry` arg and
   buries the composition story. `PickProviderInPool` as a
   separate top-level function advertises composability.
6. **Kit-owned model metadata table (cost, context, modality).**
   Rejected: this was the original temptation. Delegated to
   `aim` instead because duplication of the upstream
   `models.dev` projection would drift, and the polyglot
   consumer story already lives in `aim`.

## References

- Picker package docs: `go/ai/llm/picker.go` (package doc) and
  `go/ai/llm/README.md` §"Picker", §"Pool configuration",
  §"Tracing".
- Registry accessor: `go/ai/llm/registry.go`.
- Request profile + budget tier: `go/ai/llm/request_profile.go`.
- Pool config + resolver: `go/ai/llm/config.go`, `go/ai/llm/pool_filter.go`,
  `go/ai/llm/picker_pool.go`.
- Upstream model catalogue: `hop.top/aim` (Cost / StructuredOutput /
  Temperature on `aim.Model`; matching tristate `*bool` fields on
  `aim.Filter`).
- Upstream cache behaviour: `aim.WithCacheOpts(aim.WithCacheDir(...))`
  for test isolation.
- Trace gating: `LLM_PICKER_TRACE` env var; recognised truthy values
  `1`, `true`, `on`, `yes` (case-insensitive).
- Pool disable knobs: `LLM_POOL_DISABLE` env var; `ResolvePool` for
  CLI-parsed overrides.
