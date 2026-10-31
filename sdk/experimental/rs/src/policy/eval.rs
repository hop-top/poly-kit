//! The expression backend seam and its CEL implementation. Go's
//! `Evaluator` and the `policy/cel` + `policy/withcel` packages.

use std::collections::HashMap;
use std::fmt;
use std::sync::Arc;

use cel::{Context, Env, Program, Value};
use serde_json::{Map, Value as Json};

/// The activation an expression runs against: `principal`, `resource`,
/// `entity`, `context`, `payload` and `stage`, as JSON objects. Go's
/// `map[string]any`.
pub type Activation = Map<String, Json>;

/// Why an expression did not compile or evaluate.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct EvalError(pub String);

impl fmt::Display for EvalError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for EvalError {}

/// The pluggable expression backend: compile each rule at boot, then
/// evaluate it by name per event. [`CelEvaluator`] is the CEL backend; a
/// Cedar or OPA one would take the same shape.
pub trait Evaluator: Send + Sync {
    /// Parses `expr` and caches it under `name`. Compiling a name again
    /// replaces it.
    fn compile(&mut self, name: &str, expr: &str) -> Result<(), EvalError>;

    /// Runs the program cached under `name`; whether the rule matched.
    fn eval(&self, name: &str, activation: &Activation) -> Result<bool, EvalError>;
}

/// The CEL backend, over `cel-rust`. Go's `policy/cel.Evaluator`.
///
/// Unlike Go's `cel-go` there is no static type check: the six
/// activation names are not declared, so an unknown identifier or a
/// wrongly typed operand fails at evaluation, where [`Engine::decide`]
/// turns it into a denial ("evaluation error: ...").
///
/// [`Engine::decide`]: super::Engine::decide
pub struct CelEvaluator {
    env: Arc<Env>,
    progs: HashMap<String, Program>,
}

impl CelEvaluator {
    /// A fresh evaluator over the CEL standard library.
    pub fn new() -> Self {
        CelEvaluator {
            env: Arc::new(Env::stdlib()),
            progs: HashMap::new(),
        }
    }
}

impl Default for CelEvaluator {
    fn default() -> Self {
        Self::new()
    }
}

impl Evaluator for CelEvaluator {
    fn compile(&mut self, name: &str, expr: &str) -> Result<(), EvalError> {
        // The parser reports syntax errors as values; a panic inside it
        // would be a defect in the dependency, but boot-time config must
        // fail loud, not abort the host, so it is contained.
        let env = Arc::clone(&self.env);
        let src = expr.to_string();
        let parsed = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| env.compile(&src)))
            .map_err(|_| {
            EvalError(format!(
                "policy/cel: compile {}: parser panic",
                crate::domain::quote::quote(name)
            ))
        })?;
        let prog = parsed.map_err(|e| {
            EvalError(format!(
                "policy/cel: compile {}: {e}",
                crate::domain::quote::quote(name)
            ))
        })?;
        self.progs.insert(name.to_string(), prog);
        Ok(())
    }

    fn eval(&self, name: &str, activation: &Activation) -> Result<bool, EvalError> {
        let q = crate::domain::quote::quote(name);
        let prog = self
            .progs
            .get(name)
            .ok_or_else(|| EvalError(format!("policy/cel: no compiled program for {q}")))?;
        let mut ctx = Context::with_env(Arc::clone(&self.env));
        for (k, v) in activation {
            ctx.add_variable(k.as_str(), v)
                .map_err(|e| EvalError(format!("policy/cel: eval {q}: binding {k}: {e}")))?;
        }
        match prog.execute(&ctx) {
            Ok(Value::Bool(b)) => Ok(b),
            Ok(other) => Err(EvalError(format!(
                "policy/cel: {q} did not return bool (got {})",
                type_name(&other)
            ))),
            Err(e) => Err(EvalError(format!("policy/cel: eval {q}: {e}"))),
        }
    }
}

fn type_name(v: &Value) -> &'static str {
    match v {
        Value::Bool(_) => "bool",
        Value::Int(_) => "int",
        Value::UInt(_) => "uint",
        Value::Float(_) => "double",
        Value::String(_) => "string",
        Value::Bytes(_) => "bytes",
        Value::List(_) => "list",
        Value::Map(_) => "map",
        Value::Null => "null",
        _ => "value",
    }
}
