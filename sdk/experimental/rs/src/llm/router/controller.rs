//! The controller: model-string parsing, routing, middleware, counts and
//! the delegating `complete`.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use crate::llm::error::LlmError;
use crate::llm::provider::{BoxFuture, Capability, Provider};
use crate::llm::types::{MediaKind, Message, Part, Request, Response, Role};

use super::{routing_error, Registry};

/// The strong and weak model identifiers a router chooses between.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ModelPair {
    pub strong: String,
    pub weak: String,
}

/// Overrides the model pair per request, for example by detected
/// intent. An error or `None` keeps the pair so far.
pub trait Middleware: Send + Sync {
    fn model_pair<'a>(
        &'a self,
        prompt: &'a str,
    ) -> BoxFuture<'a, Result<Option<ModelPair>, LlmError>>;
}

/// The text and modality flags of a user message.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct UserSignal {
    pub text: String,
    pub has_image: bool,
    pub has_audio: bool,
    pub has_video: bool,
}

/// Parse `router-<name>-<threshold>` into the router name and a
/// threshold in `[0, 1]`. The threshold follows the last dash.
pub fn parse_model_string(model: &str) -> Result<(String, f64), LlmError> {
    let Some(rest) = model.strip_prefix("router-") else {
        return Err(routing_error(format!(
            "invalid model {model:?}: must start with 'router-'"
        )));
    };
    let last = rest.rfind('-');
    let Some(i) = last.filter(|&i| i != 0 && i != rest.len() - 1) else {
        return Err(routing_error(format!(
            "invalid model {model:?}: expected format 'router-<name>-<threshold>'"
        )));
    };
    let (name, text) = (&rest[..i], &rest[i + 1..]);
    let threshold: f64 = text.parse().map_err(|_| {
        routing_error(format!(
            "invalid threshold {text:?}: strconv.ParseFloat: parsing {text:?}: invalid syntax"
        ))
    })?;
    check_threshold(threshold)?;
    Ok((name.to_string(), threshold))
}

fn check_threshold(t: f64) -> Result<(), LlmError> {
    // NaN fails the range test, as in Go.
    if (0.0..=1.0).contains(&t) {
        Ok(())
    } else {
        Err(routing_error(format!(
            "threshold {t:.4} out of [0,1] range"
        )))
    }
}

/// Text and modality flags of the last user message (else the last
/// message). Parts concatenate their text.
pub fn last_user_signal(msgs: &[Message]) -> UserSignal {
    let Some(msg) = msgs
        .iter()
        .rev()
        .find(|m| m.role == Role::User)
        .or_else(|| msgs.last())
    else {
        return UserSignal::default();
    };
    let mut sig = UserSignal::default();
    if msg.parts.is_empty() {
        sig.text = msg.content.clone();
        return sig;
    }
    for p in &msg.parts {
        match p {
            Part::Text(t) => sig.text.push_str(t),
            Part::Media(m) => match m.kind {
                MediaKind::Image => sig.has_image = true,
                MediaKind::Audio => sig.has_audio = true,
                MediaKind::Video => sig.has_video = true,
            },
        }
    }
    sig
}

/// Manages router selection, threshold parsing, the middleware chain and
/// completions through a provider.
pub struct Controller {
    registry: Arc<Registry>,
    default_pair: ModelPair,
    middleware: Vec<Arc<dyn Middleware>>,
    provider: Option<Arc<dyn Provider>>,
    counts: Mutex<BTreeMap<String, BTreeMap<String, u64>>>,
}

/// Configures a [`Controller`].
pub struct ControllerBuilder {
    registry: Arc<Registry>,
    default_pair: ModelPair,
    middleware: Vec<Arc<dyn Middleware>>,
    provider: Option<Arc<dyn Provider>>,
}

impl ControllerBuilder {
    /// Append middleware to the chain.
    pub fn middleware(mut self, mw: Arc<dyn Middleware>) -> Self {
        self.middleware.push(mw);
        self
    }

    /// The provider completions go to.
    pub fn provider(mut self, p: Arc<dyn Provider>) -> Self {
        self.provider = Some(p);
        self
    }

    pub fn build(self) -> Controller {
        Controller {
            registry: self.registry,
            default_pair: self.default_pair,
            middleware: self.middleware,
            provider: self.provider,
            counts: Mutex::default(),
        }
    }
}

impl Controller {
    pub fn builder(registry: Arc<Registry>, default_pair: ModelPair) -> ControllerBuilder {
        ControllerBuilder {
            registry,
            default_pair,
            middleware: Vec::new(),
            provider: None,
        }
    }

    pub fn new(registry: Arc<Registry>, default_pair: ModelPair) -> Self {
        Self::builder(registry, default_pair).build()
    }

    /// Score `sig` with the named router and return the selected model:
    /// the strong one when the score reaches `threshold`, else the weak.
    pub async fn route(
        &self,
        sig: &UserSignal,
        router_name: &str,
        threshold: f64,
    ) -> Result<String, LlmError> {
        let router = self.registry.get(router_name)?;
        check_threshold(threshold)?;
        let pair = self.model_pair(&sig.text).await;
        let score = router.score_signal(sig).await.map_err(|e| {
            LlmError::Invalid(format!("router {router_name:?} scoring failed: {e}"))
        })?;
        let selected = if score >= threshold {
            pair.strong
        } else {
            pair.weak
        };
        self.record(router_name, &selected);
        Ok(selected)
    }

    /// Parse a `router-<name>-<threshold>` model string and route.
    pub async fn route_from_model(
        &self,
        sig: &UserSignal,
        model: &str,
    ) -> Result<String, LlmError> {
        let (name, threshold) = parse_model_string(model)?;
        self.route(sig, &name, threshold).await
    }

    /// Parse the request's model string, route, and complete through the
    /// provider with the selected model.
    pub async fn complete(&self, req: &Request) -> Result<Response, LlmError> {
        let Some(provider) = &self.provider else {
            return Err(routing_error("no provider configured"));
        };
        if !provider.capabilities().contains(&Capability::Complete) {
            return Err(routing_error("provider does not support Complete"));
        }
        let sig = last_user_signal(&req.messages);
        let selected = self.route_from_model(&sig, &req.model).await?;
        let routed = Request {
            model: selected,
            ..req.clone()
        };
        provider.complete(&routed).await
    }

    /// A snapshot of routing counts per router per model.
    pub fn model_counts(&self) -> BTreeMap<String, BTreeMap<String, u64>> {
        self.counts.lock().expect("router counts lock").clone()
    }

    /// The pair after the middleware chain, each link seeing the prompt.
    async fn model_pair(&self, prompt: &str) -> ModelPair {
        let mut pair = self.default_pair.clone();
        for mw in &self.middleware {
            if let Ok(Some(mp)) = mw.model_pair(prompt).await {
                pair = mp;
            }
        }
        pair
    }

    fn record(&self, router: &str, model: &str) {
        let mut c = self.counts.lock().expect("router counts lock");
        *c.entry(router.to_string())
            .or_default()
            .entry(model.to_string())
            .or_default() += 1;
    }
}
