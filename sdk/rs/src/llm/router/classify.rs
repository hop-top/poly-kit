//! The router engine as picker classifiers (Go `classifier.go`): the
//! dependency runs router to picker; the picker sees only its trait.

use std::sync::Arc;

use crate::llm::error::LlmError;
use crate::llm::picker::{Classification, PromptClassifier};
use crate::llm::provider::BoxFuture;

use super::{IntentDetector, Router};

pub const LABEL_STRONG: &str = "strong";
pub const LABEL_WEAK: &str = "weak";
/// The strong/weak boundary when [`ScoreClassifier::threshold`] is 0.
pub const DEFAULT_SCORE_THRESHOLD: f64 = 0.5;

/// Adapts a [`Router`] win-rate score to a classifier. A score at or
/// above the threshold is `strong` with the score as confidence, below
/// it `weak` with `1 - score`, so scores near the boundary carry low
/// confidence and a caller-side `min_confidence` sends them to the
/// deterministic chain.
pub struct ScoreClassifier {
    pub router: Arc<dyn Router>,
    /// 0 means [`DEFAULT_SCORE_THRESHOLD`].
    pub threshold: f64,
    /// Empty means [`LABEL_STRONG`].
    pub strong_label: String,
    /// Empty means [`LABEL_WEAK`].
    pub weak_label: String,
}

impl ScoreClassifier {
    pub fn new(router: Arc<dyn Router>) -> Self {
        ScoreClassifier {
            router,
            threshold: 0.0,
            strong_label: String::new(),
            weak_label: String::new(),
        }
    }
}

impl PromptClassifier for ScoreClassifier {
    fn classify<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<Classification, LlmError>> {
        Box::pin(async move {
            let score = self
                .router
                .score(prompt)
                .await
                .map_err(|e| LlmError::Invalid(format!("router: score prompt: {e}")))?;
            let threshold = if self.threshold == 0.0 {
                DEFAULT_SCORE_THRESHOLD
            } else {
                self.threshold
            };
            let pick = |custom: &str, default: &str| {
                if custom.is_empty() {
                    default.to_string()
                } else {
                    custom.to_string()
                }
            };
            Ok(if score >= threshold {
                Classification {
                    label: pick(&self.strong_label, LABEL_STRONG),
                    confidence: score,
                }
            } else {
                Classification {
                    label: pick(&self.weak_label, LABEL_WEAK),
                    confidence: 1.0 - score,
                }
            })
        })
    }
}

/// Adapts an [`IntentDetector`] using its normalized confidence
/// distribution. The best intent wins; ties break alphabetically. With
/// no registered intents the verdict is empty, which pickers treat as
/// no verdict.
pub struct IntentClassifier {
    pub detector: Arc<IntentDetector>,
}

impl PromptClassifier for IntentClassifier {
    fn classify<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<Classification, LlmError>> {
        Box::pin(async move {
            let scores = self
                .detector
                .confidence(prompt)
                .await
                .map_err(|e| LlmError::Invalid(format!("router: intent confidence: {e}")))?;
            // BTreeMap iterates alphabetically; only a strictly greater
            // score displaces the best.
            let mut best: Option<(&String, f64)> = None;
            for (label, &s) in &scores {
                if best.is_none_or(|(_, b)| s > b) {
                    best = Some((label, s));
                }
            }
            Ok(
                best.map_or_else(Classification::default, |(l, c)| Classification {
                    label: l.clone(),
                    confidence: c,
                }),
            )
        })
    }
}
