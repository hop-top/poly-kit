# picker

## What it answers

How a tool chooses a model without hard-coding one: filter a catalog by
a request profile, narrow it to the llm.yaml pool, rank by budget tier,
optionally steered by a prompt classifier. A port of Go `PickProvider`,
`PickProviderInPool` and `PickProviderClassified`. Feature `llm-picker`
(pool config and `PoolEntry` are in the `llm` core); pure, wasm-safe.

## Use it when

- you pick by capability and size → `Picker::new(catalog).pick(&RequestProfile { filter, max_input_tokens, max_output_tokens }, BudgetTier::Balanced).await`
- the operator limits the models → `load_pool(&ConfigFile::load(), &process_env)` (llm.yaml `pool:`, `LLM_POOL_DISABLE`), then `pick_in_pool`; `resolve_pool` applies your own disables
- a prompt should steer the choice → `pick_classified(&ClassifierRouting { classifier, routes, min_confidence }, prompt, ...)`
- you have no live catalog → `StaticCatalog::new(models)`; for a remote one implement `ModelCatalog`
- you debug a pick → `Picker::with_trace(Trace::from_env(env, sink))`, on with `LLM_PICKER_TRACE=1`

## Contract

- Catalog: `Filter` matches as aim's does (exact provider and family, modality subsets, tristate flags, case-insensitive `query`); `StaticCatalog` orders by (provider, id). Go reads the aim catalog; Rust has the `ModelCatalog` seam instead.
- Token windows: a limit of 0 is unknown and passes; only a known mismatch eliminates (`context_window`, `output_tokens`).
- Pool: enabled entries match `(provider, id)` against `(scheme, model)`; an empty pool accepts everything; the rest are eliminated as `pool_disabled`, ahead of the window eliminations in `NoMatch::eliminated`.
- Ranking, stable and ending in the (provider, id) tiebreak: Cheap lowest `input*0.75 + output*0.25` (no cost = 0); Balanced the median of that; Premium the largest context, then the highest input price.
- `PickError::NoMatch` carries the filter, the catalog's candidate count and every elimination; a catalog failure is `PickError::Catalog`.
- Classifier: a confident, mapped verdict swaps in the route's pool and/or budget; no classifier, an error, no label, low confidence or an unmapped label take the base pool and budget (`classifier.fallback_reason`: `no_classifier`, `classifier_error`, `low_confidence`, `unknown_label`). A classified route with no match fails, never falls back.
- Trace events carry Go's `picker.*` and `classifier.*` keys, as `TraceEvent` values to your sink instead of slog.

## See also

- [`go/ai/llm`](../../../../../../go/ai/llm), the Go reference (`picker*.go`, `pool_filter.go`)
