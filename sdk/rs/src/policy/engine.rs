//! The engine: compiled rules, the resolvers and deny-overrides
//! composition. Go's policy.go.

use std::error::Error as StdError;
use std::sync::Arc;

use serde::Serialize;
use serde_json::{Map, Value as Json};

use crate::bus::Context;

use super::config::{Config, Decision, Effect, Policy, TOPIC_STAGE_VIOLATED, VETO_TOPICS};
use super::error::{Error, PolicyDeniedError};
use super::eval::{Activation, CelEvaluator, Evaluator};

/// The actor exposed to expressions as `principal`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Principal {
    /// The actor's id.
    pub id: String,
    /// The actor's role.
    pub role: String,
    /// How the role was resolved: `"ctx"`, `"env"` or `"none"`.
    pub source: String,
}

/// Request attributes carried on the [`Context`] and exposed as
/// `context`. Go's `ContextAttrsKey` map.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ContextAttrs {
    /// The `--note` justification; `context.note`.
    pub note: String,
    /// Host-supplied flags; `context.request_attrs`.
    pub request_attrs: Map<String, Json>,
}

/// `ctx` carrying `principal`, which [`default_principal_resolver`]
/// finds. Go's `context.WithValue(ctx, ContextPrincipalKey, p)`.
pub fn with_principal(ctx: &Context, principal: Principal) -> Context {
    ctx.with_value(principal)
}

/// `ctx` carrying `attrs`, exposed as the `context` binding. Go's
/// `context.WithValue(ctx, ContextAttrsKey, m)`.
pub fn with_context_attrs(ctx: &Context, attrs: ContextAttrs) -> Context {
    ctx.with_value(attrs)
}

/// Resolves the principal for an event's context. The wiring calls it
/// per event.
pub type PrincipalResolver = Arc<dyn Fn(&Context) -> Principal + Send + Sync>;

/// Returns the `stage` binding (`mode`, `since`, `until`, `reason`,
/// `allow`, `deny`) for a scope. An error or an empty map means "no
/// state set". Go's `StageResolver`.
pub type StageResolver =
    Arc<dyn Fn(&str) -> Result<Map<String, Json>, Box<dyn StdError + Send + Sync>> + Send + Sync>;

/// Extracts the scope from an activation. The default reads
/// `payload.scope`, then `resource.id`, then `""`.
pub type ScopeResolver = Arc<dyn Fn(&Activation) -> String + Send + Sync>;

/// Receives [`TOPIC_STAGE_VIOLATED`] when a stage-driven rule denies.
pub trait ViolationPublisher: Send + Sync {
    /// Publishes one violation.
    fn publish(&self, topic: &str, payload: &ViolationPayload);
}

/// The payload published on [`TOPIC_STAGE_VIOLATED`]. Go's
/// `ViolationPayload`, same JSON keys.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct ViolationPayload {
    /// The scope the denial applied to.
    pub scope: String,
    /// The stage mode at the time.
    pub stage: String,
    /// The topic that was denied.
    pub topic: String,
    /// The entity kind, when known.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub entity: String,
    /// The principal id, when known.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub principal: String,
    /// The denying rule's message.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub message: String,
}

/// The principal from `ctx` (see [`with_principal`]), else from the
/// environment: `KIT_POLICY_ROLE` for the role, `USER` for the id, with
/// source `"env"` when either is set and `"none"` when neither is.
/// wasm32 has no environment: there it is `"none"`. A principal found
/// on `ctx` with no source gets `"ctx"`.
pub fn default_principal_resolver(ctx: &Context) -> Principal {
    if let Some(p) = ctx.value::<Principal>() {
        let mut p = p.clone();
        if p.source.is_empty() {
            p.source = "ctx".to_string();
        }
        return p;
    }
    #[cfg(not(target_arch = "wasm32"))]
    {
        let role = std::env::var("KIT_POLICY_ROLE").unwrap_or_default();
        let id = std::env::var("USER").unwrap_or_default();
        let source = if role.is_empty() && id.is_empty() {
            "none"
        } else {
            "env"
        };
        Principal {
            id,
            role,
            source: source.to_string(),
        }
    }
    #[cfg(target_arch = "wasm32")]
    {
        Principal {
            source: "none".to_string(),
            ..Principal::default()
        }
    }
}

/// Configures an [`Engine`]. Go's `EngineOption`s, as methods.
pub struct EngineBuilder {
    eval: Option<Box<dyn Evaluator>>,
    princ: PrincipalResolver,
    stage_res: Option<StageResolver>,
    scope_res: Option<ScopeResolver>,
    violate_out: Option<Arc<dyn ViolationPublisher>>,
}

impl Default for EngineBuilder {
    fn default() -> Self {
        EngineBuilder {
            eval: None,
            princ: Arc::new(default_principal_resolver),
            stage_res: None,
            scope_res: None,
            violate_out: None,
        }
    }
}

impl EngineBuilder {
    /// Installs the expression backend. Required: [`build`](Self::build)
    /// fails without one. Go's `WithEvaluator`.
    pub fn evaluator(mut self, ev: impl Evaluator + 'static) -> Self {
        self.eval = Some(Box::new(ev));
        self
    }

    /// Installs the CEL backend. Go's `withcel.New`, split from the
    /// config so options compose in any order.
    pub fn cel(self) -> Self {
        self.evaluator(CelEvaluator::new())
    }

    /// Overrides [`default_principal_resolver`]. Go's
    /// `WithPrincipalResolver`.
    pub fn principal_resolver(
        mut self,
        f: impl Fn(&Context) -> Principal + Send + Sync + 'static,
    ) -> Self {
        self.princ = Arc::new(f);
        self
    }

    /// Wires the `stage` binding. Needed by any rule that references
    /// `stage`; without it those rules see an empty map. Go's
    /// `WithStageResolver`.
    pub fn stage_resolver(
        mut self,
        f: impl Fn(&str) -> Result<Map<String, Json>, Box<dyn StdError + Send + Sync>>
            + Send
            + Sync
            + 'static,
    ) -> Self {
        self.stage_res = Some(Arc::new(f));
        self
    }

    /// Overrides how the scope is read from an activation. Go's
    /// `WithScopeResolver`.
    pub fn scope_resolver(
        mut self,
        f: impl Fn(&Activation) -> String + Send + Sync + 'static,
    ) -> Self {
        self.scope_res = Some(Arc::new(f));
        self
    }

    /// Receives [`TOPIC_STAGE_VIOLATED`] on a stage-driven denial. Without
    /// one, denials still surface as [`PolicyDeniedError`]. Go's
    /// `WithViolationPublisher`.
    pub fn violation_publisher(mut self, p: Arc<dyn ViolationPublisher>) -> Self {
        self.violate_out = Some(p);
        self
    }

    /// Compiles every rule in `cfg`. A compile error surfaces here, not
    /// at the first event. Go's `NewEngine`.
    pub fn build(self, cfg: &Config) -> Result<Engine, Error> {
        let Some(mut eval) = self.eval else {
            return Err(Error::new(
                "policy: no evaluator configured (use EngineBuilder::cel or EngineBuilder::evaluator)",
            ));
        };
        let mut policies = Vec::with_capacity(cfg.policies.len());
        for p in &cfg.policies {
            eval.compile(&p.name, &p.when)
                .map_err(|e| Error::new(format!("policy: {e}")))?;
            policies.push((p.clone(), references_stage(&p.when)));
        }
        Ok(Engine {
            policies,
            eval,
            princ: self.princ,
            stage_res: self.stage_res,
            scope_res: self.scope_res,
            violate_out: self.violate_out,
        })
    }
}

/// Evaluates compiled rules against events. Go's `Engine`.
pub struct Engine {
    /// Each rule with whether it references `stage`.
    policies: Vec<(Policy, bool)>,
    eval: Box<dyn Evaluator>,
    pub(super) princ: PrincipalResolver,
    stage_res: Option<StageResolver>,
    scope_res: Option<ScopeResolver>,
    violate_out: Option<Arc<dyn ViolationPublisher>>,
}

impl Engine {
    /// A builder with no evaluator.
    pub fn builder() -> EngineBuilder {
        EngineBuilder::default()
    }

    /// An engine over `cfg` with the CEL backend and default options.
    /// Go's `withcel.New(cfg)`.
    pub fn with_cel(cfg: &Config) -> Result<Engine, Error> {
        Engine::builder().cel().build(cfg)
    }

    /// Evaluates every rule on `topic` against `activation`. `Ok` when
    /// allowed, including when no rule matches the topic.
    ///
    /// Composition is deny-overrides: every matching rule is evaluated
    /// (no short-circuit on an allow), so evaluator side effects see the
    /// whole set, and the first denying rule supplies the error. A rule
    /// whose expression errors denies, with `evaluation error: <cause>`
    /// as the message.
    ///
    /// When a stage-driven rule denies on a veto-able topic and a
    /// [`ViolationPublisher`] is wired, [`TOPIC_STAGE_VIOLATED`] is
    /// published before the error returns.
    pub fn decide(&self, topic: &str, mut activation: Activation) -> Result<(), PolicyDeniedError> {
        self.populate_stage_binding(&mut activation);

        let mut first_deny: Option<(PolicyDeniedError, bool)> = None;
        for (p, stage_driven) in &self.policies {
            if p.on != topic {
                continue;
            }
            let eff = match self.eval.eval(&p.name, &activation) {
                Err(e) => {
                    if first_deny.is_none() {
                        first_deny = Some((
                            PolicyDeniedError {
                                policy_name: p.name.clone(),
                                topic: topic.to_string(),
                                message: format!("evaluation error: {e}"),
                                decision: Decision::Deny,
                            },
                            *stage_driven,
                        ));
                    }
                    continue;
                }
                Ok(true) => p.effect,
                Ok(false) => p.otherwise,
            };
            if eff == Effect::Deny && first_deny.is_none() {
                first_deny = Some((
                    PolicyDeniedError {
                        policy_name: p.name.clone(),
                        topic: topic.to_string(),
                        message: p.message.clone(),
                        decision: Decision::Deny,
                    },
                    *stage_driven,
                ));
            }
        }
        match first_deny {
            Some((denied, stage_driven)) => {
                self.maybe_emit_violated(topic, &activation, stage_driven, &denied);
                Err(denied)
            }
            None => Ok(()),
        }
    }

    /// Stores the resolved `stage` binding in the activation: the
    /// resolver's map for the scope, else an empty map. Without a
    /// resolver an existing `stage` is kept.
    fn populate_stage_binding(&self, activation: &mut Activation) {
        let empty = || Json::Object(Map::new());
        let Some(res) = &self.stage_res else {
            activation.entry("stage").or_insert_with(empty);
            return;
        };
        let scope = self.resolve_scope(activation);
        if scope.is_empty() {
            activation.insert("stage".into(), empty());
            return;
        }
        match res(&scope) {
            Ok(st) => activation.insert("stage".into(), Json::Object(st)),
            Err(_) => activation.insert("stage".into(), empty()),
        };
    }

    fn resolve_scope(&self, activation: &Activation) -> String {
        if let Some(f) = &self.scope_res {
            return f(activation);
        }
        let str_at = |outer: &str, inner: &str| {
            activation
                .get(outer)
                .and_then(|o| o.get(inner))
                .and_then(Json::as_str)
                .filter(|s| !s.is_empty())
                .map(str::to_string)
        };
        str_at("payload", "scope")
            .or_else(|| str_at("resource", "id"))
            .unwrap_or_default()
    }

    fn maybe_emit_violated(
        &self,
        topic: &str,
        activation: &Activation,
        stage_driven: bool,
        denied: &PolicyDeniedError,
    ) {
        let Some(out) = &self.violate_out else { return };
        if !stage_driven || !VETO_TOPICS.contains(&topic) {
            return;
        }
        let text = |outer: &str, inner: &str| {
            activation
                .get(outer)
                .and_then(|o| o.get(inner))
                .and_then(Json::as_str)
                .unwrap_or_default()
                .to_string()
        };
        let mut entity = text("entity", "kind");
        if entity.is_empty() {
            entity = text("resource", "kind");
        }
        out.publish(
            TOPIC_STAGE_VIOLATED,
            &ViolationPayload {
                scope: self.resolve_scope(activation),
                stage: text("stage", "mode"),
                topic: topic.to_string(),
                entity,
                principal: text("principal", "id"),
                message: denied.message.clone(),
            },
        );
    }
}

/// Whether `expr` references the `stage` binding: a word-boundary scan
/// (`stage.mode`, `stage[`, `stage ==`; not `mystage` or `stage_x`).
/// Cheap and good enough; the engine does not parse CEL itself.
pub(super) fn references_stage(expr: &str) -> bool {
    const TOK: &[u8] = b"stage";
    let b = expr.as_bytes();
    let ident = |c: u8| c.is_ascii_alphanumeric() || c == b'_';
    (0..b.len()).any(|i| {
        b[i..].starts_with(TOK)
            && (i == 0 || !ident(b[i - 1]))
            && b.get(i + TOK.len()).is_none_or(|&c| !ident(c))
    })
}
