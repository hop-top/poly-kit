use std::sync::atomic::{AtomicU64, Ordering};

use super::*;

const HOUR: Duration = Duration::from_secs(3600);

/// A ledger on `store` whose clock the test moves.
fn ledger_on(store: Arc<dyn UsageStore>, secs: u64) -> (UsageLedger, Arc<AtomicU64>) {
    let now = Arc::new(AtomicU64::new(secs));
    let n = now.clone();
    let l = UsageLedger::with_store(store)
        .with_clock_of(move || UNIX_EPOCH + Duration::from_secs(n.load(Ordering::SeqCst)));
    (l, now)
}

/// The budget behaviour every store must give.
fn budget_contract(store: Arc<dyn UsageStore>) {
    let (l, now) = ledger_on(store, 10 * 3600 + 5);

    assert_eq!(l.usage("a b", HOUR).unwrap().ops, 0);
    for want in 1..=2 {
        let (u, took) = l.take("a b", HOUR, 2).unwrap();
        assert!(took);
        assert_eq!(u.ops, want);
    }
    let (u, took) = l.take("a b", HOUR, 2).unwrap();
    assert!(!took, "a spent window takes nothing");
    assert_eq!(u.ops, 2);
    assert_eq!(u.reset, UNIX_EPOCH + Duration::from_secs(11 * 3600));
    assert_eq!(u.start, UNIX_EPOCH + Duration::from_secs(10 * 3600));

    let u = l.add("a b", HOUR, 3, 70).unwrap();
    assert_eq!((u.ops, u.bytes), (5, 70));
    assert_eq!(l.usage("other", HOUR).unwrap().ops, 0, "keys are apart");
    assert_eq!(l.usage("a b", Duration::from_secs(60)).unwrap().ops, 0);

    l.add("a/c", HOUR, 1, 0).unwrap();
    assert_eq!(l.keys("a").unwrap(), vec!["a b", "a/c"]);
    assert_eq!(l.keys("a/").unwrap(), vec!["a/c"]);

    now.store(11 * 3600, Ordering::SeqCst);
    assert_eq!(
        l.usage("a b", HOUR).unwrap().ops,
        0,
        "next window starts at 0"
    );
    assert!(l.take("a b", HOUR, 1).unwrap().1);

    l.reset("a b").unwrap();
    assert_eq!(l.keys("").unwrap(), vec!["a/c"]);
    assert_eq!(l.usage("a b", HOUR).unwrap().ops, 0);

    assert!(l.usage("k", Duration::from_millis(500)).is_err());
}

#[test]
fn memory_store_keeps_budgets() {
    budget_contract(Arc::new(MemoryUsageStore::new()));
}

#[cfg(feature = "kv")]
#[test]
fn kv_store_keeps_budgets() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("usage.db");
    let store = crate::kv::SqliteStore::new(path.to_str().unwrap()).unwrap();
    budget_contract(Arc::new(KvUsageStore::new(store)));
}

#[cfg(feature = "kv")]
#[test]
fn a_file_ledger_keeps_its_counts_across_a_reopen() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("usage.db");
    let p = path.to_str().unwrap();
    {
        let l = UsageLedger::with_file(p).unwrap();
        l.add("k", HOUR, 3, 9).unwrap();
    }
    let u = UsageLedger::with_file(p).unwrap().usage("k", HOUR).unwrap();
    assert_eq!((u.ops, u.bytes), (3, 9));
}

#[test]
fn the_default_is_memory_and_creates_no_file() {
    let tool = format!("kit-rs-usage-default-{}", std::process::id());
    let dir = crate::xdg::raw_state_dir(&tool);
    let g = crate::cli::ServedGate::new(crate::cli::Policy::default()).unwrap();
    g.ledger().add("k", HOUR, 1, 0).unwrap();
    assert!(!dir.exists(), "no state directory for {tool}");
    let l = UsageLedger::open(UsageStoreKind::Memory, &tool).unwrap();
    l.add("k", HOUR, 1, 0).unwrap();
    assert!(!dir.exists(), "no state directory for {tool}");
    assert_eq!(UsageLedger::new().usage("k", HOUR).unwrap().ops, 0);
}

#[cfg(feature = "kv")]
#[test]
fn the_file_path_is_gos() {
    let p = usage_path("mytool");
    assert!(p.ends_with("mytool/usage.db"), "{p:?}");
}

#[test]
fn a_given_store_wins() {
    let store: Arc<dyn UsageStore> = Arc::new(MemoryUsageStore::new());
    let a = UsageLedger::with_store(store.clone());
    a.add("k", HOUR, 2, 0).unwrap();
    let b = UsageLedger::with_store(store);
    assert_eq!(b.usage("k", HOUR).unwrap().ops, 2, "ledgers share a store");
    assert_eq!(UsageLedger::new().usage("k", HOUR).unwrap().ops, 0);
}

#[test]
fn entries_are_stored_as_go_stores_them() {
    let store = Arc::new(MemoryUsageStore::new());
    let (l, _) = ledger_on(store.clone(), 3 * 86_400 + 100);
    l.add(
        "policy/team/0/alice@x y/acme",
        Duration::from_secs(86_400),
        2,
        5,
    )
    .unwrap();
    let key = "kit/usage/policy%2Fteam%2F0%2Falice%40x+y%2Facme/86400/259200";
    let raw = store.get(key).unwrap().expect("Go's entry key");
    assert_eq!(String::from_utf8(raw).unwrap(), r#"{"ops":2,"bytes":5}"#);
    // An entry written by Go reads back (field order is free).
    store
        .put_with_ttl("kit/usage/go/3600/259200", br#"{"ops":4,"bytes":1}"#, HOUR)
        .unwrap();
    let u = l.usage("go", HOUR).unwrap();
    assert_eq!((u.ops, u.bytes), (4, 1));
    // A corrupt entry counts from zero.
    store
        .put_with_ttl("kit/usage/bad/3600/259200", b"nope", HOUR)
        .unwrap();
    assert_eq!(l.usage("bad", HOUR).unwrap().ops, 0);
}

#[test]
fn escaping_matches_go() {
    assert_eq!(
        query_escape("a b/c@d:e,f~g-h_i.j"),
        "a+b%2Fc%40d%3Ae%2Cf~g-h_i.j"
    );
    assert_eq!(query_unescape("a+b%2Fc").as_deref(), Some("a b/c"));
    assert_eq!(query_unescape("%zz"), None);
}

#[test]
fn memory_entries_expire() {
    let s = MemoryUsageStore::new();
    s.put_with_ttl("k", b"v", Duration::ZERO).unwrap();
    assert_eq!(s.get("k").unwrap(), None);
    assert!(s.list("").unwrap().is_empty());
}

#[test]
fn the_kind_parses() {
    assert_eq!(UsageStoreKind::parse("").unwrap(), UsageStoreKind::Memory);
    assert_eq!(
        UsageStoreKind::parse(" Memory ").unwrap(),
        UsageStoreKind::Memory
    );
    assert_eq!(UsageStoreKind::parse("FILE").unwrap(), UsageStoreKind::File);
    assert!(UsageStoreKind::parse("etcd")
        .unwrap_err()
        .contains("services.usage_store"));
}

#[cfg(not(feature = "kv"))]
#[test]
fn the_file_kind_needs_kv() {
    assert!(UsageLedger::open(UsageStoreKind::File, "t")
        .unwrap_err()
        .contains("kv"));
}
