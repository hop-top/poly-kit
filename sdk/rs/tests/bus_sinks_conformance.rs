//! Runs the shared sink corpus in `contracts/bus-v1/sinks`: the bytes
//! [`StdoutSink`] and [`JsonlSink`] write for `events.json`, and the
//! env-sink reporter lines for `env_cases.json`, each byte-equal to the
//! golden the Go runner checks. Env vars and the working directory are
//! process-wide, so this is the only test in its binary. The body runs
//! under the `within` watchdog.

#![cfg(all(feature = "bus", not(target_arch = "wasm32")))]

#[path = "bus_async/exec.rs"]
mod exec;

use std::io::{self, Write};
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use exec::within;
use hop_top_kit::bus::{
    reset_env_sink_err_reporter, set_env_sink_err_reporter, Context, DynBus, Event, JsonlSink,
    Sink, StdoutSink, ENV_SINK_KIND, ENV_SINK_PATH,
};
use serde::Deserialize;
use serde_json::Value;

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/bus-v1/sinks")
}

fn read(name: &str) -> String {
    std::fs::read_to_string(dir().join(name)).unwrap_or_else(|e| panic!("{name}: {e}"))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Events {
    version: String,
    #[allow(dead_code)]
    description: String,
    events: Vec<CorpusEvent>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CorpusEvent {
    topic: String,
    source: String,
    timestamp: String,
    #[serde(default)]
    payload: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EnvCases {
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<EnvCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EnvCase {
    kind: String,
    path: String,
}

#[derive(Clone, Default)]
struct Buf(Arc<Mutex<Vec<u8>>>);

impl Buf {
    fn text(&self) -> String {
        String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
    }
}

impl Write for Buf {
    fn write(&mut self, b: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(b);
        Ok(b.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn sink_conformance() {
    within(|| {
        let corpus: Events = serde_json::from_str(&read("events.json")).unwrap();
        assert_eq!(corpus.version, "v1");
        assert!(!corpus.events.is_empty());

        let (out, jsonl) = (Buf::default(), Buf::default());
        let stdout = StdoutSink::with_writer(out.clone());
        let js = JsonlSink::new(jsonl.clone());
        let ctx = Context::background();
        for c in corpus.events {
            let mut e = Event::new(c.topic.as_str(), c.source, c.payload);
            e.timestamp = c.timestamp;
            stdout.drain(&ctx, &e).unwrap();
            js.drain(&ctx, &e).unwrap();
        }
        js.close().unwrap();
        assert_eq!(out.text(), read("stdout.txt"), "stdout.txt drifted");
        assert_eq!(jsonl.text(), read("jsonl.txt"), "jsonl.txt drifted");

        let cases: EnvCases = serde_json::from_str(&read("env_cases.json")).unwrap();
        assert_eq!(cases.version, "v1");
        let reports = Arc::new(Mutex::new(Vec::<String>::new()));
        let r = Arc::clone(&reports);
        set_env_sink_err_reporter(move |e| r.lock().unwrap().push(e.to_string()));
        // Case paths are relative: run them in an empty temp dir, which
        // must stay empty, as Go's runner fails on any attached sink.
        let tmp = tempfile::tempdir().unwrap();
        let cwd = std::env::current_dir().unwrap();
        std::env::set_current_dir(tmp.path()).unwrap();
        for c in &cases.cases {
            std::env::set_var(ENV_SINK_KIND, &c.kind);
            std::env::set_var(ENV_SINK_PATH, &c.path);
            DynBus::new().close().unwrap();
        }
        std::env::set_current_dir(cwd).unwrap();
        reset_env_sink_err_reporter();
        std::env::remove_var(ENV_SINK_KIND);
        std::env::remove_var(ENV_SINK_PATH);
        let made: Vec<_> = std::fs::read_dir(tmp.path()).unwrap().collect();
        assert!(made.is_empty(), "a case attached a sink: {made:?}");
        let got = reports.lock().unwrap().join("\n") + "\n";
        assert_eq!(got, read("env_reports.txt"), "env_reports.txt drifted");
    });
}
