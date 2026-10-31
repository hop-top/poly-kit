use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::*;
use crate::cli::policy::{CallerRule, PermissionRule, Policy};

/// Refuses writes to everyone but alice, who gets `max_ops` an hour.
fn callers_policy(max_ops: i64) -> Policy {
    Policy {
        name: "team".into(),
        allow: BTreeMap::from([("write".into(), Vec::new())]),
        callers: vec![CallerRule {
            principal: "alice".into(),
            allow: BTreeMap::from([("write".into(), vec!["*".into()])]),
            max_ops,
            window: Duration::from_secs(3600),
            ..CallerRule::default()
        }],
        ..Policy::default()
    }
}

fn req(caller: &str, tenant: &str, source: CallerSource, tier: &str) -> PermissionRequest {
    PermissionRequest {
        caller: caller.into(),
        tenant: tenant.into(),
        source,
        surface: "rest".into(),
        path: vec!["widget".into(), "add".into()],
        tier: tier.into(),
        ..PermissionRequest::default()
    }
}

fn alice(tier: &str) -> PermissionRequest {
    req("alice", "acme", CallerSource::Verified, tier)
}

fn call(r: &PermissionRequest) -> ServedCall<'_> {
    ServedCall {
        request: r,
        permissions: None,
        probe: false,
    }
}

fn spent_by(g: &ServedGate, p: &str, t: &str) -> i64 {
    let c = Caller {
        principal: p.into(),
        tenant: t.into(),
        ..Caller::default()
    };
    let b = g.budget_for(&c).expect("budget");
    g.ledger().usage(&b.key, b.window).unwrap().ops
}

fn rule(name: &str, when: &str) -> PermissionRule {
    PermissionRule {
        name: name.into(),
        when: when.into(),
        effect: "allow".into(),
        otherwise: "deny".into(),
        message: "no".into(),
    }
}

#[test]
fn budget_is_charged_only_once_every_decider_admits() {
    let mut p = callers_policy(5);
    p.permissions = vec![rule(
        "stub",
        "!has(payload.flags.deny) || payload.flags.deny != 'rules'",
    )];
    let g = ServedGate::new(p).unwrap().with_permission(|r| {
        match r.flags.get("deny").and_then(|v| v.as_str()) {
            Some("adopter") => Err("adopter: no".into()),
            _ => Ok(()),
        }
    });
    let with_deny = |d: &str| {
        let mut r = alice("write");
        if !d.is_empty() {
            r.flags.insert("deny".into(), d.into());
        }
        r
    };

    let e = g.decide(&call(&with_deny("rules"))).unwrap_err();
    assert_eq!(e, r#"permission rule "stub": no"#);
    assert_eq!(
        spent_by(&g, "alice", "acme"),
        0,
        "rules refuse: nothing spent"
    );

    let e = g.decide(&call(&with_deny("adopter"))).unwrap_err();
    assert_eq!(e, "adopter: no");
    assert_eq!(
        spent_by(&g, "alice", "acme"),
        0,
        "adopter refuses: nothing spent"
    );

    let r = with_deny("");
    let mut probe = call(&r);
    probe.probe = true;
    g.decide(&probe).unwrap();
    assert_eq!(spent_by(&g, "alice", "acme"), 0, "a probe spends nothing");

    g.decide(&call(&r)).unwrap();
    assert_eq!(
        spent_by(&g, "alice", "acme"),
        1,
        "an admitted call spends one"
    );
}

#[test]
fn a_spent_budget_refuses_before_later_deciders_are_asked() {
    let asked = Arc::new(AtomicU64::new(0));
    let a = asked.clone();
    let g = ServedGate::new(callers_policy(1))
        .unwrap()
        .with_permission(move |_| {
            a.fetch_add(1, Ordering::SeqCst);
            Ok(())
        });
    let r = alice("write");
    g.decide(&call(&r)).unwrap();
    assert_eq!(asked.load(Ordering::SeqCst), 1);

    let e = g.decide(&call(&r)).unwrap_err();
    assert!(
        e.starts_with("policy: max_ops budget of 1 per 1h0m0s spent; resets at "),
        "{e}"
    );
    assert!(e.ends_with('Z'), "{e}");
    assert_eq!(asked.load(Ordering::SeqCst), 1, "adopter not asked");
}

#[test]
fn budgets_are_per_principal_and_tenant_and_reset_each_window() {
    let now = Arc::new(AtomicU64::new(10 * 3600 + 5));
    let n = now.clone();
    let ledger =
        UsageLedger::with_clock(move || UNIX_EPOCH + Duration::from_secs(n.load(Ordering::SeqCst)));
    let mut p = callers_policy(1);
    p.callers[0].principal = "*".into();
    let g = ServedGate::new(p).unwrap().with_ledger(ledger);
    let in_tenant = |t: &str| req("alice", t, CallerSource::Verified, "write");

    g.decide(&call(&in_tenant("acme"))).unwrap();
    assert!(g.decide(&call(&in_tenant("acme"))).is_err());
    g.decide(&call(&in_tenant("globex")))
        .expect("own budget per tenant");
    g.decide(&call(&req("bob", "acme", CallerSource::Verified, "write")))
        .expect("own budget per principal");

    now.store(11 * 3600, Ordering::SeqCst);
    g.decide(&call(&in_tenant("acme"))).expect("next window");
}

#[test]
fn only_mutating_commands_count() {
    let g = ServedGate::new(callers_policy(1)).unwrap();
    for _ in 0..3 {
        g.decide(&call(&alice("read"))).unwrap();
    }
    assert_eq!(spent_by(&g, "alice", "acme"), 0);
    for tier in ["write-local", "destructive"] {
        let g = ServedGate::new(Policy {
            allow: BTreeMap::new(),
            ..callers_policy(1)
        })
        .unwrap();
        g.decide(&call(&alice(tier))).unwrap();
        assert!(g.decide(&call(&alice(tier))).is_err(), "{tier}");
    }
}

#[test]
fn an_undeclared_tier_is_the_policys_unannotated_class() {
    let mut p = callers_policy(1);
    p.unannotated = "write".into();
    let g = ServedGate::new(p).unwrap();
    g.decide(&call(&alice(""))).unwrap();
    assert!(g
        .decide(&call(&alice("")))
        .unwrap_err()
        .contains("budget of 1"));
    // Declaring nothing with no unannotated class: nothing to count.
    let g = ServedGate::new(callers_policy(1)).unwrap();
    for _ in 0..3 {
        g.decide(&call(&alice(""))).unwrap();
    }
}

#[test]
fn the_caller_is_mapped_as_the_transport_established_it() {
    // Verified but another principal: the policy's own allow refuses.
    let g = ServedGate::new(callers_policy(1)).unwrap();
    let bob = req("bob", "acme", CallerSource::Verified, "write");
    assert!(g
        .decide(&call(&bob))
        .unwrap_err()
        .contains("write not allowed"));
    // Nobody established: matches no caller rule, however it claims.
    let claim = req("alice", "acme", CallerSource::None, "write");
    assert!(g
        .decide(&call(&claim))
        .unwrap_err()
        .contains("write not allowed"));
    // The transport's caller is the owner and a claim of "alice" is no
    // name: a rule naming a principal does not match it.
    let owner = req("alice", "acme", CallerSource::Transport, "write");
    assert!(g.decide(&call(&owner)).is_err());
    // A rule without principal, tenant or scope matches the owner, and
    // its budget is the owner's own.
    let mut p = callers_policy(1);
    p.callers[0].principal.clear();
    let g = ServedGate::new(p).unwrap();
    g.decide(&call(&owner)).unwrap();
    assert!(g.decide(&call(&owner)).unwrap_err().contains("budget of 1"));
}

#[test]
fn a_rule_scope_needs_a_verified_credential_holding_it() {
    let mut p = callers_policy(0);
    p.callers[0].principal.clear();
    p.callers[0].scope = "writer".into();
    let g = ServedGate::new(p).unwrap();
    let mut r = alice("write");
    assert!(g.decide(&call(&r)).is_err());
    r.scopes = vec!["writer".into()];
    g.decide(&call(&r)).unwrap();
}

#[test]
fn new_names_the_rule_that_cannot_run() {
    let mut p = callers_policy(1);
    p.permissions = vec![rule("bad", "nope.id == 'x'")];
    let e = ServedGate::new(p).unwrap_err();
    assert!(e.contains("\"bad\"") && e.contains("undeclared"), "{e}");
    let mut p = callers_policy(-1);
    p.callers[0].max_ops = -1;
    assert!(ServedGate::new(p).unwrap_err().contains("max_ops"));
}

#[test]
fn a_window_under_a_second_is_unavailable_and_refuses() {
    let mut p = callers_policy(1);
    p.callers[0].window = Duration::from_millis(500);
    let g = ServedGate::new(p).unwrap();
    let e = g.decide(&call(&alice("write"))).unwrap_err();
    assert!(e.starts_with("policy: max_ops budget unavailable: "), "{e}");
}

#[test]
fn an_unset_window_counts_hourly() {
    let mut p = callers_policy(1);
    p.callers[0].window = Duration::ZERO;
    let g = ServedGate::new(p).unwrap();
    g.decide(&call(&alice("write"))).unwrap();
    assert!(g
        .decide(&call(&alice("write")))
        .unwrap_err()
        .contains("per 1h0m0s"));
}

#[test]
fn helpers_match_go() {
    for (secs, want) in [
        (1, "1s"),
        (90, "1m30s"),
        (3600, "1h0m0s"),
        (86400, "24h0m0s"),
        (3661, "1h1m1s"),
    ] {
        assert_eq!(go_duration(Duration::from_secs(secs)), want);
    }
    assert_eq!(go_duration(Duration::from_millis(1500)), "1.5s");
    assert_eq!(go_duration(Duration::from_millis(250)), "250ms");
    assert_eq!(go_duration(Duration::ZERO), "0s");
    assert_eq!(path_escape("a b/c@d:e,f"), "a%20b%2Fc@d:e%2Cf");
    assert_eq!(rfc3339(UNIX_EPOCH), "1970-01-01T00:00:00Z");
    assert_eq!(
        rfc3339(UNIX_EPOCH + Duration::from_secs(1_783_000_000)),
        "2026-07-02T13:46:40Z"
    );
    // Windows align on Go's zero time: a day runs midnight to midnight UTC.
    let l = UsageLedger::with_clock(|| UNIX_EPOCH + Duration::from_secs(86_400 * 3 + 100));
    let u = l.usage("k", Duration::from_secs(86_400)).unwrap();
    assert_eq!(u.reset, UNIX_EPOCH + Duration::from_secs(86_400 * 4));
    let _ = SystemTime::now();
}
