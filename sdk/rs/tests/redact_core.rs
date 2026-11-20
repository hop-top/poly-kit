//! Ports of the go/core/redact tests the conformance vectors do not
//! cover: observers, stats, strategies, the two allowlist modes,
//! thread safety and a seeded corpus-wide generator.
#![cfg(feature = "redact")]

use std::borrow::Cow;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use hop_top_kit::redact::{
    self, AllowlistSpec, Policy, Redactor, Replacement, Rule, RuleSpec, Span,
};
use regex_syntax::hir::{Class, Hir, HirKind};

fn spec(id: &str, regex: &str) -> RuleSpec {
    RuleSpec {
        id: id.into(),
        regex: regex.into(),
        ..RuleSpec::default()
    }
}

fn redactor(strategy: Replacement, rules: Vec<RuleSpec>) -> Redactor {
    let mut r = Redactor::from_policy(&Policy {
        rules,
        ..Policy::default()
    })
    .unwrap();
    r.set_replacement(strategy);
    r
}

fn fail_closed(a: AllowlistSpec) -> Redactor {
    let rule = RuleSpec {
        secret_group: 1,
        allowlists: vec![a],
        ..spec("k", r"key=([A-Za-z0-9]{8})")
    };
    redactor(Replacement::Tag, vec![rule])
}

#[test]
fn stopword_does_not_exempt_apply() {
    let r = fail_closed(AllowlistSpec {
        stopwords: vec!["test".into()],
        ..AllowlistSpec::default()
    });
    let input = "key=Zq9TEST7";
    assert_eq!(r.apply(input), "key=<k>");
    assert_eq!(
        r.spans(input),
        vec![Span {
            start: 4,
            end: 12,
            rule_id: "k".into()
        }]
    );
    assert!(
        r.scan(input).is_empty(),
        "detection keeps the stopword exemption"
    );
}

#[test]
fn partial_regex_does_not_exempt_apply() {
    let r = fail_closed(AllowlistSpec {
        regexes: vec!["^EXAMPL".into()],
        ..AllowlistSpec::default()
    });
    assert_eq!(r.apply("key=EXAMPLzz"), "key=<k>");
    assert!(r.scan("key=EXAMPLzz").is_empty());
}

#[test]
fn whole_secret_regex_exempts_apply() {
    let r = fail_closed(AllowlistSpec {
        regexes: vec!["EXAMPL[A-Z]{2}".into()],
        ..AllowlistSpec::default()
    });
    assert_eq!(r.apply("key=EXAMPLZZ"), "key=EXAMPLZZ");
    assert_eq!(r.apply("key=EXAMPLZz"), "key=<k>");
}

#[test]
fn default_policy_stopword_still_redacted() {
    let r = redact::embedded().unwrap();
    let secret = String::from("Zq9Lm2") + "client7Rt4Vb8";
    assert!(!r.apply(&format!("api_token = {secret}")).contains(&secret));
}

#[test]
fn credentials_file_fully_redacted_under_tag() {
    let mut r = redact::embedded().unwrap();
    r.set_replacement(Replacement::Tag);
    let key_id = String::from("AKIA") + "QX7MZ2LP4RT6VB3N";
    let secret = String::from("wJalrXUtnFEMI") + "/K7MDENG/bPxRfiCYzq8Lw3Hd";
    let input =
        format!("[default]\naws_access_key_id = {key_id}\naws_secret_access_key = {secret}\n");
    let out = r.apply(&input);
    assert!(
        !out.contains(&key_id) && !out.contains(&secret[13..]),
        "{out}"
    );
    assert!(
        out.contains("aws_access_key_id = <aws-access-token>\n"),
        "{out}"
    );
}

#[test]
fn replacement_is_not_rescanned() {
    let first = RuleSpec {
        replacement: "zz_tok_ABCDEFGH".into(),
        ..spec("first", "pin-[0-9]{4}")
    };
    let second = RuleSpec {
        replacement: "<second>".into(),
        ..spec("second", "tok_[A-Z]{8}")
    };
    let r = redactor(Replacement::Tag, vec![first, second]);
    assert_eq!(
        r.apply("door pin-1234 opened"),
        "door zz_tok_ABCDEFGH opened"
    );
}

#[test]
fn clean_input_is_borrowed() {
    let r = redactor(Replacement::Mask, vec![spec("d", r"\d{4}")]);
    assert!(matches!(r.apply("no digits"), Cow::Borrowed(_)));
}

#[test]
fn observers_fire_per_region_and_stats_count() {
    let mut r = redactor(
        Replacement::Mask,
        vec![spec("a", "A{3}"), spec("b", "B{3}")],
    );
    let seen = Arc::new(AtomicUsize::new(0));
    let s = seen.clone();
    r.on_match(move |m| {
        assert!(m.end > m.start);
        s.fetch_add(1, Ordering::Relaxed);
    });
    r.allow_exact(["CCC".to_string()]);
    let allowed = Arc::new(AtomicUsize::new(0));
    let a = allowed.clone();
    r.add_rule("c", "C{3}", "").unwrap();
    r.on_allowed(move |_| {
        a.fetch_add(1, Ordering::Relaxed);
    });
    assert_eq!(
        r.apply("AAABBB BBB CCC"),
        "*** *** CCC".replace("***", "***REDACTED***")
    );
    assert_eq!(
        seen.load(Ordering::Relaxed),
        2,
        "adjacent AAA+BBB merge into one region"
    );
    assert_eq!(allowed.load(Ordering::Relaxed), 1);
    let st = r.stats();
    assert_eq!((st.matches, st.allowed, st.by_rule["a"]), (2, 1, 1));
}

#[test]
fn strategies() {
    let mut r = redactor(Replacement::Hash, vec![spec("e", "abcdefgh")]);
    assert_eq!(r.apply("x abcdefgh"), "x sha256:9c56cc51");
    r.set_replacement(Replacement::Custom(Arc::new(|m| {
        format!("[{}]", m.rule_id)
    })));
    assert_eq!(r.apply("abcdefgh"), "[e]");
    r.set_replacement(Replacement::Custom(Arc::new(|_| panic!("formatter bug"))));
    assert_eq!(r.apply("abcdefgh"), "***REDACTED***");
}

#[test]
fn keyword_gate_lowers_like_go() {
    let rule = RuleSpec {
        keywords: vec!["INFO".into()],
        ..spec("k", "[0-9a-f]{12}")
    };
    let r = redactor(Replacement::Tag, vec![rule]);
    assert_eq!(r.apply("0123456789ab"), "0123456789ab");
    assert_eq!(r.apply("\u{130}nfo 0123456789ab"), "\u{130}nfo <k>");
}

#[test]
fn strict_compile() {
    assert!(Rule::compile(RuleSpec {
        secret_group: 2,
        ..spec("x", "(a)")
    })
    .is_err());
    assert!(
        Rule::new("x", "a(?=b)", "").is_err(),
        "lookaround fails loudly"
    );
    assert!(Redactor::from_policy(&Policy {
        version: "v2".into(),
        ..Policy::default()
    })
    .is_err());
    assert!(Rule::new("", "a", "").is_err());
}

#[test]
fn rule_internals_and_policy_round_trip() {
    let r = redact::embedded().unwrap();
    let p = r.policy();
    assert_eq!(p, redact::embedded_policy().unwrap());
    let ada = r
        .rules()
        .iter()
        .find(|x| x.id() == "adafruit-api-key")
        .unwrap();
    assert_eq!(
        (ada.secret_group(), ada.keywords()),
        (1, &["adafruit".to_string()][..])
    );
    assert!(r.rules().len() > 200);
    assert!(p.allowlists[0]
        .regexes
        .contains(&"(?i)^(?:true|false|null)$".to_string()));
}

#[test]
fn send_sync_shared_across_threads() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<Redactor>();
    let r = redact::shared();
    let mail = String::from("dev") + "@example.com";
    std::thread::scope(|s| {
        for _ in 0..4 {
            s.spawn(|| assert!(!r.apply(&format!("mail {mail}")).contains(&mail)));
        }
    });
}

#[test]
fn distinctive_classification() {
    for (pattern, want) in [
        (r"ghp_[0-9a-zA-Z]{36}", true),
        (r"\bxoxb-[0-9]{10}", true),
        (r"(?i)\b(p8e-[a-z0-9]{32})", true),
        (r"\b((?:A3T[A-Z0-9]|AKIA|ASIA)[A-Z2-7]{16})", true),
        (r"(?:sk-ant-|sk-proj-)[a-z]{8}", true),
        (r"(?:sk|rk)_live_[a-z]{8}", true),
        (r"(?:ghu|ghs)_[0-9a-zA-Z]{36}", true),
        (r"[ab]cd[0-9]", true),
        (r"(?:s|r)k[a-z]{8}", false),
        (r"ab[0-9]{8}", false),
        (r"[\w.-]{0,50}?(?:adafruit)=([a-z0-9]{32})", false),
        (r"(?:AKIA|ab)[0-9]{8}", false),
        (r"[0-9a-f]{12}", false),
        (r"A{3}[0-9]", false),
    ] {
        assert_eq!(
            Rule::new("r", pattern, "").unwrap().distinctive(),
            want,
            "{pattern}"
        );
    }
}

#[test]
fn distinctive_skips_keyword_gate_and_entropy_in_apply() {
    let gated = RuleSpec {
        keywords: vec!["zzz".into()],
        ..spec("t", "tok_[0-9a-f]{8}")
    };
    let r = redactor(Replacement::Tag, vec![gated]);
    assert_eq!(r.apply("x tok_0123abcd"), "x <t>");
    assert!(r.scan("x tok_0123abcd").is_empty());
    let low = RuleSpec {
        secret_group: 1,
        entropy: 3.0,
        ..spec("t", "tok_(a{8})")
    };
    let r = redactor(Replacement::Tag, vec![low]);
    assert_eq!(r.apply("tok_aaaaaaaa"), "tok_<t>");
    assert!(r.scan("tok_aaaaaaaa").is_empty());
}

#[test]
fn generic_keeps_gate_and_entropy_in_apply() {
    let g = RuleSpec {
        keywords: vec!["key".into()],
        ..spec("g", "[0-9a-f]{12}")
    };
    let e = RuleSpec {
        entropy: 3.0,
        ..spec("e", "[a-h]{8}")
    };
    let r = redactor(Replacement::Tag, vec![g, e]);
    assert_eq!(r.apply("0123456789ab abcdefgh"), "0123456789ab abcdefgh");
}

/// A seeded generator of strings matching a pattern, on its HIR.
struct Gen(u64);

impl Gen {
    fn next(&mut self, n: usize) -> usize {
        self.0 = self
            .0
            .wrapping_mul(6364136223846793005)
            .wrapping_add(1442695040888963407);
        ((self.0 >> 33) as usize) % n.max(1)
    }

    fn walk(&mut self, h: &Hir, out: &mut String) {
        match h.kind() {
            HirKind::Literal(l) => out.push_str(&String::from_utf8_lossy(&l.0)),
            HirKind::Class(Class::Unicode(c)) => {
                let rs = c.ranges();
                if rs.is_empty() {
                    return;
                }
                let r = &rs[self.next(rs.len())];
                let span = (r.end() as u32 - r.start() as u32 + 1).min(1 << 16) as usize;
                if let Some(ch) = char::from_u32(r.start() as u32 + self.next(span) as u32) {
                    out.push(ch);
                }
            }
            HirKind::Class(Class::Bytes(c)) => {
                if let Some(r) = c.ranges().first() {
                    out.push(r.start() as char);
                }
            }
            HirKind::Repetition(rep) => {
                let hi = rep.max.map_or(rep.min + 3, |m| m.min(rep.min + 3));
                let n = rep.min as usize + self.next((hi - rep.min + 1) as usize);
                for _ in 0..n {
                    self.walk(&rep.sub, out);
                }
            }
            HirKind::Capture(c) => self.walk(&c.sub, out),
            HirKind::Concat(hs) => hs.iter().for_each(|x| self.walk(x, out)),
            HirKind::Alternation(hs) => {
                let i = self.next(hs.len());
                self.walk(&hs[i], out);
            }
            HirKind::Empty | HirKind::Look(_) => {}
        }
    }
}

/// Every rule's findings on generated keys leave no fragment behind.
#[test]
fn generated_keys_leave_no_fragments() {
    let r = redact::embedded().unwrap();
    let mut g = Gen(1667);
    let mut found = 0;
    for rule in r.rules() {
        let hir = regex_syntax::Parser::new()
            .parse(rule.regex().as_str())
            .unwrap();
        let mut hit = false;
        for _ in 0..12 {
            let mut key = String::new();
            g.walk(&hir, &mut key);
            let input = format!("log: {} {key} end", rule.keywords().join(" "));
            let out = r.apply(&input);
            for m in r.scan(&input) {
                hit |= m.rule_id == rule.id();
                assert!(
                    m.original.len() < 6 || !out.contains(&m.original),
                    "rule {}: finding {:?} of {} survives in {out:?}",
                    rule.id(),
                    m.original,
                    m.rule_id
                );
            }
        }
        found += usize::from(hit);
    }
    assert!(
        found * 10 >= r.rules().len() * 8,
        "only {found} of {} rules found their keys",
        r.rules().len()
    );
}

// credit-card: Luhn plus token boundary (mirrors go/core/redact/validate_test.go).
mod credit_card {
    use hop_top_kit::redact;

    #[test]
    fn kept_inside_token() {
        let r = redact::shared();
        for s in [
            // Luhn-valid 4+12 UUID tail: only the UUID check saves it.
            "bffab4c3-9d88-43ab-4111-111111111111",
            "evt-bffab4c3-9d88-43ab-4111-111111111111",
            "BFFAB4C3-9D88-43A9-4111-111111111111",
            "xbffab4c3-9d88-43ab-4111-111111111111",
            r#"{"id":"bffab4c3-9d88-43ab-4111-111111111111"}"#,
            // All-digit tail, not Luhn-valid.
            "evt-bffab4c3-9d88-4349-9131-824401265021",
            // Alphanumerics glued to a Luhn-valid run.
            "abc4111111111111111",
            "4111111111111111abc",
            "deadbeef4111111111111111cafe",
            "41111111111111111111",
            // Fails Luhn.
            "ref 1234 5678 9012 3456",
            "card-1234567890123456",
        ] {
            assert_eq!(r.apply(s), s, "must round-trip: {s}");
        }
    }

    #[test]
    fn redacted_real_cards() {
        let r = redact::shared();
        for (s, digits) in [
            ("card 4111 1111 1111 1111 on file", "4111 1111 1111 1111"),
            ("card 4111-1111-1111-1111.", "4111-1111-1111-1111"),
            (r#"{"pan":"4111111111111111"}"#, "4111111111111111"),
            ("amex 378282246310005", "378282246310005"),
            ("card-4111111111111111", "4111111111111111"),
            ("cc_4111111111111111", "4111111111111111"),
            ("4111111111111111_cc", "4111111111111111"),
            ("4111111111111111-card", "4111111111111111"),
        ] {
            let out = r.apply(s);
            assert!(!out.contains(digits), "must redact: {s} -> {out}");
            assert!(out.contains("***REDACTED***"), "{s} -> {out}");
        }
    }

    #[test]
    fn label_kept() {
        let r = redact::shared();
        assert_eq!(r.apply("card-4111111111111111"), "card-***REDACTED***");
        assert_eq!(r.apply("cc_4111111111111111"), "cc_***REDACTED***");
    }
}

// Digit rules: token boundary plus SSN/ITIN structure (mirrors
// go/core/redact/validate_test.go).
mod digit_rules {
    use hop_top_kit::redact;

    // (real value, label-joined form, token-embedded or invalid forms)
    const CASES: &[(&str, &str, &[&str])] = &[
        (
            "123-45-6789",
            "ssn-123-45-6789",
            &[
                "ORD123-45-6789",
                "123-45-6789ab",
                "000-12-3456",
                "666-12-3456",
                "900-12-3456",
                "999-12-3456",
                "123-00-6789",
                "123-45-0000",
            ],
        ),
        (
            "912-70-1234",
            "itin-912-70-1234",
            &[
                "ORD912-70-1234",
                "912-70-1234ab",
                "912-49-1234",
                "912-66-1234",
                "912-69-1234",
                "912-89-1234",
                "912-93-1234",
                "912-00-1234",
            ],
        ),
        (
            "415-555-2671",
            "tel-415-555-2671",
            &["ORD415-555-2671", "415-555-2671ab"],
        ),
        (
            "+14155552671",
            "tel_+14155552671",
            &["id9+14155552671", "+14155552671abc"],
        ),
        (
            "123456789",
            "passport-123456789",
            &[
                "id123456789",
                "123456789ab",
                "bffab4c3-9d88-43ab-4111-123456789012",
            ],
        ),
        ("D1234567", "dl-D1234567", &["ORD1234567", "D1234567ab"]),
    ];

    #[test]
    fn token_boundary() {
        let r = redact::shared();
        for (real, label, kept) in CASES {
            assert_eq!(
                r.apply(&format!("ref {real} end")),
                "ref ***REDACTED*** end",
                "{real}"
            );
            assert_eq!(r.apply(real), "***REDACTED***", "{real}");
            assert_eq!(
                r.apply(label),
                label.replace(real, "***REDACTED***"),
                "{label}"
            );
            for k in *kept {
                assert_eq!(r.apply(k), *k, "must round-trip: {k}");
                let s = format!("ref {k} end");
                assert_eq!(r.apply(&s), s, "must round-trip: {s}");
            }
        }
    }

    #[test]
    fn itin_group_ranges() {
        let r = redact::shared();
        for g in ["50", "65", "70", "88", "90", "92", "94", "99"] {
            assert_eq!(r.apply(&format!("912-{g}-1234")), "***REDACTED***", "{g}");
        }
    }

    #[test]
    fn ssn_area_boundaries() {
        let r = redact::shared();
        for s in ["001-01-0001", "665-12-3456", "667-12-3456", "899-12-3456"] {
            assert_eq!(r.apply(s), "***REDACTED***", "{s}");
        }
    }
}
