//! Secret, token and PII redaction: a port of `go/core/redact`.
//!
//! A [`Redactor`] holds compiled [`Rule`]s with gitleaks semantics
//! (keyword gate, secret group, entropy floor, allowlists) and one
//! [`Replacement`] strategy. [`Redactor::apply`] finds every rule's
//! secret spans on the original text, merges overlapping and adjacent
//! spans (earliest start wins, then the longest, then the rule added
//! first) and replaces them in one pass: a replacement is never scanned
//! again.
//!
//! Allowlists have two modes. [`Redactor::scan`] is detection and
//! follows gitleaks (substring regexes, stopwords). [`Redactor::apply`]
//! and [`Redactor::spans`] fail closed: stopwords never exempt, and a
//! regex exempts only when it matches the whole target.
//!
//! Patterns are Go RE2 syntax, compiled with Go's meaning (ASCII `\d`,
//! `\s`, `\w`, `\b`; `\Q…\E`); see `syntax.rs`. A pattern the `regex`
//! crate cannot express fails the build of the rule, never silently.
//!
//! [`embedded`] and [`shared`] carry the default policy: the gitleaks
//! v8.30.1 corpus with kit's patches plus the Presidio PII pack, as
//! exported by Go (`contracts/redact-v1/rules.json`). There is no
//! environment override. No I/O: wasm32-safe. `Send + Sync`.

mod rule;
mod spans;
mod syntax;

use std::borrow::Cow;
use std::collections::HashMap;
use std::fmt;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

use regex::RegexSet;
use sha2::{Digest, Sha256};

use rule::Allowlist;
pub use rule::{AllowlistSpec, Policy, Rule, RuleSpec, POLICY_VERSION};

/// The default policy, byte-identical to `contracts/redact-v1/rules.json`.
pub const RULES_JSON: &str = include_str!("rules.json");

const MASK: &str = "***REDACTED***";

/// A redaction failure: a rule or allowlist that does not compile, or a
/// policy this version cannot read.
#[derive(Debug, Clone, PartialEq)]
pub enum Error {
    Rule { id: String, msg: String },
    Allowlist(String),
    Policy(String),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Rule { id, msg } => write!(f, "redact: rule {id}: {msg}"),
            Error::Allowlist(m) => write!(f, "redact: global allowlist: {m}"),
            Error::Policy(m) => write!(f, "redact: policy: {m}"),
        }
    }
}

impl std::error::Error for Error {}

/// A user formatter for [`Replacement::Custom`].
pub type Formatter = Arc<dyn Fn(&Match) -> String + Send + Sync>;
/// An observer of redactions or allowlist exemptions.
pub type Observer = Arc<dyn Fn(&Match) + Send + Sync>;

/// How a region is rewritten.
#[derive(Clone, Default)]
pub enum Replacement {
    /// `***REDACTED***`.
    #[default]
    Mask,
    /// The rule's label, else `<rule-id>`.
    Tag,
    /// `sha256:` and the first 4 bytes of the region's digest in hex.
    Hash,
    /// A formatter; a panic in it degrades to `Mask`.
    Custom(Formatter),
}

impl fmt::Debug for Replacement {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Replacement::Mask => "Mask",
            Replacement::Tag => "Tag",
            Replacement::Hash => "Hash",
            Replacement::Custom(_) => "Custom",
        })
    }
}

/// A redaction (observers), a finding (`scan`) or an exemption
/// (`on_allowed`). Offsets are bytes of the input.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Match {
    pub rule_id: String,
    pub original: String,
    pub replacement: String,
    pub start: usize,
    pub end: usize,
}

/// A merged region `apply` replaces, in bytes of the input.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Span {
    pub start: usize,
    pub end: usize,
    pub rule_id: String,
}

/// A snapshot of activity. `matches` counts replaced regions;
/// `allowed` counts exemptions, which leave text unredacted.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Stats {
    pub rules: usize,
    pub matches: u64,
    pub allowed: u64,
    pub by_rule: HashMap<String, u64>,
}

/// Applies rules to text. Configure with `&mut self`, then share: every
/// `&self` method is safe to call concurrently.
pub struct Redactor {
    rules: Vec<Rule>,
    strategy: Replacement,
    exact: Vec<String>,
    allowlists: Vec<Allowlist>,
    allowlist_specs: Vec<AllowlistSpec>,
    need_line: bool,
    gate: Gate,
    observers: Vec<Observer>,
    allowed_obs: Vec<Observer>,
    matches: AtomicU64,
    allowed: AtomicU64,
    by_rule: Mutex<HashMap<String, u64>>,
}

/// The keyword gate: one set over every distinct keyword, matched on
/// the lower-cased input; `rule_keys[i]` lists rule i's keyword ids.
#[derive(Default)]
struct Gate {
    keywords: Vec<String>,
    rule_keys: Vec<Vec<usize>>,
    set: Option<RegexSet>,
}

impl Default for Redactor {
    fn default() -> Self {
        Self::new()
    }
}

impl fmt::Debug for Redactor {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Redactor")
            .field("rules", &self.rules.len())
            .field("strategy", &self.strategy)
            .finish_non_exhaustive()
    }
}

impl Redactor {
    /// An empty redactor with the `Mask` strategy.
    pub fn new() -> Self {
        Redactor {
            rules: Vec::new(),
            strategy: Replacement::Mask,
            exact: Vec::new(),
            allowlists: Vec::new(),
            allowlist_specs: Vec::new(),
            need_line: false,
            gate: Gate::default(),
            observers: Vec::new(),
            allowed_obs: Vec::new(),
            matches: AtomicU64::new(0),
            allowed: AtomicU64::new(0),
            by_rule: Mutex::new(HashMap::new()),
        }
    }

    /// Builds from a policy. Strict: anything that does not compile fails.
    pub fn from_policy(p: &Policy) -> Result<Self, Error> {
        if !p.version.is_empty() && p.version != POLICY_VERSION {
            return Err(Error::Policy(format!(
                "version {:?}, want {POLICY_VERSION:?}",
                p.version
            )));
        }
        let rules = p
            .rules
            .iter()
            .cloned()
            .map(Rule::compile)
            .collect::<Result<Vec<_>, _>>()?;
        let mut r = Redactor::new();
        r.add_allowlists(&p.allowlists)?;
        r.allow_exact(p.allowlist_exact.iter().cloned());
        r.add_rules(rules);
        Ok(r)
    }

    /// Appends a whole-match rule.
    pub fn add_rule(
        &mut self,
        id: &str,
        pattern: &str,
        replacement: &str,
    ) -> Result<&mut Self, Error> {
        let rule = Rule::new(id, pattern, replacement)?;
        Ok(self.add_rules([rule]))
    }

    /// Appends compiled rules.
    pub fn add_rules(&mut self, rules: impl IntoIterator<Item = Rule>) -> &mut Self {
        for rule in rules {
            self.need_line |= rule.allowlists.iter().any(Allowlist::targets_line);
            let ids = rule
                .spec
                .keywords
                .iter()
                .map(|k| match self.gate.keywords.iter().position(|x| x == k) {
                    Some(i) => i,
                    None => {
                        self.gate.keywords.push(k.clone());
                        self.gate.keywords.len() - 1
                    }
                })
                .collect();
            self.gate.rule_keys.push(ids);
            self.rules.push(rule);
        }
        let pats: Vec<String> = self
            .gate
            .keywords
            .iter()
            .map(|k| regex::escape(k))
            .collect();
        self.gate.set = if pats.is_empty() {
            None
        } else {
            Some(RegexSet::new(pats).expect("escaped literals compile"))
        };
        self
    }

    /// Allowlists for every rule, on top of each rule's own.
    pub fn add_allowlists(&mut self, specs: &[AllowlistSpec]) -> Result<&mut Self, Error> {
        let compiled = specs
            .iter()
            .map(Allowlist::compile)
            .collect::<Result<Vec<_>, _>>()
            .map_err(Error::Allowlist)?;
        self.need_line |= compiled.iter().any(Allowlist::targets_line);
        self.allowlists.extend(compiled);
        self.allowlist_specs.extend(specs.iter().cloned());
        Ok(self)
    }

    /// Exempts findings whose secret or whole match equals an entry.
    /// Empty entries are ignored.
    pub fn allow_exact(&mut self, vals: impl IntoIterator<Item = String>) -> &mut Self {
        self.exact.extend(vals);
        self
    }

    pub fn set_replacement(&mut self, strategy: Replacement) -> &mut Self {
        self.strategy = strategy;
        self
    }

    /// Fired once per replaced region, before substitution: it sees the
    /// secret. Count it; never persist it.
    pub fn on_match(&mut self, f: impl Fn(&Match) + Send + Sync + 'static) -> &mut Self {
        self.observers.push(Arc::new(f));
        self
    }

    /// Fired for every finding an allowlist exempted during `apply`.
    pub fn on_allowed(&mut self, f: impl Fn(&Match) + Send + Sync + 'static) -> &mut Self {
        self.allowed_obs.push(Arc::new(f));
        self
    }

    pub fn rules(&self) -> &[Rule] {
        &self.rules
    }

    /// The configuration as data; strategy and observers excluded.
    pub fn policy(&self) -> Policy {
        Policy {
            version: POLICY_VERSION.into(),
            allowlists: self.allowlist_specs.clone(),
            allowlist_exact: self.exact.clone(),
            rules: self.rules.iter().map(|r| r.spec.clone()).collect(),
        }
    }

    /// `s` with every secret region replaced; borrowed when nothing is.
    pub fn apply<'a>(&self, s: &'a str) -> Cow<'a, str> {
        if self.rules.is_empty() || s.is_empty() {
            return Cow::Borrowed(s);
        }
        let regions = spans::merge(spans::collect(self, s, spans::Mode::RedactRecord));
        if regions.is_empty() {
            return Cow::Borrowed(s);
        }
        let mut out = String::with_capacity(s.len());
        let mut last = 0;
        for g in regions {
            let rule = &self.rules[g.rule];
            let mut m = Match {
                rule_id: rule.spec.id.clone(),
                original: s[g.start..g.end].to_string(),
                replacement: String::new(),
                start: g.start,
                end: g.end,
            };
            m.replacement = self.format(rule, &m);
            for f in &self.observers {
                f(&m);
            }
            self.record(&rule.spec.id);
            out.push_str(&s[last..g.start]);
            out.push_str(&m.replacement);
            last = g.end;
        }
        out.push_str(&s[last..]);
        Cow::Owned(out)
    }

    /// The merged regions `apply` would replace, without side effects.
    pub fn spans(&self, s: &str) -> Vec<Span> {
        spans::merge(spans::collect(self, s, spans::Mode::RedactQuiet))
            .into_iter()
            .map(|g| Span {
                start: g.start,
                end: g.end,
                rule_id: self.rules[g.rule].spec.id.clone(),
            })
            .collect()
    }

    /// Every finding before merging, in rule order then position, with
    /// gitleaks detection semantics. Records nothing.
    pub fn scan(&self, s: &str) -> Vec<Match> {
        spans::collect(self, s, spans::Mode::Detect)
            .into_iter()
            .map(|h| Match {
                rule_id: self.rules[h.rule].spec.id.clone(),
                original: s[h.start..h.end].to_string(),
                replacement: String::new(),
                start: h.start,
                end: h.end,
            })
            .collect()
    }

    pub fn stats(&self) -> Stats {
        Stats {
            rules: self.rules.len(),
            matches: self.matches.load(Ordering::Relaxed),
            allowed: self.allowed.load(Ordering::Relaxed),
            by_rule: self.by_rule.lock().map(|m| m.clone()).unwrap_or_default(),
        }
    }

    fn record(&self, id: &str) {
        self.matches.fetch_add(1, Ordering::Relaxed);
        if let Ok(mut m) = self.by_rule.lock() {
            *m.entry(id.to_string()).or_default() += 1;
        }
    }

    fn record_allowed(&self, rule: &Rule, secret: &str, start: usize, end: usize) {
        self.allowed.fetch_add(1, Ordering::Relaxed);
        if self.allowed_obs.is_empty() {
            return;
        }
        let m = Match {
            rule_id: rule.spec.id.clone(),
            original: secret.into(),
            replacement: String::new(),
            start,
            end,
        };
        for f in &self.allowed_obs {
            f(&m);
        }
    }

    fn format(&self, rule: &Rule, m: &Match) -> String {
        match &self.strategy {
            Replacement::Mask => MASK.into(),
            Replacement::Tag if !rule.spec.replacement.is_empty() => rule.spec.replacement.clone(),
            Replacement::Tag => format!("<{}>", rule.spec.id),
            Replacement::Hash => {
                let d = Sha256::digest(m.original.as_bytes());
                format!("sha256:{:02x}{:02x}{:02x}{:02x}", d[0], d[1], d[2], d[3])
            }
            Replacement::Custom(f) => {
                catch_unwind(AssertUnwindSafe(|| f(m))).unwrap_or_else(|_| MASK.into())
            }
        }
    }
}

/// The default policy: [`RULES_JSON`] parsed.
pub fn embedded_policy() -> Result<Policy, Error> {
    serde_json::from_str(RULES_JSON).map_err(|e| Error::Policy(e.to_string()))
}

/// A fresh redactor with the default policy, to configure.
pub fn embedded() -> Result<Redactor, Error> {
    Redactor::from_policy(&embedded_policy()?)
}

/// The process-wide default redactor (`Mask`), built on first use.
///
/// # Panics
/// When the embedded policy does not compile: a build defect, caught by
/// the crate's tests.
pub fn shared() -> &'static Redactor {
    static R: OnceLock<Redactor> = OnceLock::new();
    R.get_or_init(|| embedded().expect("embedded redact policy compiles"))
}
