//! The delegation-safety policy: which side-effect classes a named
//! policy lets run, how many mutating operations one invocation may
//! perform, and which commands always ask for confirmation. Ports Go
//! `console/cli/policy` (policy, engine, loader, the shipped
//! `kit-default`); `permissions:` rules are carried and validated but
//! not evaluated (the expression evaluator is Go's `celpermission`,
//! which Rust does not have), and the served per-caller budget windows
//! are not ported (no Rust surface counts calls per principal).

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::Duration;

use serde::Deserialize;

/// The name of the policy kit ships; reserved, never a file.
pub const KIT_DEFAULT_NAME: &str = "kit-default";

/// Side-effect class ladders, legacy and expanded.
const KNOWN: &[&str] = &[
    "read",
    "write",
    "write-local",
    "write-shared",
    "destructive",
    "destructive-local",
    "destructive-shared",
    "interactive",
];

/// The legacy class of a side-effect tier; `None` for a value outside
/// kit's vocabulary.
pub fn class_of(tier: &str) -> Option<&str> {
    match tier {
        "read" | "write" | "destructive" | "interactive" => Some(tier),
        "write-local" | "write-shared" => Some("write"),
        "destructive-local" | "destructive-shared" => Some("destructive"),
        _ => None,
    }
}

/// Whether `tier` is a write tier.
pub fn is_write_like(tier: &str) -> bool {
    matches!(tier, "write" | "write-local" | "write-shared")
}

/// Whether `tier` is a destructive tier.
pub fn is_destructive_like(tier: &str) -> bool {
    matches!(
        tier,
        "destructive" | "destructive-local" | "destructive-shared"
    )
}

/// Whether `tier` names a side-effect tier kit knows.
pub fn is_valid_tier(tier: &str) -> bool {
    KNOWN.contains(&tier)
}

/// A named boolean expression over a served invocation (the
/// `permissions:` block). Carried and shape-checked only.
#[derive(Clone, Debug, Default, PartialEq, Eq, Deserialize)]
#[serde(default)]
pub struct PermissionRule {
    /// Identifies the rule; required and unique.
    pub name: String,
    /// The expression; required.
    pub when: String,
    /// Verdict when `when` holds: `allow` or `deny`.
    pub effect: String,
    /// Verdict when it does not.
    pub otherwise: String,
    /// The refusal's explanation.
    pub message: String,
}

/// A rule for the callers it matches, on served surfaces.
#[derive(Clone, Debug, Default, PartialEq, Eq, Deserialize)]
#[serde(default)]
pub struct CallerRule {
    /// Glob on the established principal.
    pub principal: String,
    /// Glob on the principal's tenant.
    pub tenant: String,
    /// A scope the credential must hold.
    pub scope: String,
    /// Answers the classes it declares.
    pub allow: BTreeMap<String, Vec<String>>,
    /// Mutating calls per window; carried, not counted here.
    pub max_ops: i64,
    /// The budget window, as a duration string (`24h`).
    #[serde(deserialize_with = "de_duration")]
    pub window: Duration,
    /// Classes admitted only for a command declaring `kit/permissions`.
    pub require_declared_scope: Vec<String>,
}

fn de_duration<'de, D: serde::Deserializer<'de>>(d: D) -> Result<Duration, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Raw {
        Text(String),
        Nanos(i64),
    }
    match Raw::deserialize(d)? {
        Raw::Nanos(n) => Ok(Duration::from_nanos(n.max(0) as u64)),
        Raw::Text(s) => parse_duration(&s).map_err(serde::de::Error::custom),
    }
}

/// Go's `time.ParseDuration` for the units policies use.
fn parse_duration(s: &str) -> Result<Duration, String> {
    let bad = || format!("time: invalid duration {s:?}");
    if s.is_empty() {
        return Ok(Duration::ZERO);
    }
    let mut rest = s;
    let mut total = 0f64;
    while !rest.is_empty() {
        let n_end = rest
            .find(|c: char| !(c.is_ascii_digit() || c == '.'))
            .ok_or_else(bad)?;
        let n: f64 = rest[..n_end].parse().map_err(|_| bad())?;
        rest = &rest[n_end..];
        let u_end = rest
            .find(|c: char| c.is_ascii_digit() || c == '.')
            .unwrap_or(rest.len());
        let secs = match &rest[..u_end] {
            "ns" => 1e-9,
            "us" | "µs" => 1e-6,
            "ms" => 1e-3,
            "s" => 1.0,
            "m" => 60.0,
            "h" => 3600.0,
            _ => return Err(bad()),
        };
        total += n * secs;
        rest = &rest[u_end..];
    }
    Ok(Duration::from_secs_f64(total))
}

/// Per-side-effect-class rules; loaded from
/// `$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml`.
///
/// `allow`: per-class verb globs; an empty list categorically refuses
/// that class. `max_ops`: cap on mutating operations (0 unlimited).
/// `require_confirm`: command-path globs that always ask.
#[derive(Clone, Debug, Default, PartialEq, Eq, Deserialize)]
#[serde(default)]
pub struct Policy {
    /// The policy's name (the file stem when the YAML omits it).
    pub name: String,
    /// Per-class allowed verb globs.
    pub allow: BTreeMap<String, Vec<String>>,
    /// Cap on mutating operations per invocation.
    pub max_ops: i64,
    /// Verb globs that always require confirmation.
    pub require_confirm: Vec<String>,
    /// Expression rules for served invocations (inert here).
    pub permissions: Vec<PermissionRule>,
    /// The class a command declaring no side effect is authorized as.
    pub unannotated: String,
    /// Per-caller rules, for served surfaces.
    pub callers: Vec<CallerRule>,
    /// Appended to every refusal; code only.
    #[serde(skip)]
    pub remedy: String,
}

/// The caller of a served call, as the transport established it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Caller {
    /// The established principal.
    pub principal: String,
    /// Its tenant.
    pub tenant: String,
    /// The scopes its credential holds.
    pub scopes: Vec<String>,
    /// Proven to hold the owner's authority (holds every scope).
    pub owner: bool,
}

impl Caller {
    fn holds(&self, scope: &str) -> bool {
        self.owner || self.scopes.iter().any(|s| s == scope)
    }
}

impl CallerRule {
    /// Whether the rule applies to `c`.
    pub fn matches(&self, c: &Caller) -> bool {
        (self.principal.is_empty() || glob(&self.principal, &c.principal))
            && (self.tenant.is_empty() || glob(&self.tenant, &c.tenant))
            && (self.scope.is_empty() || c.holds(&self.scope))
    }
}

fn glob(pattern: &str, value: &str) -> bool {
    pattern == "*" || path_match(pattern, value).unwrap_or(false)
}

impl Policy {
    /// The first rule matching `c`; `None` for no caller or no match.
    pub fn rule_for(&self, c: Option<&Caller>) -> Option<&CallerRule> {
        let c = c?;
        self.callers.iter().find(|r| r.matches(c))
    }

    /// The policy kit ships: a caller nobody established reads only; an
    /// established principal reads and writes, and runs a destructive
    /// command declaring a `kit/permissions` scope.
    pub fn kit_default() -> Policy {
        let wild = || vec!["*".to_string()];
        Policy {
            name: KIT_DEFAULT_NAME.into(),
            unannotated: "write".into(),
            allow: BTreeMap::from([
                ("write".into(), Vec::new()),
                ("destructive".into(), Vec::new()),
            ]),
            callers: vec![CallerRule {
                allow: BTreeMap::from([("write".into(), wild()), ("destructive".into(), wild())]),
                require_declared_scope: vec!["destructive".into()],
                ..CallerRule::default()
            }],
            remedy: "kit's default beyond loopback: writes need an authenticated caller, \
                     destructive commands a declared kit/permissions scope; name a --policy \
                     to choose otherwise"
                .into(),
            ..Policy::default()
        }
    }

    /// Shape checks on `permissions`, `unannotated` and `callers`: the
    /// only parts a load refuses on.
    pub fn validate(&self) -> Result<(), String> {
        let mut seen = std::collections::BTreeSet::new();
        for (i, r) in self.permissions.iter().enumerate() {
            if r.name.is_empty() {
                return Err(format!("permissions[{i}]: name required"));
            }
            if !seen.insert(r.name.clone()) {
                return Err(format!("permission rule {:?}: duplicate name", r.name));
            }
            if r.when.is_empty() {
                return Err(format!("permission rule {:?}: 'when' required", r.name));
            }
            for (field, v) in [("effect", &r.effect), ("otherwise", &r.otherwise)] {
                match v.as_str() {
                    "allow" | "deny" => {}
                    "" => {
                        return Err(format!(
                            "permission rule {:?}: {field} required (allow|deny)",
                            r.name
                        ))
                    }
                    other => {
                        return Err(format!(
                            "permission rule {:?}: {field} {other:?} invalid (want allow|deny)",
                            r.name
                        ))
                    }
                }
            }
        }
        let mut errs = Vec::new();
        if !self.unannotated.is_empty() && class_of(&self.unannotated).is_none() {
            errs.push(format!(
                "unannotated: unknown side-effect class {:?}",
                self.unannotated
            ));
        }
        for (i, r) in self.callers.iter().enumerate() {
            for (field, p) in [("principal", &r.principal), ("tenant", &r.tenant)] {
                if path_match(p, "").is_none() {
                    errs.push(format!(
                        "callers[{i}]: {field} {p:?}: syntax error in pattern"
                    ));
                }
            }
            for class in r.allow.keys() {
                if class_of(class).is_none() {
                    errs.push(format!(
                        "callers[{i}]: allow: unknown side-effect class {class:?}"
                    ));
                }
            }
            for class in &r.require_declared_scope {
                if class_of(class).is_none() {
                    errs.push(format!(
                        "callers[{i}]: require_declared_scope: unknown side-effect class {class:?}"
                    ));
                }
            }
            if r.max_ops < 0 {
                errs.push(format!(
                    "callers[{i}]: max_ops {} is negative; 0 means no budget",
                    r.max_ops
                ));
            }
        }
        if errs.is_empty() {
            Ok(())
        } else {
            Err(errs.join("\n"))
        }
    }
}

/// What the engine reads of a command: its path below the root, its
/// declared side-effect tier and its `kit/permissions` annotation.
#[derive(Clone, Copy, Debug)]
pub struct CommandRef<'a> {
    /// The command path minus the root name (`widget add`).
    pub verb: &'a str,
    /// The `kit/side-effect` value, when declared.
    pub side_effect: Option<&'a str>,
    /// The `kit/permissions` value, when declared.
    pub permissions: Option<&'a str>,
}

/// The authorization verdict for a command.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Verdict {
    /// Whether the command may run.
    pub allowed: bool,
    /// Whether the policy lists it under `require_confirm`.
    pub require_confirm: bool,
    /// The refusal text when `allowed` is false.
    pub reason: String,
}

/// Raised by [`Engine::record_op`] once the budget is spent.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct MaxOpsExceeded;

impl std::fmt::Display for MaxOpsExceeded {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("policy: max-ops budget exceeded")
    }
}

impl std::error::Error for MaxOpsExceeded {}

/// Per-invocation enforcement state: [`Engine::authorize`] each command
/// before it runs, [`Engine::record_op`] after a successful mutating
/// run. Not shared across invocations.
#[derive(Clone, Debug)]
pub struct Engine {
    policy: Policy,
    ops_count: i64,
    max_ops: i64,
}

impl Engine {
    /// An engine for `policy`; `max_ops_override > 0` takes precedence
    /// over the policy's own budget (the `--max-ops` flag).
    pub fn new(policy: Policy, max_ops_override: i64) -> Engine {
        let max_ops = if max_ops_override > 0 {
            max_ops_override
        } else {
            policy.max_ops
        };
        Engine {
            policy,
            ops_count: 0,
            max_ops,
        }
    }

    /// The policy in force.
    pub fn policy(&self) -> &Policy {
        &self.policy
    }

    /// The active budget; 0 is unlimited.
    pub fn max_ops(&self) -> i64 {
        self.max_ops
    }

    /// Mutating operations recorded so far.
    pub fn ops_count(&self) -> i64 {
        self.ops_count
    }

    /// [`Engine::authorize_for`] a caller nobody established.
    pub fn authorize(&self, cmd: &CommandRef<'_>) -> Verdict {
        self.authorize_for(cmd, None)
    }

    /// Whether `cmd` may run under the policy for caller `c`. A read
    /// command always may; a command declaring no side effect is
    /// deferred to the validator unless the policy names an
    /// `unannotated` class.
    pub fn authorize_for(&self, cmd: &CommandRef<'_>, c: Option<&Caller>) -> Verdict {
        let pass = |require_confirm| Verdict {
            allowed: true,
            require_confirm,
            reason: String::new(),
        };
        let Some(se) = self.side_effect(cmd) else {
            return pass(false);
        };
        if se == "read" {
            return pass(false);
        }
        let rule = self.policy.rule_for(c);
        if !self.allows(rule.map(|r| &r.allow), se, cmd.verb) {
            return self.refuse(format!("policy: {se} not allowed for {}", cmd.verb));
        }
        if let Some(r) = rule {
            if requires_declared_scope(r, se) && !declares_scope(cmd) {
                return self.refuse(format!(
                    "policy: {se} command {} declares no kit/permissions scope",
                    cmd.verb
                ));
            }
        }
        pass(match_any(&self.policy.require_confirm, cmd.verb))
    }

    fn refuse(&self, reason: String) -> Verdict {
        let reason = if self.policy.remedy.is_empty() {
            reason
        } else {
            format!(
                "{reason} (policy {}: {})",
                self.policy.name, self.policy.remedy
            )
        };
        Verdict {
            allowed: false,
            require_confirm: false,
            reason,
        }
    }

    fn side_effect<'a>(&'a self, cmd: &CommandRef<'a>) -> Option<&'a str> {
        cmd.side_effect.or(if self.policy.unannotated.is_empty() {
            None
        } else {
            Some(self.policy.unannotated.as_str())
        })
    }

    fn allows(
        &self,
        rule_allow: Option<&BTreeMap<String, Vec<String>>>,
        se: &str,
        verb: &str,
    ) -> bool {
        if let Some(a) = rule_allow {
            if let Some(patterns) = lookup_class(a, se) {
                return match_any(patterns, verb);
            }
        }
        match lookup_class(&self.policy.allow, se) {
            Some(patterns) => match_any(patterns, verb),
            None => true,
        }
    }

    /// Accounts one successful mutating operation; errors once the
    /// budget is exceeded.
    pub fn record_op(&mut self) -> Result<(), MaxOpsExceeded> {
        self.ops_count += 1;
        if self.max_ops > 0 && self.ops_count > self.max_ops {
            return Err(MaxOpsExceeded);
        }
        Ok(())
    }
}

fn lookup_class<'a>(allow: &'a BTreeMap<String, Vec<String>>, se: &str) -> Option<&'a Vec<String>> {
    if let Some(p) = allow.get(se) {
        return Some(p);
    }
    match class_of(se) {
        Some(class) if class != se => allow.get(class),
        _ => None,
    }
}

fn declares_scope(cmd: &CommandRef<'_>) -> bool {
    cmd.permissions
        .is_some_and(|p| !p.replace(',', "").trim().is_empty())
}

fn requires_declared_scope(r: &CallerRule, se: &str) -> bool {
    let class = class_of(se).unwrap_or(se);
    r.require_declared_scope
        .iter()
        .any(|w| w == se || w == class)
}

/// Whether `value` matches any pattern: `*` alone matches anything,
/// others are `path.Match` globs; `<verb>:*` is the prefix shorthand.
pub fn match_any(patterns: &[String], value: &str) -> bool {
    patterns.iter().any(|p| {
        if p == "*" {
            return true;
        }
        if path_match(p, value) == Some(true) {
            return true;
        }
        if let Some(prefix) = p.strip_suffix(":*") {
            return value == prefix || value.starts_with(&format!("{prefix} "));
        }
        false
    })
}

/// Go's `path.Match`: `*` any run of non-`/`, `?` one non-`/`
/// character, `[...]` a class, `\` an escape. `None` for a malformed
/// pattern.
pub fn path_match(pattern: &str, name: &str) -> Option<bool> {
    let p: Vec<char> = pattern.chars().collect();
    let n: Vec<char> = name.chars().collect();
    // Validate the pattern first, as Go does when the match fails early.
    check_pattern(&p)?;
    Some(match_from(&p, &n))
}

fn check_pattern(p: &[char]) -> Option<()> {
    let mut i = 0;
    while i < p.len() {
        match p[i] {
            '\\' => {
                i += 1;
                if i >= p.len() {
                    return None;
                }
            }
            '[' => {
                i += 1;
                if p.get(i) == Some(&'^') {
                    i += 1;
                }
                let mut nrange = 0;
                loop {
                    if p.get(i) == Some(&']') && nrange > 0 {
                        break;
                    }
                    if i >= p.len() {
                        return None;
                    }
                    if p[i] == '\\' {
                        i += 1;
                        if i >= p.len() {
                            return None;
                        }
                    }
                    i += 1;
                    if p.get(i) == Some(&'-') {
                        i += 1;
                        if p.get(i) == Some(&'\\') {
                            i += 1;
                        }
                        if i >= p.len() {
                            return None;
                        }
                        i += 1;
                    }
                    nrange += 1;
                }
            }
            _ => {}
        }
        i += 1;
    }
    Some(())
}

fn match_from(p: &[char], n: &[char]) -> bool {
    let Some((&c, rest)) = p.split_first() else {
        return n.is_empty();
    };
    match c {
        '*' => {
            // Longest run of non-'/' first, as Go backtracks.
            let mut k = 0;
            loop {
                if match_from(rest, &n[k..]) {
                    return true;
                }
                if k < n.len() && n[k] != '/' {
                    k += 1;
                } else {
                    return false;
                }
            }
        }
        '?' => matches!(n.first(), Some(&x) if x != '/') && match_from(rest, &n[1..]),
        '[' => {
            let Some(&x) = n.first() else { return false };
            let mut i = 0;
            let neg = rest.first() == Some(&'^');
            if neg {
                i += 1;
            }
            let mut hit = false;
            let mut nrange = 0;
            while !(rest.get(i) == Some(&']') && nrange > 0) {
                let (lo, ni) = class_char(rest, i);
                i = ni;
                let mut hi = lo;
                if rest.get(i) == Some(&'-') {
                    let (h, ni) = class_char(rest, i + 1);
                    hi = h;
                    i = ni;
                }
                if lo <= x && x <= hi {
                    hit = true;
                }
                nrange += 1;
            }
            hit != neg && match_from(&rest[i + 1..], &n[1..])
        }
        '\\' => {
            matches!((rest.first(), n.first()), (Some(a), Some(b)) if a == b)
                && match_from(&rest[1..], &n[1..])
        }
        c => n.first() == Some(&c) && match_from(rest, &n[1..]),
    }
}

fn class_char(p: &[char], mut i: usize) -> (char, usize) {
    if p.get(i) == Some(&'\\') {
        i += 1;
    }
    (p[i], i + 1)
}

/// Resolves a named policy to a [`Policy`].
pub type PolicyLoader = Arc<dyn Fn(&str) -> Result<Policy, String> + Send + Sync>;

/// A loader that resolves names against
/// `$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml`.
pub fn default_policy_loader(tool: &str) -> PolicyLoader {
    let tool = tool.to_string();
    Arc::new(move |name| load_named(&tool, name))
}

/// `<config home>/<tool>/policies/<name>.yaml`.
pub fn resolve(tool: &str, name: &str) -> Result<PathBuf, String> {
    if tool.is_empty() {
        return Err("policy: tool name required for XDG resolution".into());
    }
    if name.is_empty() {
        return Err("policy: policy name required".into());
    }
    Ok(crate::xdg::raw_config_dir(tool)
        .join("policies")
        .join(format!("{name}.yaml")))
}

/// [`resolve`] then [`load`]; the reserved [`KIT_DEFAULT_NAME`] is
/// [`Policy::kit_default`] and no file is read.
pub fn load_named(tool: &str, name: &str) -> Result<Policy, String> {
    if name == KIT_DEFAULT_NAME {
        return Ok(Policy::kit_default());
    }
    load(&resolve(tool, name)?)
}

/// Reads and validates a YAML policy file; the name defaults to the
/// file's stem.
pub fn load(path: &Path) -> Result<Policy, String> {
    let shown = path.display();
    let data = std::fs::read_to_string(path)
        .map_err(|e| format!("policy: load {shown}: {}", io_text(&e)))?;
    let mut p: Policy =
        serde_norway::from_str(&data).map_err(|e| format!("policy: parse {shown}: {e}"))?;
    p.validate()
        .map_err(|e| format!("policy: parse {shown}: {e}"))?;
    if p.name.is_empty() {
        let base = path.file_name().and_then(|s| s.to_str()).unwrap_or("");
        p.name = [".yaml", ".yml"]
            .iter()
            .find_map(|ext| base.strip_suffix(ext).filter(|s| !s.is_empty()))
            .unwrap_or(base)
            .to_string();
    }
    Ok(p)
}

/// Go's wording of an OS error (`open <path>: no such file or
/// directory` minus the prefix).
fn io_text(e: &std::io::Error) -> String {
    match e.kind() {
        std::io::ErrorKind::NotFound => "no such file or directory".into(),
        std::io::ErrorKind::PermissionDenied => "permission denied".into(),
        _ => e.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cmd<'a>(verb: &'a str, se: Option<&'a str>) -> CommandRef<'a> {
        CommandRef {
            verb,
            side_effect: se,
            permissions: None,
        }
    }

    fn policy(yaml: &str) -> Policy {
        let p: Policy = serde_norway::from_str(yaml).unwrap();
        p.validate().unwrap();
        p
    }

    #[test]
    fn empty_policy_permits_everything() {
        let e = Engine::new(Policy::default(), 0);
        for se in ["read", "write", "destructive", "interactive"] {
            assert!(e.authorize(&cmd("x", Some(se))).allowed, "{se}");
        }
        assert!(e.authorize(&cmd("x", None)).allowed);
    }

    #[test]
    fn empty_allow_list_refuses_the_class() {
        let e = Engine::new(policy("allow:\n  destructive: []\n"), 0);
        let v = e.authorize(&cmd("rm", Some("destructive")));
        assert!(!v.allowed);
        assert_eq!(v.reason, "policy: destructive not allowed for rm");
        assert!(e.authorize(&cmd("add", Some("write"))).allowed);
        assert!(e.authorize(&cmd("ls", Some("read"))).allowed);
    }

    #[test]
    fn allow_globs_and_colon_shorthand() {
        let e = Engine::new(policy("allow:\n  write: [\"widget:*\", \"sync\"]\n"), 0);
        assert!(e.authorize(&cmd("widget add", Some("write"))).allowed);
        assert!(e.authorize(&cmd("widget", Some("write"))).allowed);
        assert!(e.authorize(&cmd("sync", Some("write"))).allowed);
        assert!(!e.authorize(&cmd("other", Some("write"))).allowed);
    }

    #[test]
    fn expanded_tiers_fall_back_to_their_class() {
        let e = Engine::new(policy("allow:\n  write: []\n"), 0);
        assert!(!e.authorize(&cmd("a", Some("write-local"))).allowed);
        let e = Engine::new(policy("allow:\n  write: []\n  write-local: [\"*\"]\n"), 0);
        assert!(e.authorize(&cmd("a", Some("write-local"))).allowed);
        assert!(!e.authorize(&cmd("a", Some("write-shared"))).allowed);
    }

    #[test]
    fn require_confirm_is_reported() {
        let e = Engine::new(policy("require_confirm: [\"add\"]\n"), 0);
        let v = e.authorize(&cmd("add", Some("write")));
        assert!(v.allowed && v.require_confirm);
        assert!(!e.authorize(&cmd("ls", Some("write"))).require_confirm);
    }

    #[test]
    fn unannotated_class_applies_to_untagged_commands() {
        let e = Engine::new(policy("unannotated: write\nallow:\n  write: []\n"), 0);
        assert!(!e.authorize(&cmd("x", None)).allowed);
    }

    #[test]
    fn max_ops_override_beats_the_policy_and_counts_past_the_cap() {
        let mut e = Engine::new(policy("max_ops: 5\n"), 2);
        assert_eq!(e.max_ops(), 2);
        assert!(e.record_op().is_ok());
        assert!(e.record_op().is_ok());
        assert_eq!(e.record_op(), Err(MaxOpsExceeded));
        let mut unlimited = Engine::new(Policy::default(), 0);
        for _ in 0..100 {
            assert!(unlimited.record_op().is_ok());
        }
        let mut from_policy = Engine::new(policy("max_ops: 1\n"), 0);
        assert!(from_policy.record_op().is_ok());
        assert!(from_policy.record_op().is_err());
    }

    #[test]
    fn kit_default_refuses_an_unestablished_caller_with_the_remedy() {
        let e = Engine::new(Policy::kit_default(), 0);
        let v = e.authorize(&cmd("add", Some("write")));
        assert!(!v.allowed);
        assert!(v
            .reason
            .starts_with("policy: write not allowed for add (policy kit-default: "));
        // An untagged command is a write under it.
        assert!(!e.authorize(&cmd("x", None)).allowed);
        // An established principal writes; a destructive needs a scope.
        let who = Caller {
            principal: "svc".into(),
            ..Caller::default()
        };
        assert!(
            e.authorize_for(&cmd("add", Some("write")), Some(&who))
                .allowed
        );
        let no_scope = e.authorize_for(&cmd("rm", Some("destructive")), Some(&who));
        assert!(
            !no_scope.allowed
                && no_scope
                    .reason
                    .contains("declares no kit/permissions scope")
        );
        let scoped = CommandRef {
            verb: "rm",
            side_effect: Some("destructive"),
            permissions: Some("rm:delete"),
        };
        assert!(e.authorize_for(&scoped, Some(&who)).allowed);
    }

    #[test]
    fn caller_rules_match_by_glob_and_scope() {
        let p = policy(
            "callers:\n  - principal: \"svc-*\"\n    scope: w\n    allow:\n      write: [\"*\"]\nallow:\n  write: []\n",
        );
        let e = Engine::new(p, 0);
        let ok = Caller {
            principal: "svc-1".into(),
            scopes: vec!["w".into()],
            ..Caller::default()
        };
        assert!(e.authorize_for(&cmd("a", Some("write")), Some(&ok)).allowed);
        let wrong = Caller {
            principal: "svc-1".into(),
            ..Caller::default()
        };
        assert!(
            !e.authorize_for(&cmd("a", Some("write")), Some(&wrong))
                .allowed
        );
        let owner = Caller {
            principal: "svc-9".into(),
            owner: true,
            ..Caller::default()
        };
        assert!(
            e.authorize_for(&cmd("a", Some("write")), Some(&owner))
                .allowed
        );
    }

    #[test]
    fn validation_refuses_what_go_refuses() {
        let bad = |y: &str| {
            serde_norway::from_str::<Policy>(y)
                .unwrap()
                .validate()
                .unwrap_err()
        };
        assert!(bad("unannotated: nope\n").contains("unannotated: unknown side-effect class"));
        assert!(
            bad("callers:\n  - allow:\n      weird: []\n").contains("callers[0]: allow: unknown")
        );
        assert!(bad("callers:\n  - max_ops: -1\n").contains("max_ops -1 is negative"));
        assert!(
            bad("permissions:\n  - name: a\n    when: x\n    effect: allow\n")
                .contains("otherwise required")
        );
    }

    #[test]
    fn durations_parse_like_go() {
        assert_eq!(parse_duration("24h").unwrap(), Duration::from_secs(86400));
        assert_eq!(parse_duration("1h30m").unwrap(), Duration::from_secs(5400));
        assert_eq!(parse_duration("250ms").unwrap(), Duration::from_millis(250));
        assert!(parse_duration("5x").is_err());
    }

    #[test]
    fn path_match_is_gos() {
        assert_eq!(path_match("a*", "abc"), Some(true));
        assert_eq!(path_match("a*", "a/c"), Some(false));
        assert_eq!(path_match("a?c", "abc"), Some(true));
        assert_eq!(path_match("[a-c]x", "bx"), Some(true));
        assert_eq!(path_match("[^a-c]x", "bx"), Some(false));
        assert_eq!(path_match("\\*", "*"), Some(true));
        assert_eq!(path_match("[", "x"), None);
    }

    #[test]
    fn load_defaults_the_name_to_the_stem_and_validates() {
        let dir = tempfile::tempdir().unwrap();
        let f = dir.path().join("strict.yaml");
        std::fs::write(&f, "allow:\n  write: []\n").unwrap();
        assert_eq!(load(&f).unwrap().name, "strict");
        std::fs::write(&f, "name: custom\nunannotated: bogus\n").unwrap();
        assert!(load(&f).unwrap_err().contains("unannotated"));
        let missing = load(&dir.path().join("nope.yaml")).unwrap_err();
        assert!(missing.contains("no such file or directory"), "{missing}");
        assert_eq!(
            load_named("t", KIT_DEFAULT_NAME).unwrap().name,
            KIT_DEFAULT_NAME
        );
    }
}
