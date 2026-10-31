//! Embedding-based intent detection and the middleware built on it.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use crate::llm::error::LlmError;
use crate::llm::provider::BoxFuture;

use super::{Embedder, Middleware, ModelPair};

/// Maps an intent name to a model pair override.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct IntentMapping {
    pub intent: String,
    pub description: String,
    pub pair: ModelPair,
}

#[derive(Clone, Default)]
struct IntentData {
    examples: Vec<String>,
    embeddings: Vec<Vec<f64>>,
}

/// Classifies a prompt into an intent by cosine similarity against
/// labeled examples.
pub struct IntentDetector {
    embedder: Arc<dyn Embedder>,
    intents: Mutex<BTreeMap<String, IntentData>>,
}

impl IntentDetector {
    pub fn new(embedder: Arc<dyn Embedder>) -> Self {
        IntentDetector {
            embedder,
            intents: Mutex::default(),
        }
    }

    /// Add labeled example prompts for `intent`; embeddings are computed
    /// eagerly, and nothing is added if any fails.
    pub async fn add_examples(&self, intent: &str, examples: &[String]) -> Result<(), LlmError> {
        let mut embeddings = Vec::with_capacity(examples.len());
        for (i, ex) in examples.iter().enumerate() {
            let e = self
                .embedder
                .embed(ex)
                .await
                .map_err(|e| LlmError::Invalid(format!("intent: embed example {i}: {e}")))?;
            embeddings.push(e);
        }
        let mut m = self.intents.lock().expect("intents lock");
        let d = m.entry(intent.to_string()).or_default();
        d.examples.extend_from_slice(examples);
        d.embeddings.extend(embeddings);
        Ok(())
    }

    fn snapshot(&self) -> BTreeMap<String, IntentData> {
        self.intents.lock().expect("intents lock").clone()
    }

    /// The best-matching intent, `general` when none is registered or
    /// none has examples. On an embedding failure: the error.
    pub async fn detect(&self, prompt: &str) -> Result<String, LlmError> {
        let snap = self.snapshot();
        if snap.is_empty() {
            return Ok("general".into());
        }
        let p = self
            .embedder
            .embed(prompt)
            .await
            .map_err(|e| LlmError::Invalid(format!("intent: embed prompt: {e}")))?;
        let mut best = ("general".to_string(), -1.0_f64);
        for (intent, d) in &snap {
            if d.embeddings.is_empty() {
                continue;
            }
            let avg = d
                .embeddings
                .iter()
                .map(|e| cosine_similarity(&p, e))
                .sum::<f64>()
                / d.embeddings.len() as f64;
            if avg > best.1 {
                best = (intent.clone(), avg);
            }
        }
        Ok(best.0)
    }

    /// Normalized similarity per intent: a distribution summing to 1
    /// (uniform when every similarity is not above 0).
    pub async fn confidence(&self, prompt: &str) -> Result<BTreeMap<String, f64>, LlmError> {
        let snap = self.snapshot();
        if snap.is_empty() {
            return Ok(BTreeMap::new());
        }
        let p = self
            .embedder
            .embed(prompt)
            .await
            .map_err(|e| LlmError::Invalid(format!("intent: embed prompt: {e}")))?;
        let mut scores = BTreeMap::new();
        let mut total = 0.0;
        for (intent, d) in &snap {
            if d.embeddings.is_empty() {
                continue;
            }
            let sum: f64 = d.embeddings.iter().map(|e| cosine_similarity(&p, e)).sum();
            let avg = (sum / d.embeddings.len() as f64).max(0.0);
            scores.insert(intent.clone(), avg);
            total += avg;
        }
        if total > 0.0 {
            for v in scores.values_mut() {
                *v /= total;
            }
        } else if !scores.is_empty() {
            let u = 1.0 / scores.len() as f64;
            for v in scores.values_mut() {
                *v = u;
            }
        }
        Ok(scores)
    }
}

/// A [`Middleware`] choosing model pairs by detected intent; `general`
/// and unknown intents, and detection failures, get the default pair.
pub struct IntentModelSelector {
    detector: Arc<IntentDetector>,
    mappings: BTreeMap<String, ModelPair>,
    default_pair: ModelPair,
}

impl IntentModelSelector {
    pub fn new(
        detector: Arc<IntentDetector>,
        default_pair: ModelPair,
        mappings: Vec<IntentMapping>,
    ) -> Self {
        IntentModelSelector {
            detector,
            mappings: mappings.into_iter().map(|m| (m.intent, m.pair)).collect(),
            default_pair,
        }
    }
}

impl Middleware for IntentModelSelector {
    fn model_pair<'a>(
        &'a self,
        prompt: &'a str,
    ) -> BoxFuture<'a, Result<Option<ModelPair>, LlmError>> {
        Box::pin(async move {
            let pair = match self.detector.detect(prompt).await {
                Ok(intent) => self.mappings.get(&intent).unwrap_or(&self.default_pair),
                Err(_) => &self.default_pair,
            };
            Ok(Some(pair.clone()))
        })
    }
}

/// Cosine similarity clamped to `[-1, 1]`; 0 when either norm is 0.
pub fn cosine_similarity(a: &[f64], b: &[f64]) -> f64 {
    let (na, nb) = (super::routers::vec_norm(a), super::routers::vec_norm(b));
    if na == 0.0 || nb == 0.0 {
        return 0.0;
    }
    (super::routers::dot(a, b) / (na * nb)).clamp(-1.0, 1.0)
}
