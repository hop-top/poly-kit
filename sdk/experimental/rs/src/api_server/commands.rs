//! The command-tree projection: `GET /v1/commands` lists the tree a kit
//! CLI defines, each invocable command is mounted under it, and the
//! OpenAPI document describes them. A port of `go/transport/api`'s
//! projection, built on [`cli::Root::model`](crate::cli::Root::model):
//! the projection reads the same kit-owned model any other surface
//! reads, never a tree of its own.
//!
//! A `read` command is `GET /v1/commands/<path>` with flags as query
//! parameters; every other tier is `POST` with `{"flags": {}, "args":
//! []}`. The same route plus `/stream` answers as `text/event-stream`.
//! A command that is not invocable (no handler, hidden, management,
//! builtin, interactive) is listed by discovery with its reason and is
//! not mounted. The command's exit class selects the HTTP status; see
//! [`status_for_exit_code`].

use std::collections::BTreeMap;
use std::convert::Infallible;
use std::io::Write;
use std::sync::Arc;

use axum::body::Body;
use axum::http::{HeaderMap, Method, Request};
use axum::response::sse::Event;
use axum::response::Response;
use axum::routing::on;
use axum::routing::MethodFilter;
use axum::Router;
use serde::Serialize;
use serde_json::{json, Map, Value};

use crate::cli::{CommandInfo, FlagInfo, Root};
use crate::output::EXIT_OK;

use super::error::ApiError;
use super::openapi::{OpenApi, Operation, Param, RequestBody, Response as OaResponse};

/// The versioned mount point of the projection.
pub const COMMAND_PROJECTION_PREFIX: &str = "/v1/commands";
/// Appended to a command's route for its event-stream twin.
pub const STREAM_SUFFIX: &str = "/stream";
/// OpenAPI extension carrying the side-effect class.
pub const EXT_SIDE_EFFECT: &str = "x-kit-side-effect";
/// OpenAPI extension carrying where the class came from.
pub const EXT_SIDE_EFFECT_SOURCE: &str = "x-kit-side-effect-source";

/// Refusal codes for a command that did not run.
pub const CODE_NOT_INVOCABLE: &str = "not_invocable";
/// Policy refused a destructive command on this surface.
pub const CODE_DESTRUCTIVE_BLOCKED: &str = "destructive_blocked";
/// The permission gate refused the caller.
pub const CODE_PERMISSION_DENIED: &str = "permission_denied";

/// The projection's view of a command's side-effect tier.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum SideEffectClass {
    /// No observable state change.
    Read,
    /// Reversible mutation.
    Write,
    /// Irreversible mutation.
    Destructive,
    /// Session-bound; never served.
    Interactive,
}

impl SideEffectClass {
    fn as_str(self) -> &'static str {
        match self {
            SideEffectClass::Read => "read",
            SideEffectClass::Write => "write",
            SideEffectClass::Destructive => "destructive",
            SideEffectClass::Interactive => "interactive",
        }
    }
}

/// Whether the class is the adopter's word or kit's stand-in.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum SideEffectSource {
    /// The `kit/side-effect` annotation.
    Declared,
    /// A destructive-looking name.
    Inferred,
    /// Nothing declared; projected as write.
    Unannotated,
}

impl SideEffectSource {
    fn as_str(self) -> &'static str {
        match self {
            SideEffectSource::Declared => "declared",
            SideEffectSource::Inferred => "inferred",
            SideEffectSource::Unannotated => "unannotated",
        }
    }
}

/// The HTTP method of a class: `GET` for read, `POST` otherwise.
pub fn method_for(c: SideEffectClass) -> &'static str {
    if c == SideEffectClass::Read {
        "GET"
    } else {
        "POST"
    }
}

/// `["widget","add"]` -> `/v1/commands/widget/add`.
pub fn route_for(path: &[String]) -> String {
    if path.is_empty() {
        COMMAND_PROJECTION_PREFIX.to_string()
    } else {
        format!("{COMMAND_PROJECTION_PREFIX}/{}", path.join("/"))
    }
}

/// `["widget","add"]` -> `commands_widget_add`.
pub fn operation_id_for(path: &[String]) -> String {
    if path.is_empty() {
        "commands_discover".into()
    } else {
        format!("commands_{}", path.join("_").replace('-', "_"))
    }
}

/// The HTTP status an exit code produces. Unmapped codes are 500.
pub fn status_for_exit_code(code: i32) -> u16 {
    match code {
        0 => 200,
        2 => 400,
        3 => 404,
        4 => 409,
        5 | 7 => 403,
        6 | 70 => 503,
        64 => 429,
        65 => 422,
        _ => 500,
    }
}

/// The exit-code to status table, ascending by code.
pub fn exit_status_table() -> Vec<(i32, u16)> {
    [0, 1, 2, 3, 4, 5, 6, 7, 64, 65, 70]
        .into_iter()
        .map(|c| (c, status_for_exit_code(c)))
        .collect()
}

/// A projected flag.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct CommandFlag {
    /// Long name.
    pub name: String,
    /// pflag type name (`string`, `bool`, `int`, ...).
    #[serde(rename = "type")]
    pub ty: String,
    /// Usage string.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub description: String,
    /// Default value.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub default: String,
    /// Whether the flag must be given.
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub required: bool,
    #[serde(skip)]
    enum_values: Vec<String>,
}

/// A projected positional argument.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct CommandArg {
    /// Name.
    pub name: String,
    /// Whether it must be given.
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub required: bool,
}

/// Everything the projection needs about one command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CommandDescriptor {
    /// The path below the root.
    pub path: Vec<String>,
    /// One-line description.
    pub summary: String,
    /// Long description.
    pub description: String,
    /// Resolved tier.
    pub side_effect: SideEffectClass,
    /// Where the tier came from.
    pub source: SideEffectSource,
    /// Declared flags.
    pub flags: Vec<CommandFlag>,
    /// Declared positionals.
    pub args: Vec<CommandArg>,
    /// Whether the command is mounted.
    pub invocable: bool,
    /// Why it is not, empty when it is.
    pub reason: String,
}

impl CommandDescriptor {
    /// The HTTP method.
    pub fn method(&self) -> &'static str {
        method_for(self.side_effect)
    }
    /// The route.
    pub fn route(&self) -> String {
        route_for(&self.path)
    }
    /// The streaming route.
    pub fn stream_route(&self) -> String {
        format!("{}{STREAM_SUFFIX}", self.route())
    }
    /// The space-joined path.
    pub fn name(&self) -> String {
        self.path.join(" ")
    }
}

const DESTRUCTIVE_NAMES: [&str; 6] = ["delete", "remove", "rm", "destroy", "purge", "drop"];

/// The descriptors of every command below the root, in tree order.
pub fn descriptors(root: &Root) -> Vec<CommandDescriptor> {
    let model = root.model();
    let mut out = Vec::new();
    model.walk(&mut |c| {
        // The root and pure groups are not calls; alias shims are not commands.
        // kit's own management commands (completion) are not part of
        // the author's tree; neither are the root and pure groups.
        let builtin = c
            .path
            .first()
            .is_some_and(|top| root.command_group(top).is_some());
        if !c.path.is_empty()
            && !builtin
            && c.commands.is_empty()
            && !root.aliases().contains_key(&c.path_string())
        {
            out.push(describe(root, c));
        }
    });
    out
}

fn describe(root: &Root, c: &CommandInfo) -> CommandDescriptor {
    let (class, source) = match c.side_effect() {
        Some("read") => (SideEffectClass::Read, SideEffectSource::Declared),
        Some(t) if t.starts_with("destructive") => {
            (SideEffectClass::Destructive, SideEffectSource::Declared)
        }
        Some("interactive") => (SideEffectClass::Interactive, SideEffectSource::Declared),
        Some(_) => (SideEffectClass::Write, SideEffectSource::Declared),
        None if DESTRUCTIVE_NAMES.contains(&c.name.as_str()) => {
            (SideEffectClass::Destructive, SideEffectSource::Inferred)
        }
        None => (SideEffectClass::Write, SideEffectSource::Unannotated),
    };
    let reason = if root.command_group(&c.name).is_some() && c.path.len() == 1 {
        "management-only"
    } else if c.hidden {
        "hidden-internal"
    } else if class == SideEffectClass::Interactive {
        "interactive"
    } else if !c.invocable {
        "not-runnable"
    } else {
        ""
    };
    let mut flags: Vec<CommandFlag> = c
        .flags
        .iter()
        .filter(|f| !f.hidden && !f.persistent && f.name != "help")
        .map(flag_of)
        .collect();
    flags.sort_by(|a, b| a.name.cmp(&b.name));
    CommandDescriptor {
        path: c.path.clone(),
        summary: c.short.clone(),
        description: c.long.clone(),
        side_effect: class,
        source,
        flags,
        args: c
            .args
            .iter()
            .map(|a| CommandArg {
                name: a.name.clone(),
                required: a.required,
            })
            .collect(),
        invocable: reason.is_empty(),
        reason: reason.to_string(),
    }
}

fn flag_of(f: &FlagInfo) -> CommandFlag {
    CommandFlag {
        name: f.name.clone(),
        ty: f.kind.type_name().to_string(),
        description: f.usage.clone(),
        default: f.default.clone().unwrap_or_default(),
        required: f.required,
        enum_values: f.enum_values.clone(),
    }
}

/// A per-command gate: the caller's request headers and the command;
/// `Err` refuses before the command runs.
pub type Gate = dyn Fn(&CommandDescriptor, &HeaderMap) -> Result<(), ApiError> + Send + Sync;

/// The projection of a kit CLI root onto HTTP.
#[derive(Clone)]
pub struct CommandProjection {
    root: Arc<Root>,
    tool: String,
    version: String,
    descriptors: Arc<Vec<CommandDescriptor>>,
    gate: Option<Arc<Gate>>,
    policy: Option<String>,
    max_ops: i64,
}

impl CommandProjection {
    /// Projects `root`'s command tree as it stands now.
    pub fn new(root: Arc<Root>, tool: &str, version: &str) -> Self {
        let descriptors = Arc::new(descriptors(&root));
        CommandProjection {
            root,
            tool: tool.into(),
            version: version.into(),
            descriptors,
            gate: None,
            policy: None,
            max_ops: 0,
        }
    }

    /// Fixes the delegation policy every call runs under (`--policy`).
    /// Only the operator sets it: a caller naming a policy is refused.
    pub fn with_policy(mut self, name: &str) -> Self {
        self.policy = Some(name.to_string());
        self
    }

    /// Sets the ceiling on mutating operations per call (`--max-ops`).
    /// A caller may ask for fewer, never more, and cannot lift it.
    pub fn with_max_ops(mut self, ceiling: i64) -> Self {
        self.max_ops = ceiling.max(0);
        self
    }

    /// Installs a per-command gate (policy, permission): a refusal is
    /// its [`ApiError`], the command not run.
    pub fn with_gate<F>(mut self, f: F) -> Self
    where
        F: Fn(&CommandDescriptor, &HeaderMap) -> Result<(), ApiError> + Send + Sync + 'static,
    {
        self.gate = Some(Arc::new(f));
        self
    }

    /// The descriptors, in tree order.
    pub fn descriptors(&self) -> &[CommandDescriptor] {
        &self.descriptors
    }

    /// The discovery document: every command, invocable first.
    pub fn discovery(&self) -> Value {
        let mut cmds: Vec<Value> = Vec::new();
        let mut reasons: Vec<String> = Vec::new();
        for d in self.descriptors.iter() {
            let mut e = Map::new();
            e.insert("path".into(), json!(d.path));
            e.insert("name".into(), json!(d.name()));
            if !d.summary.is_empty() {
                e.insert("summary".into(), json!(d.summary));
            }
            e.insert("side_effect".into(), json!(d.side_effect.as_str()));
            e.insert("side_effect_source".into(), json!(d.source.as_str()));
            e.insert("invocable".into(), json!(d.invocable));
            if d.invocable {
                e.insert("method".into(), json!(d.method()));
                e.insert("route".into(), json!(d.route()));
                e.insert("stream_route".into(), json!(d.stream_route()));
            } else {
                e.insert("reason".into(), json!(d.reason));
                if !d.reason.is_empty() && !reasons.contains(&d.reason) {
                    reasons.push(d.reason.clone());
                }
            }
            if !d.flags.is_empty() {
                e.insert("flags".into(), json!(d.flags));
            }
            if !d.args.is_empty() {
                e.insert("args".into(), json!(d.args));
            }
            cmds.push(Value::Object(e));
        }
        // invocable first, tree order kept within each group
        cmds.sort_by_key(|c| !c["invocable"].as_bool().unwrap_or(false));
        reasons.sort();
        let mut doc = Map::new();
        if !self.tool.is_empty() {
            doc.insert("tool".into(), json!(self.tool));
        }
        if !self.version.is_empty() {
            doc.insert("version".into(), json!(self.version));
        }
        doc.insert("prefix".into(), json!(COMMAND_PROJECTION_PREFIX));
        doc.insert("commands".into(), Value::Array(cmds));
        if !reasons.is_empty() {
            doc.insert("reasons".into(), json!(reasons));
        }
        doc.insert(
            "exit_status".into(),
            Value::Array(
                exit_status_table()
                    .into_iter()
                    .map(|(c, s)| json!({"exit_code": c, "status": s}))
                    .collect(),
            ),
        );
        Value::Object(doc)
    }

    /// The OpenAPI document of the projection: the discovery operation
    /// and, per invocable command, its operation and its stream twin.
    pub fn openapi(&self, title: &str, version: &str) -> OpenApi {
        let mut api = OpenApi::new(title, version);
        api.operation(Operation {
            operation_id: operation_id_for(&[]),
            method: "GET".into(),
            path: COMMAND_PROJECTION_PREFIX.into(),
            summary: "List every command, mounted or withheld".into(),
            tags: vec!["commands".into()],
            responses: BTreeMap::from([(
                "200".into(),
                OaResponse {
                    description: "Command listing".into(),
                    schema: Some(json!({"type": "object"})),
                },
            )]),
            ..Operation::default()
        });
        for d in self.descriptors.iter().filter(|d| d.invocable) {
            api.operation(command_operation(d, false));
            api.operation(command_operation(d, true));
        }
        api
    }

    /// An axum router serving discovery and every invocable command
    /// (and its `/stream` twin). Merge it into the adopter's routes
    /// before [`ApiService::build`](super::ApiService::build): the
    /// service's stack (auth, rate limit, body limit) then applies.
    pub fn router(&self) -> Router {
        let this = self.clone();
        let mut r = Router::new().route(
            COMMAND_PROJECTION_PREFIX,
            on(MethodFilter::GET, move || {
                let doc = this.discovery();
                async move { super::error::json_response(200, &doc) }
            }),
        );
        for d in self.descriptors.iter().filter(|d| d.invocable) {
            let filter = if d.method() == "GET" {
                MethodFilter::GET
            } else {
                MethodFilter::POST
            };
            for stream in [false, true] {
                let (p, d2) = (self.clone(), d.clone());
                let route = if stream { d.stream_route() } else { d.route() };
                r = r.route(
                    &route,
                    on(filter, move |req: Request<Body>| {
                        let (p, d) = (p.clone(), d2.clone());
                        async move { p.serve(&d, req, stream).await }
                    }),
                );
            }
        }
        r
    }

    async fn serve(&self, d: &CommandDescriptor, req: Request<Body>, stream: bool) -> Response {
        if let Some(g) = &self.gate {
            if let Err(e) = g(d, req.headers()) {
                return e.into_response();
            }
        }
        let (parts, body) = req.into_parts();
        let bytes = match axum::body::to_bytes(body, 1 << 20).await {
            Ok(b) => b,
            Err(e) => return bad_request(e.to_string()),
        };
        let (mut argv, caller_max) = match decode(d, &parts.method, parts.uri.query(), &bytes) {
            Ok(a) => a,
            Err(m) => return bad_request(m),
        };
        // Operator controls, ahead of the command's own flags. A call
        // never prompts (the server's terminal is not the caller's) and
        // never approves itself: --confirm is fixed at no.
        let mut ops = vec!["--confirm=no".to_string()];
        if let Some(p) = &self.policy {
            ops.push(format!("--policy={p}"));
        }
        let max = match (caller_max, self.max_ops) {
            (Some(c), o) if c > 0 && o > 0 => c.min(o),
            (Some(c), 0) if c > 0 => c,
            (_, o) => o,
        };
        if max > 0 {
            ops.push(format!("--max-ops={max}"));
        }
        // Ahead of the command path, so they bind to the root's
        // persistent flags and never to a leaf-local flag of the same name.
        argv.splice(0..0, ops);
        let root = self.root.clone();
        let mut full = vec![root.model().name];
        full.extend(argv);
        if stream {
            return run_stream(root, full);
        }
        let joined = tokio::task::spawn_blocking(move || run_captured(&root, &full)).await;
        match joined {
            Ok(res) => {
                let status = status_for_exit_code(res.exit_code);
                super::error::json_response(status, &res.to_value())
            }
            Err(e) => ApiError::new(500, "server_error", e.to_string()).into_response(),
        }
    }
}

fn bad_request(msg: String) -> Response {
    ApiError::new(400, "bad_request", msg).into_response()
}

struct Captured {
    exit_code: i32,
    stdout: String,
    stderr: String,
}

impl Captured {
    fn to_value(&self) -> Value {
        let mut m = Map::new();
        m.insert("exit_code".into(), json!(self.exit_code));
        if let Ok(v @ (Value::Object(_) | Value::Array(_))) = serde_json::from_str(&self.stdout) {
            m.insert("data".into(), v);
        }
        if !self.stdout.is_empty() {
            m.insert("stdout".into(), json!(self.stdout));
        }
        if !self.stderr.is_empty() {
            m.insert("stderr".into(), json!(self.stderr));
        }
        Value::Object(m)
    }
}

fn run_captured(root: &Root, argv: &[String]) -> Captured {
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let exit_code = match root.execute(argv, &mut out, &mut err) {
        Ok(()) => EXIT_OK,
        Err(e) => e.exit_code,
    };
    Captured {
        exit_code,
        stdout: String::from_utf8_lossy(&out).into_owned(),
        stderr: String::from_utf8_lossy(&err).into_owned(),
    }
}

/// A `Write` that forwards each write to the event stream and keeps a
/// copy for the final result.
struct Tee {
    tx: tokio::sync::mpsc::UnboundedSender<(&'static str, String)>,
    kind: &'static str,
    buf: Vec<u8>,
}

impl Write for Tee {
    fn write(&mut self, b: &[u8]) -> std::io::Result<usize> {
        self.buf.extend_from_slice(b);
        let _ = self
            .tx
            .send((self.kind, String::from_utf8_lossy(b).into_owned()));
        Ok(b.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn run_stream(root: Arc<Root>, argv: Vec<String>) -> Response {
    let (tx, rx) = tokio::sync::mpsc::unbounded_channel::<(&'static str, String)>();
    let done = tx.clone();
    tokio::task::spawn_blocking(move || {
        let mut out = Tee {
            tx: tx.clone(),
            kind: "stdout",
            buf: Vec::new(),
        };
        let mut err = Tee {
            tx: tx.clone(),
            kind: "stderr",
            buf: Vec::new(),
        };
        let exit_code = match root.execute(&argv, &mut out, &mut err) {
            Ok(()) => EXIT_OK,
            Err(e) => e.exit_code,
        };
        let res = Captured {
            exit_code,
            stdout: String::from_utf8_lossy(&out.buf).into_owned(),
            stderr: String::from_utf8_lossy(&err.buf).into_owned(),
        };
        let mut v = res.to_value();
        v["status"] = json!(status_for_exit_code(exit_code));
        let _ = done.send(("result", v.to_string()));
    });
    let events = futures_util::stream::unfold(Some(rx), |st| async move {
        let mut rx = st?;
        let (kind, data) = rx.recv().await?;
        let last = kind == "result";
        let ev = if last {
            Event::default().event("result").data(data)
        } else {
            let payload = json!({"kind": kind, "data": data, "at": rfc3339_now()});
            Event::default().event("event").data(payload.to_string())
        };
        Some((Ok::<_, Infallible>(ev), (!last).then_some(rx)))
    });
    super::sse::sse(events)
}

/// The current UTC time as RFC 3339 (seconds).
fn rfc3339_now() -> String {
    let secs = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    let (days, rem) = (secs.div_euclid(86_400), secs.rem_euclid(86_400));
    // civil-from-days (Howard Hinnant)
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

/// The root's delegation controls, by what a caller may do with them.
/// `dry-run` only makes a call safer and is the caller's; `max-ops` may
/// only tighten the operator's ceiling; `policy` and `confirm` are the
/// operator's and a caller naming either is refused (a call never
/// approves itself: an unconfirmed destructive command is the command's
/// own refusal, exit 5, status 403).
const CONTROL_FLAGS: &[&str] = &["confirm", "max-ops", "policy", "dry-run"];

/// The typed confirmation token flag (the CLI gate's `confirm-token`);
/// never accepted from a caller.
const CONFIRM_TOKEN: &str = "confirm-token";

/// Builds the argv tail for a request, or the refusal text.
fn decode(
    d: &CommandDescriptor,
    method: &Method,
    query: Option<&str>,
    body: &[u8],
) -> Result<(Vec<String>, Option<i64>), String> {
    let by_name: BTreeMap<&str, &CommandFlag> =
        d.flags.iter().map(|f| (f.name.as_str(), f)).collect();
    let mut flags: Vec<(String, Value)> = Vec::new();
    let mut args: Vec<String> = Vec::new();
    for (k, v) in parse_query(query.unwrap_or("")) {
        if k == "arg" {
            args.push(v);
            continue;
        }
        let Some(f) = by_name.get(k.as_str()) else {
            if CONTROL_FLAGS.contains(&k.as_str()) {
                flags.push((k.clone(), control_value(&k, &v)));
            }
            continue;
        };
        flags.push((k, coerce(f, &v)?));
    }
    if method != Method::GET && !body.is_empty() {
        let parsed: Value = serde_json::from_slice(body).map_err(|e| e.to_string())?;
        if let Some(fm) = parsed.get("flags").and_then(Value::as_object) {
            for (k, v) in fm {
                if !by_name.contains_key(k.as_str()) && !CONTROL_FLAGS.contains(&k.as_str()) {
                    return Err(format!("unknown flag: {k}"));
                }
                flags.push((k.clone(), v.clone()));
            }
        }
        if let Some(a) = parsed.get("args").and_then(Value::as_array) {
            args = a
                .iter()
                .map(|v| v.as_str().unwrap_or_default().to_string())
                .collect();
        }
    }
    let need = d.args.iter().filter(|a| a.required).count();
    if args.len() < need {
        return Err(format!(
            "missing required argument: {}",
            d.args[args.len()].name
        ));
    }
    let mut argv: Vec<String> = d.path.clone();
    let mut caller_max = None;
    for (k, v) in flags {
        // Delegation controls belong to the operator whether or not the
        // leaf also declares a flag of that name: a leaf-local `--confirm`
        // would otherwise shadow the root's and let a caller approve
        // itself, and the typed token is derivable from the path.
        match k.as_str() {
            "policy" | "confirm" | CONFIRM_TOKEN => {
                return Err(format!("{k} is set by the operator, not the caller"))
            }
            "max-ops" => {
                caller_max = Some(
                    scalar(&v)
                        .parse::<i64>()
                        .map_err(|_| "max-ops must be an integer".to_string())?,
                );
                continue;
            }
            _ => {}
        }
        match v {
            Value::Bool(true) => argv.push(format!("--{k}")),
            Value::Bool(false) | Value::Null => {}
            Value::Array(items) => {
                for i in items {
                    argv.push(format!("--{k}={}", scalar(&i)));
                }
            }
            other => argv.push(format!("--{k}={}", scalar(&other))),
        }
    }
    if !args.is_empty() {
        argv.push("--".into());
        argv.extend(args);
    }
    Ok((argv, caller_max))
}

/// A control flag's value from the query: `dry-run` is a switch, the
/// rest take their text.
fn control_value(name: &str, raw: &str) -> Value {
    if name == "dry-run" {
        return Value::Bool(raw != "false");
    }
    Value::String(raw.to_string())
}

fn scalar(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        other => other.to_string(),
    }
}

fn coerce(f: &CommandFlag, raw: &str) -> Result<Value, String> {
    Ok(match f.ty.as_str() {
        "bool" => match raw {
            "" => Value::Bool(true),
            _ => Value::Bool(
                raw.parse::<bool>()
                    .map_err(|_| format!("{}: invalid boolean: {raw}", f.name))?,
            ),
        },
        "int" | "uint" | "count" => json!(raw
            .parse::<i64>()
            .map_err(|_| format!("{}: invalid integer: {raw}", f.name))?),
        "float" => json!(raw
            .parse::<f64>()
            .map_err(|_| format!("{}: invalid number: {raw}", f.name))?),
        "stringArray" => json!(raw.split(',').collect::<Vec<_>>()),
        _ => json!(raw),
    })
}

fn parse_query(q: &str) -> Vec<(String, String)> {
    q.split('&')
        .filter(|p| !p.is_empty())
        .map(|p| {
            let (k, v) = p.split_once('=').unwrap_or((p, ""));
            (pct(k), pct(v))
        })
        .collect()
}

fn pct(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'+' => out.push(b' '),
            b'%' if i + 2 < b.len() => match u8::from_str_radix(&s[i + 1..i + 3], 16) {
                Ok(v) => {
                    out.push(v);
                    i += 2;
                }
                Err(_) => out.push(b'%'),
            },
            c => out.push(c),
        }
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn flag_schema(f: &CommandFlag) -> Value {
    let mut s = match f.ty.as_str() {
        "bool" => json!({"type": "boolean"}),
        "int" | "uint" | "count" => json!({"type": "integer"}),
        "float" => json!({"type": "number"}),
        "stringArray" => json!({"type": "array", "items": {"type": "string"}}),
        _ => json!({"type": "string"}),
    };
    if !f.enum_values.is_empty() {
        s["enum"] = json!(f.enum_values);
    }
    s
}

fn command_operation(d: &CommandDescriptor, stream: bool) -> Operation {
    let arg_names = d
        .args
        .iter()
        .map(|a| a.name.as_str())
        .collect::<Vec<_>>()
        .join(", ");
    let mut op = Operation {
        operation_id: if stream {
            format!("stream_{}", operation_id_for(&d.path))
        } else {
            operation_id_for(&d.path)
        },
        method: d.method().into(),
        path: if stream { d.stream_route() } else { d.route() },
        summary: if d.summary.is_empty() {
            format!("Invoke {}", d.name())
        } else {
            d.summary.clone()
        },
        description: d.description.clone(),
        tags: vec!["commands".into()],
        extensions: BTreeMap::from([
            (EXT_SIDE_EFFECT.into(), json!(d.side_effect.as_str())),
            (EXT_SIDE_EFFECT_SOURCE.into(), json!(d.source.as_str())),
        ]),
        ..Operation::default()
    };
    if d.method() == "GET" {
        for f in &d.flags {
            op.parameters.push(Param {
                name: f.name.clone(),
                location: "query".into(),
                required: f.required,
                description: f.description.clone(),
                schema: Some(flag_schema(f)),
            });
        }
        if !d.args.is_empty() {
            op.parameters.push(Param {
                name: "arg".into(),
                location: "query".into(),
                required: d.args.iter().any(|a| a.required),
                description: format!("Positional argument; repeat in order: {arg_names}"),
                schema: Some(json!({"type": "array", "items": {"type": "string"}})),
            });
        }
    } else {
        let props: Map<String, Value> = d
            .flags
            .iter()
            .map(|f| (f.name.clone(), flag_schema(f)))
            .collect();
        let required: Vec<&str> = d
            .flags
            .iter()
            .filter(|f| f.required)
            .map(|f| f.name.as_str())
            .collect();
        let mut flags_schema = json!({"type": "object", "properties": props});
        if !required.is_empty() {
            flags_schema["required"] = json!(required);
        }
        op.request_body = Some(RequestBody {
            description: "Command flags and positional arguments".into(),
            required: !required.is_empty() || d.args.iter().any(|a| a.required),
            schema: Some(json!({"type": "object", "properties": {
                "flags": flags_schema,
                "args": {"type": "array", "items": {"type": "string"}, "description": arg_names},
            }})),
        });
    }
    let err = json!({"type": "object", "properties": {
        "status": {"type": "integer"}, "code": {"type": "string"}, "message": {"type": "string"}}});
    let result = json!({"type": "object", "properties": {
        "exit_code": {"type": "integer"}, "data": {}, "stdout": {"type": "string"}, "stderr": {"type": "string"}}});
    if stream {
        op.responses.insert(
            "200".into(),
            OaResponse {
                description: "text/event-stream of event and result frames".into(),
                schema: None,
            },
        );
    } else {
        op.responses.insert(
            "200".into(),
            OaResponse {
                description: "Command completed".into(),
                schema: Some(result),
            },
        );
    }
    for (code, desc) in [
        ("400", "Malformed request, or the command exited USAGE"),
        (
            "403",
            "Refused by policy, or the command exited UNAUTHORIZED",
        ),
        ("500", "Command failed"),
    ] {
        op.responses.insert(
            code.into(),
            OaResponse {
                description: desc.into(),
                schema: Some(err.clone()),
            },
        );
    }
    op
}

/// Maps a command refusal that never ran (gate) to its API error.
pub fn refusal(code: &str, message: &str) -> ApiError {
    let status = match code {
        CODE_NOT_INVOCABLE => 404,
        _ => 403,
    };
    ApiError::new(status, code, message)
}
