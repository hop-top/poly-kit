//! The env-driven sink: `KIT_BUS_SINK` and `KIT_BUS_SINK_PATH` on
//! [`DynBus::new`].
//!
//! Ports `env_sink_test.go` from `go/runtime/bus`. Go asserts the bare
//! bus by its type; a `DynBus` hides its layers, so these pin the same
//! contract by effect: no file written, no reporter call. Env vars are
//! process-wide, so every test holds [`ENV`] and restores what it set.
//! Paths live under temp dirs; nothing touches the real home. Each body
//! runs under the `within` watchdog.

#![cfg(all(feature = "bus", not(target_arch = "wasm32")))]

#[path = "bus_async/exec.rs"]
mod exec;

use std::ffi::OsStr;
use std::path::Path;
use std::sync::{Arc, Mutex, MutexGuard};

use exec::within;
use hop_top_kit::bus::{
    reset_env_sink_err_reporter, set_env_sink_err_reporter, DynBus, Event, ENV_SINK_KIND,
    ENV_SINK_PATH,
};
use serde_json::{json, Value};

static ENV: Mutex<()> = Mutex::new(());

/// Holds [`ENV`], sets the two vars and a recording reporter, and
/// restores all three on drop.
struct EnvScope {
    reported: Arc<Mutex<Vec<String>>>,
    _guard: MutexGuard<'static, ()>,
}

impl EnvScope {
    fn new(kind: Option<&str>, path: Option<&Path>) -> Self {
        Self::new_os(kind.map(OsStr::new), path.map(Path::as_os_str))
    }

    fn new_os(kind: Option<&OsStr>, path: Option<&OsStr>) -> Self {
        let guard = ENV.lock().unwrap_or_else(|p| p.into_inner());
        match kind {
            Some(k) => std::env::set_var(ENV_SINK_KIND, k),
            None => std::env::remove_var(ENV_SINK_KIND),
        }
        match path {
            Some(p) => std::env::set_var(ENV_SINK_PATH, p),
            None => std::env::remove_var(ENV_SINK_PATH),
        }
        let reported = Arc::new(Mutex::new(Vec::new()));
        let r = Arc::clone(&reported);
        set_env_sink_err_reporter(move |e| r.lock().unwrap().push(e.to_string()));
        EnvScope {
            reported,
            _guard: guard,
        }
    }

    fn reported(&self) -> Vec<String> {
        self.reported.lock().unwrap().clone()
    }
}

impl Drop for EnvScope {
    fn drop(&mut self) {
        reset_env_sink_err_reporter();
        std::env::remove_var(ENV_SINK_KIND);
        std::env::remove_var(ENV_SINK_PATH);
    }
}

fn publish_and_close(bus: &DynBus) {
    let e = Event::new("kit.test.thing.recorded", "test", json!({"k": "v"}));
    bus.publish(&e).unwrap();
    bus.close().unwrap();
}

#[test]
fn env_bus_sink_jsonl_routes_events() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        let env = EnvScope::new(Some("jsonl"), Some(&path));
        publish_and_close(&DynBus::new());
        let data = std::fs::read_to_string(&path).expect("jsonl file");
        let line = data.lines().next().expect("jsonl file empty");
        let rec: Value = serde_json::from_str(line).unwrap();
        assert_eq!(rec["topic"], "kit.test.thing.recorded");
        assert_eq!(rec["source"], "test");
        assert_eq!(rec["payload"], json!({"k": "v"}));
        assert!(env.reported().is_empty(), "{:?}", env.reported());
    });
}

#[test]
fn env_bus_sink_unset_no_extra_sink() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        // Path set, kind empty: Go's "unset" case; a sink would create it.
        let env = EnvScope::new(Some(""), Some(&path));
        publish_and_close(&DynBus::new());
        assert!(!path.exists(), "no sink expected without {ENV_SINK_KIND}");
        assert!(env.reported().is_empty());
        drop(env);

        let env = EnvScope::new(None, Some(&path));
        publish_and_close(&DynBus::new());
        assert!(
            !path.exists(),
            "no sink expected with {ENV_SINK_KIND} unset"
        );
        assert!(env.reported().is_empty());
    });
}

#[test]
fn env_bus_sink_jsonl_missing_path_skipped() {
    within(|| {
        let env = EnvScope::new(Some("jsonl"), Some(Path::new("  ")));
        publish_and_close(&DynBus::new());
        let reported = env.reported();
        assert_eq!(
            reported,
            vec!["bus: KIT_BUS_SINK=jsonl but KIT_BUS_SINK_PATH is empty; skipping env sink"]
        );
        assert!(reported[0].contains(ENV_SINK_PATH));
    });
}

#[test]
fn env_bus_sink_unknown_kind_skipped() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("should-not-be-touched");
        let env = EnvScope::new(Some("parquet"), Some(&path));
        publish_and_close(&DynBus::new());
        assert!(!path.exists(), "unknown kind must not open the path");
        assert_eq!(
            env.reported(),
            vec![r#"bus: unknown KIT_BUS_SINK="parquet" (want "jsonl"); skipping env sink"#]
        );
    });
}

// ---- Rust pins ----

#[test]
fn env_bus_sink_kind_is_trimmed_and_case_insensitive() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        let env = EnvScope::new(Some(" JSONL "), Some(&path));
        publish_and_close(&DynBus::new());
        assert_eq!(std::fs::read_to_string(&path).unwrap().lines().count(), 1);
        assert!(env.reported().is_empty());
    });
}

#[test]
fn env_bus_sink_open_failure_is_reported_and_skipped() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let p = dir.path().display().to_string();
        let env = EnvScope::new(Some("jsonl"), Some(dir.path()));
        publish_and_close(&DynBus::new());
        assert_eq!(
            env.reported(),
            vec![format!(
                "bus: KIT_BUS_SINK=jsonl: open \"{p}\": open {p}: is a directory; skipping env sink"
            )]
        );
    });
}

#[test]
fn env_bus_sink_appends_across_buses() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        let _env = EnvScope::new(Some("jsonl"), Some(&path));
        publish_and_close(&DynBus::new());
        publish_and_close(&DynBus::new());
        assert_eq!(std::fs::read_to_string(&path).unwrap().lines().count(), 2);
    });
}

#[test]
fn env_bus_sink_wraps_every_other_layer() {
    within(|| {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("events.jsonl");
        let _env = EnvScope::new(Some("jsonl"), Some(&path));
        let seen = Arc::new(Mutex::new(Vec::<String>::new()));
        let s = Arc::clone(&seen);
        let bus = DynBus::builder()
            .invalid_topic_reporter(move |e| s.lock().unwrap().push(e.to_string()))
            .build();
        // The tee sits outside enforcement: Warn reports the bad topic,
        // the publish succeeds, and only then does the sink drain.
        bus.publish(&Event::new("Bad", "test", json!(null)))
            .unwrap();
        bus.close().unwrap();
        assert_eq!(seen.lock().unwrap().len(), 1, "{:?}", seen.lock().unwrap());
        assert_eq!(std::fs::read_to_string(&path).unwrap().lines().count(), 1);
    });
}

// Go's strings are bytes: a non-UTF-8 kind prints as `\xNN` under %q,
// and a non-UTF-8 path names that exact file, not a U+FFFD lookalike.
#[cfg(unix)]
#[test]
fn env_bus_sink_values_are_bytes() {
    use std::os::unix::ffi::{OsStrExt, OsStringExt};
    within(|| {
        let env = EnvScope::new_os(Some(OsStr::from_bytes(b"a\xff")), None);
        DynBus::new().close().unwrap();
        assert_eq!(
            env.reported(),
            vec![r#"bus: unknown KIT_BUS_SINK="a\xff" (want "jsonl"); skipping env sink"#]
        );
        drop(env);

        let dir = tempfile::tempdir().unwrap();
        let mut path = dir.path().as_os_str().as_bytes().to_vec();
        path.extend_from_slice(b"/ev\xff.jsonl");
        let path = std::ffi::OsString::from_vec(path);
        let env = EnvScope::new_os(Some(OsStr::new("jsonl")), Some(&path));
        publish_and_close(&DynBus::new());
        let names: Vec<Vec<u8>> = std::fs::read_dir(dir.path())
            .unwrap()
            .map(|e| e.unwrap().file_name().into_vec())
            .collect();
        match env.reported().as_slice() {
            // Created: under the raw name.
            [] => assert_eq!(names, vec![b"ev\xff.jsonl".to_vec()]),
            // Refused by the filesystem (APFS wants UTF-8): quoted as Go
            // quotes the bytes, and nothing created.
            [r] => {
                assert!(r.contains(r"ev\xff.jsonl"), "{r}");
                assert!(names.is_empty(), "{names:?}");
            }
            more => panic!("{more:?}"),
        }
    });
}
