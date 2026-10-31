//! The classifier stage in front of the picker (Go
//! `picker_classifier.go`): the seam and the routing table mapping labels
//! to pool and budget overrides. Concrete classifiers live elsewhere.

use std::collections::BTreeMap;
use std::sync::Arc;

use crate::llm::error::LlmError;
use crate::llm::pool::PoolEntry;
use crate::llm::provider::BoxFuture;

use super::BudgetTier;

/// Classifies a prompt into a routing label with a confidence in
/// `[0, 1]`. Implementations may call remote scorers; errors are never
/// fatal to picking.
pub trait PromptClassifier: Send + Sync {
    fn classify<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<Classification, LlmError>>;
}

/// A verdict.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct Classification {
    /// The routing label. Empty means no verdict.
    pub label: String,
    pub confidence: f64,
}

/// The override applied when its label wins. Unset fields inherit the
/// caller's base pool and budget, so a label can steer only the pool,
/// only the budget, both, or neither.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ClassifiedRoute {
    /// Replaces the base pool when non-empty.
    pub pool: Vec<PoolEntry>,
    /// Replaces the base tier when set.
    pub budget: Option<BudgetTier>,
}

/// A classifier and its label routes.
#[derive(Clone, Default)]
pub struct ClassifierRouting {
    /// `None` disables the stage: every pick takes the deterministic
    /// chain.
    pub classifier: Option<Arc<dyn PromptClassifier>>,
    /// Labels absent from the map are ambiguous and fall back.
    pub routes: BTreeMap<String, ClassifiedRoute>,
    /// Verdicts below this are ambiguous. 0 accepts any success.
    pub min_confidence: f64,
}

pub const FALLBACK_NO_CLASSIFIER: &str = "no_classifier";
pub const FALLBACK_ERROR: &str = "classifier_error";
pub const FALLBACK_LOW_CONFIDENCE: &str = "low_confidence";
pub const FALLBACK_UNKNOWN_LABEL: &str = "unknown_label";

/// Evaluates the classifier and the routing table: the verdict (when the
/// classifier returned one), the route, and the fallback reason (`None`
/// when the route applies).
pub(crate) async fn resolve(
    routing: &ClassifierRouting,
    prompt: &str,
) -> (
    Option<Classification>,
    Option<ClassifiedRoute>,
    Option<&'static str>,
) {
    let Some(c) = &routing.classifier else {
        return (None, None, Some(FALLBACK_NO_CLASSIFIER));
    };
    let verdict = match c.classify(prompt).await {
        Ok(v) => v,
        Err(_) => return (None, None, Some(FALLBACK_ERROR)),
    };
    if verdict.label.is_empty() {
        return (Some(verdict), None, Some(FALLBACK_UNKNOWN_LABEL));
    }
    if verdict.confidence < routing.min_confidence {
        return (Some(verdict), None, Some(FALLBACK_LOW_CONFIDENCE));
    }
    match routing.routes.get(&verdict.label) {
        Some(r) => (Some(verdict), Some(r.clone()), None),
        None => (Some(verdict), None, Some(FALLBACK_UNKNOWN_LABEL)),
    }
}
