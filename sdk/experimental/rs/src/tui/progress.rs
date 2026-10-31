//! Per-phase progress events for long-running commands: a port of
//! `go/console/progress`.
//!
//! Progress is metadata, not data: hand reporters stderr (or a test
//! buffer) and keep stdout for the data envelope. [`select`] applies the
//! flag precedence the Go CLI root uses, so a command never picks a
//! renderer itself.
//!
//! [`Reporter::emit`] is best effort: it returns nothing, never panics
//! and never blocks the work on a failed write. Reporters are
//! `Send + Sync`; one lock covers each line, so concurrent lines do not
//! interleave. Go threads the reporter through `context.Context`; here
//! pass an `Arc<dyn Reporter>` to whoever emits.

use super::sanitize::clean;
use std::collections::BTreeMap;
use std::io::Write;
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

/// A value in [`Event::extra`]: the JSON scalars.
#[derive(Debug, Clone, PartialEq)]
pub enum Value {
    Null,
    Bool(bool),
    Int(i64),
    /// A non-finite float cannot be encoded as JSON: the JSONL reporter
    /// drops the whole event, as Go's encoder fails on it.
    Float(f64),
    Str(String),
}

/// One progress update. Phase names are lowercase nouns (`resolve`,
/// `download`, `verify`).
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Event {
    pub phase: String,
    /// Filled by `emit` when `None`.
    pub at: Option<SystemTime>,
    pub item: String,
    pub bytes: i64,
    pub total: i64,
    pub ok: Option<bool>,
    pub extra: BTreeMap<String, Value>,
}

impl Event {
    pub fn new(phase: impl Into<String>) -> Event {
        Event {
            phase: phase.into(),
            ..Default::default()
        }
    }

    pub fn item(mut self, item: impl Into<String>) -> Event {
        self.item = item.into();
        self
    }

    pub fn bytes(mut self, bytes: i64, total: i64) -> Event {
        self.bytes = bytes;
        self.total = total;
        self
    }

    pub fn ok(mut self, ok: bool) -> Event {
        self.ok = Some(ok);
        self
    }

    pub fn at(mut self, at: SystemTime) -> Event {
        self.at = Some(at);
        self
    }

    pub fn extra(mut self, key: impl Into<String>, v: Value) -> Event {
        self.extra.insert(key.into(), v);
        self
    }
}

/// What a command calls to report a phase.
pub trait Reporter: Send + Sync {
    fn emit(&self, e: Event);
}

/// One [`Event`] per line as JSON. Keys in Go's order (`phase`, `at`,
/// `item`, `bytes`, `total`, `ok`, `extra`), zero optional fields
/// omitted, `at` as RFC 3339 in UTC, `<`, `>` and `&` escaped as Go does.
pub fn jsonl<W: Write + Send + 'static>(w: W) -> Arc<dyn Reporter> {
    Arc::new(Jsonl { w: Mutex::new(w) })
}

/// `[phase] item (bytes/total) ok|fail`; everything after the phase is
/// optional and bytes render as `<n>.<d> KiB`.
pub fn human<W: Write + Send + 'static>(w: W) -> Arc<dyn Reporter> {
    Arc::new(Human { w: Mutex::new(w) })
}

/// Drops every event.
pub fn discard() -> Arc<dyn Reporter> {
    Arc::new(Discard)
}

/// `--progress-format`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProgressFormat {
    Human,
    Json,
}

/// The reporter the CLI root wires for a command:
///
/// 1. `quiet` wins: [`discard`].
/// 2. An explicit `--progress-format` is honoured.
/// 3. Otherwise `--format json` (`data_is_json`) implies JSONL.
/// 4. Otherwise human lines.
pub fn select<W: Write + Send + 'static>(
    quiet: bool,
    format: Option<ProgressFormat>,
    data_is_json: bool,
    w: W,
) -> Arc<dyn Reporter> {
    if quiet {
        return discard();
    }
    match format {
        Some(ProgressFormat::Json) => jsonl(w),
        Some(ProgressFormat::Human) => human(w),
        None if data_is_json => jsonl(w),
        None => human(w),
    }
}

struct Discard;

impl Reporter for Discard {
    fn emit(&self, _: Event) {}
}

struct Jsonl<W> {
    w: Mutex<W>,
}

impl<W: Write + Send> Reporter for Jsonl<W> {
    fn emit(&self, e: Event) {
        let Some(line) = encode(&e) else { return };
        // A poisoned lock only means another emit panicked mid-write.
        let mut w = self.w.lock().unwrap_or_else(|p| p.into_inner());
        let _ = w.write_all(line.as_bytes());
    }
}

struct Human<W> {
    w: Mutex<W>,
}

impl<W: Write + Send> Reporter for Human<W> {
    fn emit(&self, e: Event) {
        let mut s = format!("[{}]", clean(&e.phase));
        if !e.item.is_empty() {
            s.push(' ');
            s.push_str(&clean(&e.item));
        }
        if e.total != 0 {
            s.push_str(&format!(" ({}/{})", kib(e.bytes), kib(e.total)));
        } else if e.bytes != 0 {
            s.push_str(&format!(" ({})", kib(e.bytes)));
        }
        if let Some(ok) = e.ok {
            s.push_str(if ok { " ok" } else { " fail" });
        }
        s.push('\n');
        let mut w = self.w.lock().unwrap_or_else(|p| p.into_inner());
        let _ = w.write_all(s.as_bytes());
    }
}

fn kib(b: i64) -> String {
    format!("{:.1} KiB", b as f64 / 1024.0)
}

// ── JSON ────────────────────────────────────────────────────────────────

/// `None` when the event cannot be encoded (non-finite float).
fn encode(e: &Event) -> Option<String> {
    let at = e.at.unwrap_or_else(SystemTime::now);
    let mut s = String::from("{\"phase\":");
    string(&mut s, &e.phase);
    s.push_str(",\"at\":\"");
    s.push_str(&rfc3339_nano(at));
    s.push('"');
    if !e.item.is_empty() {
        s.push_str(",\"item\":");
        string(&mut s, &e.item);
    }
    if e.bytes != 0 {
        s.push_str(&format!(",\"bytes\":{}", e.bytes));
    }
    if e.total != 0 {
        s.push_str(&format!(",\"total\":{}", e.total));
    }
    if let Some(ok) = e.ok {
        s.push_str(&format!(",\"ok\":{ok}"));
    }
    if !e.extra.is_empty() {
        s.push_str(",\"extra\":{");
        for (i, (k, v)) in e.extra.iter().enumerate() {
            if i > 0 {
                s.push(',');
            }
            string(&mut s, k);
            s.push(':');
            match v {
                Value::Null => s.push_str("null"),
                Value::Bool(b) => s.push_str(&b.to_string()),
                Value::Int(n) => s.push_str(&n.to_string()),
                Value::Float(f) if f.is_finite() => s.push_str(&f.to_string()),
                Value::Float(_) => return None,
                Value::Str(t) => string(&mut s, t),
            }
        }
        s.push('}');
    }
    s.push_str("}\n");
    Some(s)
}

fn string(out: &mut String, t: &str) {
    out.push('"');
    for c in t.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '<' | '>' | '&' | '\u{2028}' | '\u{2029}' => {
                out.push_str(&format!("\\u{:04x}", c as u32))
            }
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
}

/// RFC 3339 in UTC; fraction only when non-zero, trailing zeros trimmed
/// (Go's `RFC3339Nano`).
fn rfc3339_nano(t: SystemTime) -> String {
    let (secs, nanos) = match t.duration_since(UNIX_EPOCH) {
        Ok(d) => (d.as_secs() as i64, d.subsec_nanos()),
        Err(e) => {
            let d = e.duration();
            let (s, n) = (d.as_secs() as i64, d.subsec_nanos());
            if n == 0 {
                (-s, 0)
            } else {
                (-s - 1, 1_000_000_000 - n)
            }
        }
    };
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    // Howard Hinnant's civil_from_days.
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    let mut s = format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    );
    if nanos != 0 {
        let f = format!("{nanos:09}");
        s.push('.');
        s.push_str(f.trim_end_matches('0'));
    }
    s.push('Z');
    s
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io;
    use std::time::Duration;

    #[derive(Clone, Default)]
    struct Buf(Arc<Mutex<Vec<u8>>>);

    impl Write for Buf {
        fn write(&mut self, b: &[u8]) -> io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(b);
            Ok(b.len())
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    impl Buf {
        fn text(&self) -> String {
            String::from_utf8(self.0.lock().unwrap().clone()).unwrap()
        }
    }

    struct Broken;

    impl Write for Broken {
        fn write(&mut self, _: &[u8]) -> io::Result<usize> {
            Err(io::Error::new(io::ErrorKind::BrokenPipe, "broken pipe"))
        }
        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    fn at(secs: u64, nanos: u32) -> SystemTime {
        UNIX_EPOCH + Duration::new(secs, nanos)
    }

    fn json(text: &str) -> serde_json::Value {
        serde_json::from_str(text).unwrap()
    }

    #[test]
    fn jsonl_one_event_per_line() {
        let b = Buf::default();
        let r = jsonl(b.clone());
        r.emit(Event::new("resolve").item("github.com/foo/bar"));
        r.emit(Event::new("download").bytes(12345, 50000));
        r.emit(Event::new("verify").ok(true));
        let text = b.text();
        let lines: Vec<&str> = text.trim_end_matches('\n').split('\n').collect();
        assert_eq!(lines.len(), 3);
        for l in lines {
            let v = json(l);
            assert!(!v["phase"].as_str().unwrap().is_empty());
            assert!(!v["at"].as_str().unwrap().is_empty());
        }
    }

    #[test]
    fn jsonl_exact_line_matches_go() {
        let b = Buf::default();
        jsonl(b.clone()).emit(
            Event::new("download")
                .at(at(1_767_323_045, 0))
                .item("kit.tar.gz")
                .bytes(512 * 1024, 2048 * 1024),
        );
        assert_eq!(
            b.text(),
            "{\"phase\":\"download\",\"at\":\"2026-01-02T03:04:05Z\",\"item\":\"kit.tar.gz\",\
             \"bytes\":524288,\"total\":2097152}\n"
        );
    }

    #[test]
    fn jsonl_omits_zero_fields() {
        let b = Buf::default();
        jsonl(b.clone()).emit(Event::new("resolve").at(at(0, 0)));
        assert_eq!(
            b.text(),
            "{\"phase\":\"resolve\",\"at\":\"1970-01-01T00:00:00Z\"}\n"
        );
    }

    #[test]
    fn jsonl_ok_false_is_kept() {
        let b = Buf::default();
        jsonl(b.clone()).emit(Event::new("verify").ok(false));
        assert_eq!(json(&b.text())["ok"], false);
    }

    #[test]
    fn jsonl_fills_at_with_utc_now() {
        let b = Buf::default();
        jsonl(b.clone()).emit(Event::new("resolve"));
        let v = json(&b.text());
        let t = v["at"].as_str().unwrap();
        assert!(t.ends_with('Z') && t.len() >= 20, "{t}");
        assert!(t.starts_with("20"), "{t}");
    }

    #[test]
    fn rfc3339_fraction_and_epoch_edges() {
        assert_eq!(
            rfc3339_nano(at(1_767_323_045, 120_000_000)),
            "2026-01-02T03:04:05.12Z"
        );
        assert_eq!(
            rfc3339_nano(at(1_767_323_045, 1)),
            "2026-01-02T03:04:05.000000001Z"
        );
        // 2024-02-29T23:59:59Z, a leap day.
        assert_eq!(rfc3339_nano(at(1_709_251_199, 0)), "2024-02-29T23:59:59Z");
        assert_eq!(
            rfc3339_nano(UNIX_EPOCH - Duration::from_millis(500)),
            "1969-12-31T23:59:59.5Z"
        );
    }

    #[test]
    fn jsonl_extra_sorted_and_escaped() {
        let b = Buf::default();
        jsonl(b.clone()).emit(
            Event::new("p")
                .at(at(0, 0))
                .extra("z", Value::Int(1))
                .extra("a", Value::Str("x<\"\n".into()))
                .extra("n", Value::Null)
                .extra("f", Value::Float(1.5))
                .extra("b", Value::Bool(true)),
        );
        let text = b.text();
        assert!(text.contains(
            "\"extra\":{\"a\":\"x\\u003c\\\"\\n\",\"b\":true,\"f\":1.5,\"n\":null,\"z\":1}"
        ));
        assert_eq!(json(&text)["extra"]["a"], "x<\"\n");
    }

    #[test]
    fn jsonl_nan_drops_event() {
        let b = Buf::default();
        jsonl(b.clone()).emit(Event::new("p").extra("x", Value::Float(f64::NAN)));
        assert_eq!(b.text(), "");
    }

    #[test]
    fn human_renders_bytes_as_kib() {
        let b = Buf::default();
        human(b.clone()).emit(
            Event::new("download")
                .item("blob.tar.gz")
                .bytes(12345, 50000),
        );
        let out = b.text();
        assert_eq!(out, "[download] blob.tar.gz (12.1 KiB/48.8 KiB)\n");
        assert!(serde_json::from_str::<serde_json::Value>(&out).is_err());
    }

    #[test]
    fn human_omits_bytes_when_zero() {
        let b = Buf::default();
        human(b.clone()).emit(Event::new("resolve").item("github.com/foo/bar"));
        assert_eq!(b.text(), "[resolve] github.com/foo/bar\n");
    }

    #[test]
    fn human_bytes_only_no_total() {
        let b = Buf::default();
        human(b.clone()).emit(Event::new("download").bytes(2048, 0));
        let out = b.text();
        assert_eq!(out, "[download] (2.0 KiB)\n");
        assert!(!out.contains('/'));
    }

    #[test]
    fn human_strips_control_chars() {
        use crate::tui::sanitize::HOSTILE;
        for h in HOSTILE {
            let b = Buf::default();
            human(b.clone()).emit(Event::new(h).item(h));
            let out = b.text();
            assert!(
                !out.contains('\x1b') && !out.contains('\r') && !out.contains('\u{9b}'),
                "{out:?}"
            );
            assert_eq!(out.matches('\n').count(), 1, "{out:?}");
        }
    }

    #[test]
    fn jsonl_escapes_control_chars() {
        use crate::tui::sanitize::HOSTILE;
        for h in HOSTILE {
            let b = Buf::default();
            jsonl(b.clone()).emit(Event::new(h).item(h));
            let out = b.text();
            assert!(!out.contains('\x1b') && !out.contains('\r'), "{out:?}");
            assert_eq!(json(&out)["phase"], h);
        }
    }

    #[test]
    fn human_ok_status() {
        let b = Buf::default();
        let r = human(b.clone());
        r.emit(Event::new("verify").ok(true));
        r.emit(Event::new("verify").ok(false));
        assert_eq!(b.text(), "[verify] ok\n[verify] fail\n");
    }

    #[test]
    fn discard_writes_nothing() {
        let r = discard();
        for i in 0..1000 {
            r.emit(Event::new("spam").bytes(i, 0));
        }
        r.emit(Event::default());
    }

    #[test]
    fn broken_writer_does_not_panic() {
        jsonl(Broken).emit(Event::new("resolve"));
        human(Broken).emit(Event::new("resolve").item("x").bytes(1024, 2048));
    }

    #[test]
    fn concurrent_lines_do_not_interleave() {
        let b = Buf::default();
        let r = jsonl(b.clone());
        let hs: Vec<_> = (0..8)
            .map(|t| {
                let r = r.clone();
                std::thread::spawn(move || {
                    for i in 0..50 {
                        r.emit(Event::new(format!("t{t}")).bytes(i + 1, 0));
                    }
                })
            })
            .collect();
        hs.into_iter().for_each(|h| h.join().unwrap());
        let text = b.text();
        assert_eq!(text.lines().count(), 400);
        text.lines().for_each(|l| {
            json(l);
        });
    }

    #[test]
    fn select_precedence() {
        let pick = |q, f, j| {
            let b = Buf::default();
            select(q, f, j, b.clone()).emit(Event::new("p").at(at(0, 0)));
            b.text()
        };
        use ProgressFormat::*;
        assert_eq!(pick(true, Some(Json), true), "", "quiet wins");
        assert!(pick(false, Some(Json), false).starts_with('{'));
        assert_eq!(
            pick(false, Some(Human), true),
            "[p]\n",
            "explicit human beats --format json"
        );
        assert!(
            pick(false, None, true).starts_with('{'),
            "--format json implies JSONL"
        );
        assert_eq!(pick(false, None, false), "[p]\n");
    }
}
