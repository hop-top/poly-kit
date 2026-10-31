//! Pick tracing: Go's `llm.pick` and `llm.classify` log events, as
//! structured values handed to a sink.

use std::sync::Arc;

use super::{BudgetTier, Model, RequestProfile};

/// One trace event: a message and ordered `key=value` attributes.
#[derive(Debug, Clone, PartialEq)]
pub struct TraceEvent {
    /// `llm.pick` or `llm.classify`.
    pub msg: &'static str,
    pub attrs: Vec<(String, String)>,
}

impl std::fmt::Display for TraceEvent {
    /// `msg=llm.pick picker.budget=cheap ...`, as Go's text handler.
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "msg={}", self.msg)?;
        for (k, v) in &self.attrs {
            write!(f, " {k}={v}")?;
        }
        Ok(())
    }
}

type Sink = Arc<dyn Fn(&TraceEvent) + Send + Sync>;
type EnvFn = Arc<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// Where events go and when they are on.
#[derive(Clone)]
pub struct Trace {
    sink: Option<Sink>,
    env: Option<EnvFn>,
}

impl Trace {
    /// Never traces.
    pub fn off() -> Self {
        Trace {
            sink: None,
            env: None,
        }
    }

    /// Traces to `sink` whenever `LLM_PICKER_TRACE` is truthy (`1`,
    /// `true`, `on`, `yes`; case-insensitive), read through `env` on each
    /// pick so it can flip without a restart.
    pub fn from_env(
        env: impl Fn(&str) -> Option<String> + Send + Sync + 'static,
        sink: impl Fn(&TraceEvent) + Send + Sync + 'static,
    ) -> Self {
        Trace {
            sink: Some(Arc::new(sink)),
            env: Some(Arc::new(env)),
        }
    }

    pub(crate) fn enabled(&self) -> bool {
        let (Some(env), Some(_)) = (&self.env, &self.sink) else {
            return false;
        };
        matches!(
            env("LLM_PICKER_TRACE")
                .map(|v| v.trim().to_lowercase())
                .as_deref(),
            Some("1" | "true" | "on" | "yes")
        )
    }

    fn emit(&self, e: TraceEvent) {
        if let Some(s) = &self.sink {
            s(&e);
        }
    }

    /// `winner` is `None` for a no-match.
    pub(crate) fn pick(
        &self,
        p: &RequestProfile,
        budget: BudgetTier,
        candidates: usize,
        eliminated: usize,
        winner: Option<&Model>,
    ) {
        let tri = |b: Option<bool>| b.map_or("<nil>".to_string(), |b| b.to_string());
        let mut a: Vec<(String, String)> = vec![
            ("picker.budget".into(), budget.as_str().into()),
            ("picker.filter.tool_call".into(), tri(p.filter.tool_call)),
            ("picker.filter.reasoning".into(), tri(p.filter.reasoning)),
            (
                "picker.filter.structured_output".into(),
                tri(p.filter.structured_output),
            ),
            (
                "picker.filter.temperature".into(),
                tri(p.filter.temperature),
            ),
        ];
        if !p.filter.provider.is_empty() {
            a.push(("picker.filter.provider".into(), p.filter.provider.clone()));
        }
        if !p.filter.family.is_empty() {
            a.push(("picker.filter.family".into(), p.filter.family.clone()));
        }
        if !p.filter.input.is_empty() {
            a.push(("picker.filter.input".into(), p.filter.input.join(",")));
        }
        if !p.filter.output.is_empty() {
            a.push(("picker.filter.output".into(), p.filter.output.join(",")));
        }
        if p.max_input_tokens > 0 {
            a.push((
                "picker.profile.max_input_tokens".into(),
                p.max_input_tokens.to_string(),
            ));
        }
        if p.max_output_tokens > 0 {
            a.push((
                "picker.profile.max_output_tokens".into(),
                p.max_output_tokens.to_string(),
            ));
        }
        a.push(("picker.candidate_count".into(), candidates.to_string()));
        a.push(("picker.eliminated_count".into(), eliminated.to_string()));
        match winner {
            None => a.push(("picker.outcome".into(), "no_match".into())),
            Some(w) => {
                a.push(("picker.outcome".into(), "matched".into()));
                a.push(("picker.chosen.provider".into(), w.provider.clone()));
                a.push(("picker.chosen.model".into(), w.id.clone()));
            }
        }
        self.emit(TraceEvent {
            msg: "llm.pick",
            attrs: a,
        });
    }

    pub(crate) fn classify(
        &self,
        verdict: Option<&super::Classification>,
        fallback: Option<&'static str>,
    ) {
        let mut a = Vec::new();
        match fallback {
            None => a.push(("classifier.outcome".into(), "classified".into())),
            Some(r) => {
                a.push(("classifier.outcome".into(), "fallback".into()));
                a.push(("classifier.fallback_reason".into(), r.into()));
            }
        }
        if let Some(v) = verdict.filter(|v| !v.label.is_empty()) {
            a.push(("classifier.label".into(), v.label.clone()));
            a.push(("classifier.confidence".into(), v.confidence.to_string()));
        }
        self.emit(TraceEvent {
            msg: "llm.classify",
            attrs: a,
        });
    }
}
