//! The native routing engine (Go `ai/llm/router`): a [`Router`] scores a
//! prompt for the strong model's win rate in `[0, 1]`; the
//! [`Controller`] parses `router-<name>-<threshold>` model strings,
//! scores, and sends the request to the strong model when the score
//! reaches the threshold, else the weak one. Feature `llm-router`
//! (builds on `llm-triton` for the scorer seam and `llm-picker` for the
//! classifier adapters), native only.
//!
//! Routers: [`RandomRouter`], [`MfRouter`] (embedding plus a Triton
//! scorer), [`BertRouter`] (token ids plus a Triton scorer) and
//! [`SwRankingRouter`] (similarity-weighted Elo over arena battles).
//! [`IntentDetector`] and [`IntentModelSelector`] pick a model pair by
//! embedding similarity; [`ScoreClassifier`] and [`IntentClassifier`]
//! expose the engine through the picker's
//! [`PromptClassifier`](crate::llm::picker::PromptClassifier) seam.
//!
//! Embedders and tokenizers are seams ([`Embedder`], [`Tokenizer`]);
//! none ships. Not ported: Go's OpenAI-compatible HTTP server
//! (`Server`, `NewHTTPServer`) that hosts a controller, and its config
//! types.

mod classify;
mod controller;
mod intent;
mod routers;
#[cfg(test)]
mod tests;

pub use classify::{
    IntentClassifier, ScoreClassifier, DEFAULT_SCORE_THRESHOLD, LABEL_STRONG, LABEL_WEAK,
};
pub use controller::{
    last_user_signal, parse_model_string, Controller, ControllerBuilder, Middleware, ModelPair,
    UserSignal,
};
pub use intent::{cosine_similarity, IntentDetector, IntentMapping, IntentModelSelector};
pub use routers::{
    score_from_logits, softmax, BattleRecord, BertConfig, BertRouter, MfConfig, MfRouter,
    RandomRouter, SwConfig, SwRankingRouter, Tokenizer,
};

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use super::error::LlmError;
use super::provider::BoxFuture;

/// A routing-specific failure, `routing: <message>`.
pub(crate) fn routing_error(msg: impl std::fmt::Display) -> LlmError {
    LlmError::Invalid(format!("routing: {msg}"))
}

/// Scores a prompt: the strong model's win rate in `[0, 1]`. At or above
/// the threshold the controller routes to the strong model.
pub trait Router: Send + Sync {
    fn score<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<f64, LlmError>>;

    /// Scores the full multimodal signal. Routers that route on modality
    /// override this (Go's `ModalRouter`); the default scores the text,
    /// and the controller always calls this one.
    fn score_signal<'a>(&'a self, sig: &'a UserSignal) -> BoxFuture<'a, Result<f64, LlmError>> {
        self.score(&sig.text)
    }
}

/// Produces an embedding vector for a text, typically through an
/// OpenAI-compatible embeddings API.
pub trait Embedder: Send + Sync {
    fn embed<'a>(&'a self, text: &'a str) -> BoxFuture<'a, Result<Vec<f64>, LlmError>>;
}

/// Maps router names to routers.
#[derive(Default)]
pub struct Registry {
    routers: Mutex<BTreeMap<String, Arc<dyn Router>>>,
}

impl Registry {
    pub fn new() -> Self {
        Registry::default()
    }

    /// Add a named router; a name registers once.
    pub fn register(&self, name: &str, router: Arc<dyn Router>) -> Result<(), LlmError> {
        let mut r = self.routers.lock().expect("router registry lock");
        if r.contains_key(name) {
            return Err(LlmError::invalid(format!(
                "router {name:?} already registered"
            )));
        }
        r.insert(name.to_string(), router);
        Ok(())
    }

    /// The router for `name`, or a routing error listing the names.
    pub fn get(&self, name: &str) -> Result<Arc<dyn Router>, LlmError> {
        let r = self.routers.lock().expect("router registry lock");
        r.get(name).cloned().ok_or_else(|| {
            let names: Vec<&String> = r.keys().collect();
            routing_error(format!("unknown router {name:?}; available: {names:?}"))
        })
    }

    /// Registered names, sorted.
    pub fn names(&self) -> Vec<String> {
        self.routers
            .lock()
            .expect("router registry lock")
            .keys()
            .cloned()
            .collect()
    }
}
