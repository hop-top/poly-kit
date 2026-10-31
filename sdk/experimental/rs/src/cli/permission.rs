//! The permission gate: evaluates the `permissions:` rules of a policy
//! file for one served invocation. Ports Go `console/cli/celpermission`;
//! the expression language is CEL, as for the policy engine, and the
//! interpreter stays behind this file (feature `cli-permissions`): the
//! contract below names no provider type.
//!
//! Each rule sees one invocation through these bindings:
//!
//! | binding | value |
//! |---------|-------|
//! | `principal.id` / `.tenant` | caller and tenant |
//! | `principal.scopes` | the verified credential's scopes; `[]` unless [`CallerSource::Verified`] |
//! | `principal.established` | whether the transport established the caller |
//! | `principal.source` | `"verified"`, `"transport"` or `"none"` |
//! | `resource.kind` | `"command"` |
//! | `resource.id` / `.path` / `.tier` | the command path space-joined, as a list, and its side-effect tier |
//! | `context.surface` | the surface the call arrived on (`"rest"`, `"mcp"`, ...) |
//! | `context.client_addr` | the caller's host as the transport recorded it, port removed; `""` when none |
//! | `payload.args` / `.flags` | the positional arguments and the flags the caller set |
//!
//! `entity` and `stage` are declared and empty. Across rules deny
//! overrides: the first denying rule, in declaration order, names the
//! refusal. A rule whose expression fails to evaluate (a flag the
//! caller did not set read without `has()`, a non-boolean result)
//! denies. A rule that does not compile, or reads a name that is not a
//! binding, fails [`PermissionGate::new`] naming the rule.
//!
//! Unlike Go's `cel-go` there is no static type check, so a mistyped
//! operand fails at evaluation, where it denies, rather than at load.

use std::collections::BTreeMap;
use std::sync::Arc;

use cel::common::ast::{EntryExpr, Expr, IdedExpr};
use cel::{Context, Env, Program, Value as CelValue};
use serde_json::{json, Map, Value};

use super::policy::{validate_permission_rules, PermissionRule};

/// The names a rule may read.
const BINDINGS: &[&str] = &[
    "principal",
    "resource",
    "context",
    "payload",
    "entity",
    "stage",
];

/// How the caller was established, in the words rules compare against.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum CallerSource {
    /// A verifier checked a credential; its scopes are the caller's.
    Verified,
    /// The transport vouched for the caller (a peer credential).
    Transport,
    /// Nobody established the caller; whatever it claims is a claim.
    #[default]
    None,
}

impl CallerSource {
    fn as_str(self) -> &'static str {
        match self {
            CallerSource::Verified => "verified",
            CallerSource::Transport => "transport",
            CallerSource::None => "none",
        }
    }

    fn established(self) -> bool {
        self != CallerSource::None
    }
}

/// One served invocation, as the rules see it.
#[derive(Clone, Debug, Default)]
pub struct PermissionRequest {
    /// The caller's id.
    pub caller: String,
    /// The caller's tenant.
    pub tenant: String,
    /// The scopes the credential carries; bound only when `source` is
    /// [`CallerSource::Verified`].
    pub scopes: Vec<String>,
    /// How the caller was established.
    pub source: CallerSource,
    /// The surface the call arrived on.
    pub surface: String,
    /// The peer address as the transport recorded it (`host:port`).
    pub remote_addr: String,
    /// The command path below the root.
    pub path: Vec<String>,
    /// The command's side-effect tier; empty when undeclared.
    pub tier: String,
    /// The positional arguments.
    pub args: Vec<String>,
    /// The flags the caller set, by long name.
    pub flags: BTreeMap<String, Value>,
}

/// The gate's verdict.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PermissionDecision {
    /// Whether the invocation may run.
    pub allowed: bool,
    /// The refusal's reason, `permission rule "<name>": <message>`;
    /// empty when allowed.
    pub reason: String,
}

struct CompiledRule {
    name: String,
    message: String,
    on_match: bool,
    otherwise: bool,
    program: Program,
}

/// The compiled `permissions:` rules of a policy. Rules can only narrow
/// what the policy's other checks admitted: run the gate after them.
pub struct PermissionGate {
    env: Arc<Env>,
    rules: Vec<CompiledRule>,
}

impl std::fmt::Debug for PermissionGate {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PermissionGate")
            .field("rules", &self.rules.len())
            .finish()
    }
}

impl PermissionGate {
    /// Validates and compiles `rules`. Every rule is compiled here, so a
    /// rule that cannot run fails construction with an error naming it
    /// rather than denying at the first call. No rules yields a gate
    /// that permits everything.
    pub fn new(rules: &[PermissionRule]) -> Result<PermissionGate, String> {
        validate_permission_rules(rules)?;
        let env = Arc::new(Env::stdlib());
        let mut compiled = Vec::with_capacity(rules.len());
        for r in rules {
            let program = compile(&env, &r.when)
                .map_err(|e| format!("permission rule {:?} does not compile: {e}", r.name))?;
            let mut free = Vec::new();
            free_idents(program.expression(), &mut Vec::new(), &mut free);
            for v in free {
                if !BINDINGS.contains(&v.as_str()) {
                    return Err(format!(
                        "permission rule {:?} does not compile: undeclared reference to '{v}'",
                        r.name
                    ));
                }
            }
            compiled.push(CompiledRule {
                name: r.name.clone(),
                message: r.message.clone(),
                on_match: r.effect == "allow",
                otherwise: r.otherwise == "allow",
                program,
            });
        }
        Ok(PermissionGate {
            env,
            rules: compiled,
        })
    }

    /// Whether `req` may run. Deny overrides across rules.
    pub fn decide(&self, req: &PermissionRequest) -> PermissionDecision {
        let activation = activation(req);
        let mut ctx = Context::with_env(Arc::clone(&self.env));
        let mut first: Option<String> = None;
        for (k, v) in &activation {
            if let Err(e) = ctx.add_variable(k.as_str(), v) {
                return refuse(
                    &self.rules.first().map_or("", |r| &r.name),
                    &format!("evaluation error: binding {k}: {e}"),
                );
            }
        }
        for r in &self.rules {
            let allowed = match r.program.execute(&ctx) {
                Ok(CelValue::Bool(b)) => {
                    if b {
                        r.on_match
                    } else {
                        r.otherwise
                    }
                }
                Ok(_) => {
                    first.get_or_insert_with(|| {
                        refuse_text(&r.name, "evaluation error: expression did not return bool")
                    });
                    continue;
                }
                Err(e) => {
                    first.get_or_insert_with(|| {
                        refuse_text(&r.name, &format!("evaluation error: {e}"))
                    });
                    continue;
                }
            };
            if !allowed {
                first.get_or_insert_with(|| refuse_text(&r.name, &r.message));
            }
        }
        match first {
            Some(reason) => PermissionDecision {
                allowed: false,
                reason,
            },
            None => PermissionDecision {
                allowed: true,
                reason: String::new(),
            },
        }
    }
}

fn refuse(name: &str, msg: &str) -> PermissionDecision {
    PermissionDecision {
        allowed: false,
        reason: refuse_text(name, msg),
    }
}

fn refuse_text(name: &str, msg: &str) -> String {
    if msg.is_empty() {
        format!("permission rule {name:?}")
    } else {
        format!("permission rule {name:?}: {msg}")
    }
}

/// Compiles `src`; a parser panic is a defect in the dependency, but
/// boot-time config must fail loud rather than abort the host.
fn compile(env: &Arc<Env>, src: &str) -> Result<Program, String> {
    let env = Arc::clone(env);
    let src = src.to_string();
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| env.compile(&src)))
        .map_err(|_| "parser panic".to_string())?
        .map_err(|e| e.to_string())
}

fn activation(req: &PermissionRequest) -> Map<String, Value> {
    let verified = req.source == CallerSource::Verified;
    let scopes: Vec<&String> = if verified {
        req.scopes.iter().collect()
    } else {
        Vec::new()
    };
    let mut m = Map::new();
    m.insert(
        "principal".into(),
        json!({
            "id": req.caller,
            "tenant": req.tenant,
            "scopes": scopes,
            "established": req.source.established(),
            "source": req.source.as_str(),
        }),
    );
    m.insert(
        "resource".into(),
        json!({
            "kind": "command",
            "id": req.path.join(" "),
            "path": req.path,
            "tier": req.tier,
        }),
    );
    m.insert(
        "context".into(),
        json!({
            "surface": req.surface,
            "client_addr": client_host(&req.remote_addr),
        }),
    );
    m.insert(
        "payload".into(),
        json!({ "args": req.args, "flags": req.flags }),
    );
    m.insert("entity".into(), json!({}));
    m.insert("stage".into(), json!({}));
    m
}

/// The caller's address without its port: each connection has its own
/// ephemeral port, which no rule can usefully name. Go's
/// `net.SplitHostPort`, falling back to the input.
fn client_host(addr: &str) -> String {
    if let Some(rest) = addr.strip_prefix('[') {
        if let Some((host, tail)) = rest.split_once(']') {
            if tail.starts_with(':') {
                return host.to_string();
            }
        }
        return addr.to_string();
    }
    match addr.split_once(':') {
        Some((host, port)) if !port.contains(':') => host.to_string(),
        _ => addr.to_string(),
    }
}

/// The identifiers `e` reads that no comprehension in `e` binds. The
/// interpreter's own reference list also names a macro's loop
/// variables, which a rule may use freely.
fn free_idents(e: &IdedExpr, bound: &mut Vec<String>, out: &mut Vec<String>) {
    match &e.expr {
        Expr::Ident(n) => {
            if !n.starts_with('@') && !bound.contains(n) && !out.contains(n) {
                out.push(n.clone());
            }
        }
        Expr::Call(c) => {
            if let Some(t) = &c.target {
                free_idents(t, bound, out);
            }
            for a in &c.args {
                free_idents(a, bound, out);
            }
        }
        Expr::Comprehension(c) => {
            free_idents(&c.iter_range, bound, out);
            free_idents(&c.accu_init, bound, out);
            let mark = bound.len();
            bound.push(c.iter_var.clone());
            bound.extend(c.iter_var2.clone());
            bound.push(c.accu_var.clone());
            for part in [&c.loop_cond, &c.loop_step, &c.result] {
                free_idents(part, bound, out);
            }
            bound.truncate(mark);
        }
        Expr::List(l) => l.elements.iter().for_each(|x| free_idents(x, bound, out)),
        Expr::Map(m) => entries(m.entries.iter().map(|x| &x.expr), bound, out),
        Expr::Struct(st) => entries(st.entries.iter().map(|x| &x.expr), bound, out),
        Expr::Select(sel) => free_idents(&sel.operand, bound, out),
        Expr::Literal(_) | Expr::Unspecified => {}
    }
}

fn entries<'a>(
    it: impl Iterator<Item = &'a EntryExpr>,
    bound: &mut Vec<String>,
    out: &mut Vec<String>,
) {
    for e in it {
        match e {
            EntryExpr::StructField(f) => free_idents(&f.value, bound, out),
            EntryExpr::MapEntry(m) => {
                free_idents(&m.key, bound, out);
                free_idents(&m.value, bound, out);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::client_host;

    #[test]
    fn the_port_is_removed_and_anything_else_is_kept() {
        for (addr, want) in [
            ("10.1.2.3:51234", "10.1.2.3"),
            ("[::1]:8080", "::1"),
            ("host.example:80", "host.example"),
            ("10.1.2.3", "10.1.2.3"),
            ("::1", "::1"),
            ("[::1]", "[::1]"),
            ("", ""),
        ] {
            assert_eq!(client_host(addr), want, "{addr}");
        }
    }
}
