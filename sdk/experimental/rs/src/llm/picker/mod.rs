//! The model picker (Go `PickProvider`, `PickProviderInPool`,
//! `PickProviderClassified`): filters a model catalog by a
//! [`RequestProfile`], narrows it to an operator pool, and ranks the
//! survivors by [`BudgetTier`]. Feature `llm-picker`; pure and wasm-safe.
//!
//! Go reads the aim catalog; here the catalog is the [`ModelCatalog`]
//! seam, with [`StaticCatalog`] for an in-memory list. Token-window
//! filters give models with unknown limits (0) the benefit of the doubt:
//! only a positively known mismatch eliminates. The pick is
//! deterministic: ties break by (provider, id).
//!
//! Tracing: a [`Trace`] receives one `llm.pick` event per pick (and one
//! `llm.classify` per classified pick) with Go's attribute keys; it is
//! on when `LLM_PICKER_TRACE` is `1`, `true`, `on` or `yes`, read per
//! pick through the environment function [`Trace::from_env`] was given.

mod catalog;
mod classifier;
mod rank;
#[cfg(test)]
mod tests;
mod trace;

use std::sync::Arc;

use super::error::LlmError;
use super::pool::PoolEntry;

pub use catalog::{Cost, Filter, Limits, Modalities, Model, ModelCatalog, StaticCatalog};
pub use classifier::{
    Classification, ClassifiedRoute, ClassifierRouting, PromptClassifier, FALLBACK_ERROR,
    FALLBACK_LOW_CONFIDENCE, FALLBACK_NO_CLASSIFIER, FALLBACK_UNKNOWN_LABEL,
};
pub use trace::{Trace, TraceEvent};

/// The consumer-facing input to a pick. The consumer derives the
/// profile from the invocation; kit picks the provider.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct RequestProfile {
    /// Constrains which models qualify.
    pub filter: Filter,
    /// Upper bound on prompt tokens: models with a smaller known context
    /// window are rejected. 0 = no filter.
    pub max_input_tokens: u64,
    /// The requested response budget: models with a smaller known output
    /// limit are rejected. 0 = no filter.
    pub max_output_tokens: u64,
}

/// The cost and capability trade-off a pick makes.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum BudgetTier {
    /// The lowest weighted price (a missing price counts as 0).
    #[default]
    Cheap,
    /// The median by weighted price.
    Balanced,
    /// The largest context window, then the highest input price.
    Premium,
}

impl BudgetTier {
    /// `cheap`, `balanced` or `premium`.
    pub fn as_str(self) -> &'static str {
        match self {
            BudgetTier::Cheap => "cheap",
            BudgetTier::Balanced => "balanced",
            BudgetTier::Premium => "premium",
        }
    }

    /// Parse a label, case-insensitively, whitespace trimmed.
    pub fn parse(s: &str) -> Result<Self, LlmError> {
        match s.trim().to_lowercase().as_str() {
            "cheap" => Ok(BudgetTier::Cheap),
            "balanced" => Ok(BudgetTier::Balanced),
            "premium" => Ok(BudgetTier::Premium),
            _ => Err(LlmError::invalid(format!("llm: invalid budget tier {s:?}"))),
        }
    }
}

/// Elimination stage labels.
pub const ELIM_CONTEXT_WINDOW: &str = "context_window";
pub const ELIM_OUTPUT_TOKENS: &str = "output_tokens";
pub const ELIM_POOL_DISABLED: &str = "pool_disabled";

/// Why a candidate was dropped.
#[derive(Debug, Clone, PartialEq)]
pub struct Elimination {
    pub model: Model,
    /// One of the `ELIM_*` labels.
    pub stage: &'static str,
    pub detail: String,
}

/// No model qualified. `candidate_count` is the count the catalog
/// returned before any elimination, so an operator can tell a filter too
/// narrow from candidates all eliminated by budget caps.
#[derive(Debug, Clone, PartialEq)]
pub struct NoMatch {
    pub filter: Filter,
    pub candidate_count: usize,
    pub eliminated: Vec<Elimination>,
}

impl std::fmt::Display for NoMatch {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "llm: no provider matches: {} candidates, {} eliminated",
            self.candidate_count,
            self.eliminated.len()
        )
    }
}

/// Why a pick failed.
#[derive(Debug, Clone, PartialEq)]
pub enum PickError {
    /// Go's `ErrNoProviderMatches`.
    NoMatch(Box<NoMatch>),
    /// The catalog failed.
    Catalog(LlmError),
}

impl PickError {
    /// Whether nothing qualified (Go `errors.Is(err, ErrNoProviderMatches)`).
    pub fn is_no_match(&self) -> bool {
        matches!(self, PickError::NoMatch(_))
    }
}

impl std::fmt::Display for PickError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            PickError::NoMatch(n) => n.fmt(f),
            PickError::Catalog(e) => write!(f, "llm: query registry: {e}"),
        }
    }
}

impl std::error::Error for PickError {}

/// Picks models from a catalog.
#[derive(Clone)]
pub struct Picker {
    catalog: Arc<dyn ModelCatalog>,
    trace: Trace,
}

impl Picker {
    pub fn new(catalog: Arc<dyn ModelCatalog>) -> Self {
        Picker {
            catalog,
            trace: Trace::off(),
        }
    }

    /// Emit trace events through `trace`.
    pub fn with_trace(mut self, trace: Trace) -> Self {
        self.trace = trace;
        self
    }

    /// The best model matching `profile` under `budget`, from the whole
    /// catalog.
    pub async fn pick(
        &self,
        profile: &RequestProfile,
        budget: BudgetTier,
    ) -> Result<Model, PickError> {
        self.pick_in_pool(profile, budget, &[]).await
    }

    /// [`Picker::pick`] over the models `pool` approves: the enabled
    /// entries' `(scheme, model)` pairs. An empty pool accepts
    /// everything. Pool eliminations merge into [`NoMatch::eliminated`]
    /// ahead of the token-window ones.
    pub async fn pick_in_pool(
        &self,
        profile: &RequestProfile,
        budget: BudgetTier,
        pool: &[PoolEntry],
    ) -> Result<Model, PickError> {
        let models = self
            .catalog
            .models(&profile.filter)
            .await
            .map_err(PickError::Catalog)?;
        let candidate_count = models.len();
        let (pooled, mut eliminated) = filter_by_pool(models, pool);
        let (survivors, token_elim) = rank::apply_token_window(pooled, profile);
        eliminated.extend(token_elim);
        let on = self.trace.enabled();
        if survivors.is_empty() {
            if on {
                self.trace
                    .pick(profile, budget, candidate_count, eliminated.len(), None);
            }
            return Err(PickError::NoMatch(Box::new(NoMatch {
                filter: profile.filter.clone(),
                candidate_count,
                eliminated,
            })));
        }
        let winner = rank::rank(survivors, budget);
        if on {
            self.trace.pick(
                profile,
                budget,
                candidate_count,
                eliminated.len(),
                Some(&winner),
            );
        }
        Ok(winner)
    }

    /// [`Picker::pick_in_pool`] behind a classifier stage. A confident,
    /// mapped verdict swaps in its route's pool and/or budget; anything
    /// else (no classifier, an error, no label, low confidence, an
    /// unmapped label) takes the deterministic chain with the base pool
    /// and budget. A classified route that matches no model surfaces
    /// [`PickError::NoMatch`]: that is a route misconfiguration, not
    /// ambiguity, and retrying the base pool would hide it.
    pub async fn pick_classified(
        &self,
        routing: &ClassifierRouting,
        prompt: &str,
        profile: &RequestProfile,
        budget: BudgetTier,
        pool: &[PoolEntry],
    ) -> Result<Model, PickError> {
        let (verdict, route, fallback) = classifier::resolve(routing, prompt).await;
        if self.trace.enabled() {
            self.trace.classify(verdict.as_ref(), fallback);
        }
        if fallback.is_some() {
            return self.pick_in_pool(profile, budget, pool).await;
        }
        let route = route.unwrap_or_default();
        let eff_pool = if route.pool.is_empty() {
            pool
        } else {
            &route.pool
        };
        let eff_budget = route.budget.unwrap_or(budget);
        self.pick_in_pool(profile, eff_budget, eff_pool).await
    }
}

/// Keep the models in `pool` with `enabled` set, matched by
/// `(provider, id)` against `(scheme, model)`. An empty pool keeps all
/// and eliminates none; the others eliminated are labelled
/// [`ELIM_POOL_DISABLED`].
pub fn filter_by_pool(
    candidates: Vec<Model>,
    pool: &[PoolEntry],
) -> (Vec<Model>, Vec<Elimination>) {
    if pool.is_empty() {
        return (candidates, Vec::new());
    }
    let allowed = |m: &Model| {
        pool.iter()
            .any(|p| p.enabled && p.scheme == m.provider && p.model == m.id)
    };
    let mut survivors = Vec::new();
    let mut eliminated = Vec::new();
    for m in candidates {
        if allowed(&m) {
            survivors.push(m);
        } else {
            eliminated.push(Elimination {
                model: m,
                stage: ELIM_POOL_DISABLED,
                detail: "not in pool or disabled".into(),
            });
        }
    }
    (survivors, eliminated)
}
