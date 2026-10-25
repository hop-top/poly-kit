//! Ports reloadable_test.go (partition and reload) from go/core/config.
#![cfg(feature = "config")]

mod common_config;

use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;

use common_config::{initial, wait_for, write_cfg, Recorder, ReloadCfg};
use hop_top_kit::bus::{Bus, SharedBus};
use hop_top_kit::config::{
    load, partition, ErrorKind, Field, Options, ReloadFields, ReloadOptions, ReloadTopics,
    Reloadable,
};

#[test]
fn partition_splits_tagged_fields() {
    let (m, i) = partition::<ReloadCfg>();
    assert_eq!(m, ["endpoint", "sub.threshold"]);
    assert_eq!(i, ["listen_addr", "sub.bind_host"]);
}

struct EmbedOuter;
impl ReloadFields for EmbedOuter {
    fn reload_fields() -> Vec<Field> {
        vec![
            Field::inline(vec![Field::leaf("a"), Field::leaf("b").reloadable()]),
            Field::leaf("c"),
        ]
    }
}

struct WholeBranch;
impl ReloadFields for WholeBranch {
    fn reload_fields() -> Vec<Field> {
        vec![Field::nested("db", vec![Field::leaf("host"), Field::leaf("port")]).reloadable()]
    }
}

#[test]
fn partition_inline_and_reloadable_branch() {
    assert_eq!(
        partition::<EmbedOuter>(),
        (
            vec!["b".to_string()],
            vec!["a".to_string(), "c".to_string()]
        )
    );
    let (m, i) = partition::<WholeBranch>();
    assert_eq!(m, ["db.host", "db.port"]);
    assert!(i.is_empty());
}

fn setup(rec: Option<Arc<Recorder>>) -> (tempfile::TempDir, Options, Reloadable<ReloadCfg>) {
    let dir = tempfile::tempdir().unwrap();
    let path = write_cfg(dir.path(), &initial());
    let opts = Options {
        project_config_path: path,
        ..Options::default()
    };
    let mut loaded = ReloadCfg::default();
    load(&mut loaded, &opts).unwrap();
    let mut ro = ReloadOptions::default();
    if let Some(r) = rec {
        ro = ro.publisher(r);
    }
    let r = Reloadable::new(loaded, opts.clone(), ro);
    (dir, opts, r)
}

#[test]
fn reload_snapshot_swaps() {
    let (dir, opts, r) = setup(None);
    assert_eq!(r.snapshot().endpoint, "https://api.example.com");
    let mut up = initial();
    up.endpoint = "https://api.example.org".into();
    up.sub.threshold = 9;
    write_cfg(dir.path(), &up);
    r.reload(opts).unwrap();
    let s = r.snapshot();
    assert_eq!(s.endpoint, "https://api.example.org");
    assert_eq!(s.sub.threshold, 9);
    assert_eq!(s.listen_addr, ":8080");
}

#[test]
fn reload_vetoes_immutable_change() {
    let rec = Arc::new(Recorder::default());
    let (dir, opts, r) = setup(Some(rec.clone()));
    let pre = r.snapshot();
    let mut up = initial();
    up.listen_addr = ":9090".into();
    write_cfg(dir.path(), &up);
    let err = r.reload(opts).unwrap_err();
    assert!(err.is(ErrorKind::ImmutableChanged));
    assert_eq!(err.immutable_paths(), ["listen_addr"]);
    assert_eq!(
        err.to_string(),
        "config reload vetoed: immutable field(s) changed: listen_addr"
    );
    assert!(Arc::ptr_eq(&pre, &r.snapshot()));
    let ev = wait_for(&rec, "kit.config.snapshot.reload_failed", 1);
    assert_eq!(ev.len(), 1);
    assert_eq!(ev[0].source, "core.config");
    assert_eq!(ev[0].payload["reason"], "immutable_changed");
    assert_eq!(
        ev[0].payload["offending"],
        serde_json::json!(["listen_addr"])
    );
    assert_eq!(
        ev[0].payload["mutable_diff"]["listen_addr"],
        serde_json::json!({"from": ":8080", "to": ":9090"})
    );
}

#[test]
fn reload_publishes_success_diff() {
    let rec = Arc::new(Recorder::default());
    let (dir, opts, r) = setup(Some(rec.clone()));
    let mut up = initial();
    up.endpoint = "https://api.example.net".into();
    write_cfg(dir.path(), &up);
    r.reload(opts.clone()).unwrap();
    let ev = wait_for(&rec, "kit.config.snapshot.reloaded", 1);
    assert_eq!(
        ev[0].payload["mutable_diff"]["endpoint"],
        serde_json::json!({"from": "https://api.example.com", "to": "https://api.example.net"})
    );
    let p = opts.project_config_path.display().to_string();
    assert_eq!(
        ev[0].payload["source_paths"],
        serde_json::json!(["", "", p])
    );
}

#[test]
fn reload_load_failure_leaves_snapshot() {
    let rec = Arc::new(Recorder::default());
    let (dir, opts, r) = setup(Some(rec.clone()));
    let mut bad = opts.clone();
    bad.extra_config_paths = vec![dir.path().join("does-not-exist.yaml")];
    let pre = r.snapshot();
    let err = r.reload(bad).unwrap_err();
    assert!(
        err.to_string().starts_with("config reload: load config "),
        "{err}"
    );
    assert!(Arc::ptr_eq(&pre, &r.snapshot()));
    let ev = wait_for(&rec, "kit.config.snapshot.reload_failed", 1);
    assert_eq!(ev[0].payload["reason"], "load_error");
    assert!(ev[0].payload.get("offending").is_none());
    // The event carries the cause unwrapped, as in Go.
    assert!(ev[0].payload["error"]
        .as_str()
        .unwrap()
        .starts_with("load config "));
}

#[test]
fn reload_concurrent_readers_safe() {
    let (dir, opts, r) = setup(None);
    let r = Arc::new(r);
    let stop = Arc::new(AtomicBool::new(false));
    let reads = Arc::new(AtomicU64::new(0));
    let readers: Vec<_> = (0..4)
        .map(|_| {
            let (r, stop, reads) = (r.clone(), stop.clone(), reads.clone());
            std::thread::spawn(move || {
                while !stop.load(Ordering::Relaxed) {
                    let s = r.snapshot();
                    let _ = (&s.endpoint, s.sub.threshold);
                    reads.fetch_add(1, Ordering::Relaxed);
                }
            })
        })
        .collect();
    for i in 0..50 {
        let mut up = initial();
        up.endpoint = format!("https://{i}");
        up.sub.threshold = i + 1;
        write_cfg(dir.path(), &up);
        r.reload(opts.clone()).unwrap();
    }
    stop.store(true, Ordering::Relaxed);
    for t in readers {
        t.join().unwrap();
    }
    assert!(reads.load(Ordering::Relaxed) > 0);
    assert_eq!(r.snapshot().endpoint, "https://49");
}

#[test]
fn reload_topics_prefix_and_bus_publisher() {
    let bus = SharedBus::new(Bus::new());
    let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
    let s2 = seen.clone();
    bus.subscribe("acme.config.settings.*", move |e| {
        s2.lock()
            .unwrap()
            .push((e.topic.0.clone(), e.source.clone()));
        Ok(())
    });
    let dir = tempfile::tempdir().unwrap();
    let opts = Options {
        project_config_path: write_cfg(dir.path(), &initial()),
        ..Options::default()
    };
    let r = Reloadable::new(
        initial(),
        opts.clone(),
        ReloadOptions::default()
            .publisher(Arc::new(bus))
            .topic_prefix("acme.config.settings")
            .source("svc"),
    );
    r.reload(opts).unwrap();
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
    while seen.lock().unwrap().is_empty() && std::time::Instant::now() < deadline {
        std::thread::sleep(std::time::Duration::from_millis(5));
    }
    assert_eq!(
        *seen.lock().unwrap(),
        [(
            "acme.config.settings.reloaded".to_string(),
            "svc".to_string()
        )]
    );

    let err = ReloadOptions::default()
        .try_topic_prefix("Bad.prefix")
        .unwrap_err();
    assert!(!err.to_string().is_empty());
    let t = ReloadOptions::default().topics(ReloadTopics {
        reloaded: "x.y.z.reloaded".into(),
        reload_failed: "".into(),
    });
    assert!(format!("{t:?}").contains("kit.config.snapshot.reload_failed"));
}

#[test]
fn reload_fields_drift_is_caught() {
    use hop_top_kit::config::{check_reload_fields, Field, ReloadFields};
    use serde::{Deserialize, Serialize};

    check_reload_fields::<ReloadCfg>().unwrap();

    #[derive(Default, Serialize, Deserialize)]
    #[serde(default)]
    struct Inner {
        a: i64,
    }
    #[derive(Default, Serialize, Deserialize)]
    #[serde(default)]
    struct Drifted {
        #[serde(rename = "listen")]
        listen_addr: String,
        added: bool,
        inner: Inner,
        #[serde(flatten)]
        flat: Inner2,
    }
    #[derive(Default, Serialize, Deserialize)]
    #[serde(default)]
    struct Inner2 {
        f: i64,
    }
    impl ReloadFields for Drifted {
        fn reload_fields() -> Vec<Field> {
            vec![
                Field::leaf("listen_addr"),
                Field::nested("inner", vec![Field::leaf("a"), Field::leaf("b")]),
                Field::inline(vec![Field::leaf("f")]),
            ]
        }
    }
    let err = check_reload_fields::<Drifted>().unwrap_err().to_string();
    assert_eq!(
        err,
        "config: ReloadFields drift: listen_addr: listed but not serialized; \
         inner.b: listed but not serialized; added: serialized but not listed; \
         listen: serialized but not listed"
    );
}
