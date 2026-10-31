//! Runs the shared redact corpus in `contracts/redact-v1`. The vectors
//! are the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited.
//!
//! Strict: unknown fields fail, `version` must be `v1`, the corpus
//! directory must match [`FILES`], and the embedded `rules.json` must
//! equal the contract's byte for byte.
#![cfg(feature = "redact")]

use std::fs;
use std::path::PathBuf;

use hop_top_kit::redact::{
    self, AllowlistSpec, Policy, Redactor, Replacement, Rule, RuleSpec, RULES_JSON,
};
use serde::Deserialize;

const FILES: &[&str] = &["README.md", "rules.json", "vectors.json"];

#[derive(Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
struct ConfSpan {
    rule: String,
    start: usize,
    end: usize,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Pad {
    text: String,
    times: usize,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Vector {
    name: String,
    #[serde(default)]
    #[allow(dead_code)]
    note: Option<String>,
    rules: Option<Vec<RuleSpec>>,
    #[serde(default)]
    allowlists: Vec<AllowlistSpec>,
    #[serde(default)]
    allowlist_exact: Vec<String>,
    strategy: String,
    input: String,
    pad: Option<Pad>,
    findings: Option<Vec<ConfSpan>>,
    spans: Vec<ConfSpan>,
    output: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct File {
    version: String,
    #[allow(dead_code)]
    description: String,
    vectors: Vec<Vector>,
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/redact-v1")
}

#[test]
fn corpus_files_have_runners_and_rules_match() {
    let mut names: Vec<String> = fs::read_dir(dir())
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    assert_eq!(names, FILES);
    let contract = fs::read_to_string(dir().join("rules.json")).unwrap();
    assert!(
        contract == RULES_JSON,
        "src/redact/rules.json differs from the contract's"
    );
    let p: Policy = serde_json::from_str(&contract).unwrap();
    Redactor::from_policy(&p).expect("every default rule compiles under the regex crate");
    // The classification Go exported is the one this port computes.
    let disagree: Vec<&str> = p
        .rules
        .iter()
        .filter(|s| Rule::compile((*s).clone()).unwrap().distinctive() != s.distinctive)
        .map(|s| s.id.as_str())
        .collect();
    assert!(
        disagree.is_empty(),
        "distinctive differs from rules.json for {disagree:?}"
    );
}

#[test]
fn vectors() {
    let f: File =
        serde_json::from_str(&fs::read_to_string(dir().join("vectors.json")).unwrap()).unwrap();
    assert_eq!(f.version, "v1");
    let default = redact::embedded_policy().unwrap();
    let mut failures = Vec::new();
    for v in &f.vectors {
        let policy = match &v.rules {
            Some(rules) => Policy {
                rules: rules.clone(),
                allowlists: v.allowlists.clone(),
                allowlist_exact: v.allowlist_exact.clone(),
                ..Policy::default()
            },
            None => default.clone(),
        };
        let mut r = match Redactor::from_policy(&policy) {
            Ok(r) => r,
            Err(e) => {
                failures.push(format!("{}: {e}", v.name));
                continue;
            }
        };
        r.set_replacement(match v.strategy.as_str() {
            "mask" => Replacement::Mask,
            "tag" => Replacement::Tag,
            "hash" => Replacement::Hash,
            other => panic!("{}: unknown strategy {other}", v.name),
        });
        let (input, want, off) = match &v.pad {
            Some(p) => {
                let pad = p.text.repeat(p.times);
                (
                    format!("{pad}{}{pad}", v.input),
                    format!("{pad}{}{pad}", v.output),
                    pad.len(),
                )
            }
            None => (v.input.clone(), v.output.clone(), 0),
        };
        let shift = |rule: String, start: usize, end: usize| ConfSpan {
            rule,
            start: start - off,
            end: end - off,
        };
        if let Some(findings) = &v.findings {
            let got: Vec<ConfSpan> = r
                .scan(&input)
                .into_iter()
                .map(|m| shift(m.rule_id, m.start, m.end))
                .collect();
            if &got != findings {
                failures.push(format!("{}: findings {got:?}, want {findings:?}", v.name));
            }
        }
        let spans: Vec<ConfSpan> = r
            .spans(&input)
            .into_iter()
            .map(|s| shift(s.rule_id, s.start, s.end))
            .collect();
        if spans != v.spans {
            failures.push(format!("{}: spans {spans:?}, want {:?}", v.name, v.spans));
        }
        let got = r.apply(&input);
        if got != want {
            let trim = |s: &str| {
                s.get(off..s.len().saturating_sub(off))
                    .unwrap_or(s)
                    .to_string()
            };
            failures.push(format!(
                "{}: output {:?}, want {:?}",
                v.name,
                trim(&got),
                v.output
            ));
        }
    }
    assert!(
        failures.is_empty(),
        "{} vector(s) failed:\n{}",
        failures.len(),
        failures.join("\n")
    );
}
