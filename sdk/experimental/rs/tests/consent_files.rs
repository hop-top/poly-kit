//! Per-tool files, `open` with a relay, and compat with the telemetry
//! module's read-only consent loader. Env-mutating: every test holds
//! [`ENV`] and points the XDG dirs at a temp dir.
#![cfg(feature = "consent-file")]

use std::sync::{Arc, Mutex};
use std::time::Duration as StdDuration;

use hop_top_kit::bus::{Bus, BusError, SharedBus};
use hop_top_kit::consent::{
    open, state_file, AuditEvent, Auditor, DecisionSource, Duration, Effect, Error, FileStore,
    Grant, GrantStore, Grants, MemoryGrantStore, Publisher,
};

static ENV: Mutex<()> = Mutex::new(());

fn isolated() -> (std::sync::MutexGuard<'static, ()>, tempfile::TempDir) {
    let g = ENV.lock().unwrap_or_else(|e| e.into_inner());
    let tmp = tempfile::tempdir().unwrap();
    std::env::set_var("XDG_STATE_HOME", tmp.path().join("state"));
    std::env::set_var("XDG_CONFIG_HOME", tmp.path().join("config"));
    std::env::set_var("HOME", tmp.path());
    std::env::remove_var("KIT_APP_PREFIX");
    (g, tmp)
}

fn allow_always(cap: &str, subject: &str) -> Grant {
    Grant::new(
        cap,
        subject,
        Effect::Allow,
        Duration::Always,
        DecisionSource::Prompt,
    )
}

#[cfg(unix)]
fn mode(p: &std::path::Path) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    std::fs::metadata(p).unwrap().permissions().mode() & 0o777
}

#[test]
fn paths_are_per_tool_under_xdg_state_home() {
    let (_g, tmp) = isolated();
    let p = state_file("dek", "grants.yaml").unwrap();
    assert_eq!(p, tmp.path().join("state/dek/consent/grants.yaml"));
    for bad in ["", ".", "..", "a/b", " dek"] {
        assert!(state_file(bad, "grants.yaml").is_err(), "{bad:?}");
    }
    let s = FileStore::for_tool("dek").unwrap();
    assert_eq!(s.config_path(), tmp.path().join("config/kit/config.yaml"));
    assert_eq!(
        s.legacy_path(),
        tmp.path().join("config/kit/telemetry.yaml")
    );
}

#[cfg(unix)]
#[test]
fn files_are_0600_in_a_0700_dir() {
    let (_g, _tmp) = isolated();
    let g = open(Some("dek")).unwrap().build();
    g.decide(allow_always("io.screen.capture", "app:dek"))
        .unwrap();
    let grants = state_file("dek", "grants.yaml").unwrap();
    assert_eq!(mode(&grants), 0o600);
    assert_eq!(mode(&state_file("dek", "audit.jsonl").unwrap()), 0o600);
    assert_eq!(mode(grants.parent().unwrap()), 0o700);
    let dir = std::fs::read_dir(grants.parent().unwrap()).unwrap();
    let names: Vec<String> = dir
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    assert!(
        names.iter().all(|n| !n.contains(".tmp.")),
        "temp file left: {names:?}"
    );
}

#[test]
fn grants_files_are_per_tool() {
    let (_g, _tmp) = isolated();
    let a = open(Some("a")).unwrap().build();
    let b = open(Some("b")).unwrap().build();
    a.decide(allow_always("io.files.read", "")).unwrap();
    assert!(a.check("io.files.read", "").unwrap().allowed);
    assert!(!b.check("io.files.read", "").unwrap().allowed);
    assert!(!b.revoke("io.files.read", "", DecisionSource::Flag).unwrap());
    assert!(a.check("io.files.read", "").unwrap().allowed);
}

#[test]
fn open_with_delivery_relays_every_record_onto_the_bus() {
    let (_g, _tmp) = isolated();
    let bus = SharedBus::new(Bus::new());
    let seen = Arc::new(Mutex::new(Vec::<(String, String)>::new()));
    let s2 = seen.clone();
    bus.subscribe("kit.core.consent.#", move |e| {
        s2.lock()
            .unwrap()
            .push((e.topic.as_str().to_string(), e.id.clone()));
        Ok(())
    });
    let g = open(Some("dek")).unwrap().deliver_to(Arc::new(bus)).build();
    let relay = g.relay().expect("relay");
    let r2 = relay.clone();
    let t = std::thread::spawn(move || r2.run());
    g.decide(allow_always("io.clipboard.read", "app:dek"))
        .unwrap();
    g.use_grant("io.clipboard.read", "app:dek").unwrap();
    for _ in 0..200 {
        if seen.lock().unwrap().len() == 2 {
            break;
        }
        std::thread::sleep(StdDuration::from_millis(10));
    }
    relay.stop();
    t.join().unwrap();
    let seen = seen.lock().unwrap().clone();
    let topics: Vec<&str> = seen.iter().map(|(t, _)| t.as_str()).collect();
    assert_eq!(
        topics,
        ["kit.core.consent.granted", "kit.core.consent.used"]
    );
    assert!(
        seen[0].1.starts_with("clog_") && seen[0].1.ends_with(":1"),
        "{seen:?}"
    );
    assert!(seen[1].1.ends_with(":2"));
    assert_eq!(relay.flush().unwrap().delivered, Vec::<u64>::new());
}

#[test]
fn an_audit_failure_refuses_the_use_and_is_published_unrecorded() {
    let (_g, _tmp) = isolated();
    let bus = SharedBus::new(Bus::new());
    let seen = Arc::new(Mutex::new(Vec::<serde_json::Value>::new()));
    let s2 = seen.clone();
    bus.subscribe("kit.core.consent.refused", move |e| {
        s2.lock().unwrap().push(e.payload.clone());
        Ok(())
    });
    let pubr: Arc<dyn Publisher> = Arc::new(bus);
    let audit = Arc::new(hop_top_kit::consent::FileAuditor::at(
        state_file("dek", "audit.jsonl").unwrap(),
    ));
    let relay = Arc::new(hop_top_kit::consent::Relay::new(audit, pubr));
    let failing = |e: &AuditEvent| -> Result<(), String> {
        if e.event == hop_top_kit::consent::AuditKind::Used {
            Err("disk full".into())
        } else {
            Ok(())
        }
    };
    let g = Grants::builder(Arc::new(MemoryGrantStore::new()))
        .auditor(Arc::new(failing))
        .delivery(relay)
        .build();
    let once = Grant::new(
        "io.camera.capture",
        "",
        Effect::Allow,
        Duration::Once,
        DecisionSource::Prompt,
    );
    g.decide(once).unwrap();
    let err = g.use_grant("io.camera.capture", "").unwrap_err();
    assert!(matches!(err, Error::Audit(_)), "{err}");
    assert!(
        g.check("io.camera.capture", "").unwrap().allowed,
        "a once grant is kept"
    );
    let seen = seen.lock().unwrap();
    assert_eq!(seen.len(), 1);
    assert_eq!(seen[0]["reason"], "audit-failed");
    assert!(seen[0].get("seq").is_none());
}

#[test]
fn a_bus_veto_on_a_bus_auditor_refuses_the_use() {
    let bus = SharedBus::new(Bus::new());
    bus.subscribe("kit.core.consent.used", |_| Err(BusError::handler("veto")));
    let a = hop_top_kit::consent::BusAuditor::new(bus, Default::default());
    let g = Grants::builder(Arc::new(MemoryGrantStore::new()))
        .auditor(Arc::new(a))
        .build();
    g.decide(allow_always("io.files.read", "")).unwrap();
    assert!(matches!(
        g.use_grant("io.files.read", ""),
        Err(Error::Audit(_))
    ));
}

#[test]
fn types_are_send_and_sync() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<Grants>();
    assert_send_sync::<FileStore>();
    assert_send_sync::<hop_top_kit::consent::FileAuditor>();
    assert_send_sync::<hop_top_kit::consent::Relay>();
    assert_send_sync::<Error>();
    let _: &dyn Auditor = &hop_top_kit::consent::FileAuditor::at("x");
    let _: &dyn GrantStore = &MemoryGrantStore::new();
}

/// The telemetry module reads only the legacy records: a decision made
/// through grants is mirrored there, and a revoke clears it.
#[cfg(feature = "telemetry")]
#[test]
fn telemetry_loader_sees_grant_writes() {
    use hop_top_kit::consent::CAP_TELEMETRY;
    let (_g, _tmp) = isolated();
    let g = open(Some("dek")).unwrap().env(|_| String::new()).build();
    g.decide(allow_always(CAP_TELEMETRY, "")).unwrap();
    assert!(hop_top_kit::telemetry::load_consent().allowed);
    let deny = Grant::new(
        CAP_TELEMETRY,
        "",
        Effect::Deny,
        Duration::Always,
        DecisionSource::Flag,
    );
    g.decide(deny).unwrap();
    let c = hop_top_kit::telemetry::load_consent();
    assert!(!c.allowed);
    assert_eq!(c.decision_source, "flag");
    g.decide(allow_always(CAP_TELEMETRY, "")).unwrap();
    assert!(g.revoke(CAP_TELEMETRY, "", DecisionSource::Flag).unwrap());
    assert!(!hop_top_kit::telemetry::load_consent().allowed);
    assert!(!g.check(CAP_TELEMETRY, "").unwrap().allowed);
}
