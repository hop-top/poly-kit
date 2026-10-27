//! Rules and allowlists as data (the `contracts/redact-v1` policy
//! format) and their compiled forms.

use regex::{Regex, RegexBuilder};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use super::{syntax, Error};

/// The policy format this module reads.
pub const POLICY_VERSION: &str = "v1";

/// A whole redactor configuration: global allowlists and exact entries,
/// then rules in evaluation order. `rules.json` is the default one.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    #[serde(default)]
    pub version: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub allowlists: Vec<AllowlistSpec>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub allowlist_exact: Vec<String>,
    #[serde(default)]
    pub rules: Vec<RuleSpec>,
}

/// A rule as data, following the gitleaks rule schema.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuleSpec {
    pub id: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    /// A Go RE2 pattern (see the module docs for how it is compiled).
    pub regex: String,
    /// The `Tag` label; empty means `<id>`.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub replacement: String,
    /// The capture group redacted; 0 redacts the whole match.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub secret_group: usize,
    /// When non-zero, a secret at or below this Shannon entropy is dropped.
    #[serde(default, skip_serializing_if = "is_zero_f")]
    pub entropy: f64,
    /// The rule runs only when the lower-cased input holds one of these.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub keywords: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tags: Vec<String>,
    /// The gitleaks file-path scope; carried, never consulted.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub path: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub allowlists: Vec<AllowlistSpec>,
    /// Literals compared against the whole secret and the whole match.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub allowlist_exact: Vec<String>,
}

/// A gitleaks allowlist. `Redactor::scan` applies gitleaks detection
/// semantics; `apply` and `spans` fail closed (no stopwords, regexes
/// must match the whole target). Paths and commits never hold.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AllowlistSpec {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    /// "OR" (or empty) or "AND".
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub condition: String,
    /// "secret" (or empty), "match" or "line".
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub regex_target: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub regexes: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub stopwords: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub paths: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub commits: Vec<String>,
}

fn is_zero(n: &usize) -> bool {
    *n == 0
}

fn is_zero_f(n: &f64) -> bool {
    *n == 0.0
}

/// Compiles a Go RE2 pattern with Go's meaning. Patterns the regex
/// crate cannot express are errors.
pub(crate) fn compile_go(pattern: &str) -> Result<Regex, String> {
    let translated = syntax::translate(pattern)?;
    RegexBuilder::new(&translated)
        .size_limit(64 << 20)
        .dfa_size_limit(16 << 20)
        .build()
        .map_err(|e| e.to_string())
}

/// A compiled rule. Read-only once built.
#[derive(Clone, Debug)]
pub struct Rule {
    pub(crate) spec: RuleSpec,
    pub(crate) re: Regex,
    pub(crate) allowlists: Vec<Allowlist>,
    pub(crate) min_len: usize,
}

impl Rule {
    /// A rule redacting the whole match of `pattern`.
    pub fn new(id: &str, pattern: &str, replacement: &str) -> Result<Self, Error> {
        Self::compile(RuleSpec {
            id: id.into(),
            regex: pattern.into(),
            replacement: replacement.into(),
            ..RuleSpec::default()
        })
    }

    /// Validates and compiles `spec`.
    pub fn compile(mut spec: RuleSpec) -> Result<Self, Error> {
        let bad = |msg: String| Error::Rule {
            id: spec.id.clone(),
            msg,
        };
        if spec.id.is_empty() {
            return Err(Error::Rule {
                id: String::new(),
                msg: "rule id is required".into(),
            });
        }
        let re = compile_go(&spec.regex).map_err(bad)?;
        let groups = re.captures_len() - 1;
        if spec.secret_group > groups {
            return Err(bad(format!(
                "secret group {} out of range 0..{groups}",
                spec.secret_group
            )));
        }
        if spec.entropy < 0.0 || spec.entropy.is_nan() {
            return Err(bad(format!("invalid entropy {}", spec.entropy)));
        }
        if spec.keywords.iter().any(String::is_empty) {
            return Err(bad("empty keyword".into()));
        }
        spec.keywords = spec.keywords.iter().map(|k| go_lower(k)).collect();
        let allowlists = spec
            .allowlists
            .iter()
            .map(Allowlist::compile)
            .collect::<Result<Vec<_>, _>>()
            .map_err(bad)?;
        let min_len = min_len(&spec.regex);
        Ok(Rule {
            spec,
            re,
            allowlists,
            min_len,
        })
    }

    pub fn id(&self) -> &str {
        &self.spec.id
    }
    /// The Go RE2 source.
    pub fn pattern(&self) -> &str {
        &self.spec.regex
    }
    /// The compiled (translated) pattern.
    pub fn regex(&self) -> &Regex {
        &self.re
    }
    pub fn secret_group(&self) -> usize {
        self.spec.secret_group
    }
    pub fn entropy(&self) -> f64 {
        self.spec.entropy
    }
    pub fn keywords(&self) -> &[String] {
        &self.spec.keywords
    }
    pub fn tags(&self) -> &[String] {
        &self.spec.tags
    }
    pub fn spec(&self) -> &RuleSpec {
        &self.spec
    }
}

/// The fewest bytes a match can span (0 when unknown).
fn min_len(pattern: &str) -> usize {
    syntax::translate(pattern)
        .ok()
        .and_then(|p| regex_syntax::Parser::new().parse(&p).ok())
        .and_then(|h| h.properties().minimum_len())
        .unwrap_or(0)
}

/// A compiled allowlist.
#[derive(Clone, Debug)]
pub(crate) struct Allowlist {
    and: bool,
    target: Target,
    regexes: Vec<Regex>,
    /// Each regex anchored to the whole target, for redaction.
    full: Vec<Regex>,
    stopwords: Vec<String>,
    contextual: bool,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) enum Target {
    Secret,
    Match,
    Line,
}

/// What an allowlist judges.
pub(crate) struct Finding<'a> {
    pub secret: &'a str,
    pub whole: &'a str,
    pub line: &'a str,
}

impl Allowlist {
    pub(crate) fn compile(s: &AllowlistSpec) -> Result<Self, String> {
        let and = match s.condition.to_ascii_uppercase().as_str() {
            "" | "OR" | "||" => false,
            "AND" | "&&" => true,
            other => return Err(format!("allowlist: unknown condition {other:?}")),
        };
        let target = match s.regex_target.as_str() {
            "" | "secret" => Target::Secret,
            "match" => Target::Match,
            "line" => Target::Line,
            other => return Err(format!("allowlist: unknown regex target {other:?}")),
        };
        if s.regexes.is_empty()
            && s.stopwords.is_empty()
            && s.paths.is_empty()
            && s.commits.is_empty()
        {
            return Err("allowlist: needs regexes, stopwords, paths or commits".into());
        }
        let mut regexes = Vec::new();
        let mut full = Vec::new();
        for p in &s.regexes {
            regexes.push(compile_go(p).map_err(|e| format!("allowlist regex {p:?}: {e}"))?);
            full.push(
                compile_go(&format!(r"\A(?:{p})\z"))
                    .map_err(|e| format!("allowlist regex {p:?}: {e}"))?,
            );
        }
        Ok(Allowlist {
            and,
            target,
            regexes,
            full,
            stopwords: s.stopwords.iter().map(|w| go_lower(w)).collect(),
            contextual: !s.paths.is_empty() || !s.commits.is_empty(),
        })
    }

    pub(crate) fn targets_line(&self) -> bool {
        self.target == Target::Line
    }

    /// Detection (gitleaks) when `detect`, else fail-closed redaction.
    pub(crate) fn allows(&self, f: &Finding<'_>, detect: bool) -> bool {
        let target = match self.target {
            Target::Secret => f.secret,
            Target::Match => f.whole,
            Target::Line => f.line,
        };
        let regex_hit = !target.is_empty()
            && if detect {
                self.regexes
                    .iter()
                    .any(|re| re.find(target).is_some_and(|m| !m.is_empty()))
            } else {
                self.full.iter().any(|re| re.is_match(target))
            };
        let stop_hit = detect && !f.secret.is_empty() && !self.stopwords.is_empty() && {
            let lower = go_lower(f.secret);
            self.stopwords.iter().any(|w| lower.contains(w.as_str()))
        };
        if !self.and {
            return regex_hit || stop_hit;
        }
        !self.contextual
            && (self.regexes.is_empty() || regex_hit)
            && (self.stopwords.is_empty() || stop_hit)
    }
}

/// Lower-cases as Go's `strings.ToLower`: per rune, simple mapping.
/// U+0130 is the one rune whose full lower case differs (it adds
/// U+0307); Go maps it to a plain `i`.
pub(crate) fn go_lower(s: &str) -> String {
    if s.is_ascii() {
        return s.to_ascii_lowercase();
    }
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        if c == '\u{130}' {
            out.push('i');
        } else {
            out.extend(c.to_lowercase());
        }
    }
    out
}

/// Shannon entropy as gitleaks computes it: rune frequencies divided by
/// the byte length, summed in ascending rune order.
pub(crate) fn shannon_entropy(data: &str) -> f64 {
    if data.is_empty() {
        return 0.0;
    }
    let mut counts: BTreeMap<char, usize> = BTreeMap::new();
    for c in data.chars() {
        *counts.entry(c).or_default() += 1;
    }
    let inv = 1.0 / data.len() as f64;
    let mut e = 0.0;
    for n in counts.values() {
        let f = *n as f64 * inv;
        e -= f * f.log2();
    }
    e
}
