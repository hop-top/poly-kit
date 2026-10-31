//! Recording implementations of the seams, for tests.
//!
//! Every call is appended to a per-implementation list of [`Call`]s.
//! [`assert_called`] and [`assert_not_called`] keep test code free of
//! matching boilerplate. An optional allow-list rejects unexpected calls
//! loudly, by panicking: the inverse of [`dryrun`](super::dryrun), which
//! tolerates anything.
//!
//! Canned answers are queued per call with `expect*`. Calls beyond the
//! queue fall back to the implementation's default (success, a 200 with
//! an empty JSON body for HTTP, empty output for `output`), which
//! `set_default*` overrides.
//!
//! Thread safe: every implementation guards its state with a mutex.

use std::collections::VecDeque;
use std::fmt;
use std::io;
use std::path::Path;
use std::sync::{Mutex, MutexGuard, PoisonError};

use serde_json::Value;

use super::seams::{
    self as seams, BoxFuture, Command, ExecError, HttpError, PublishError, Request, Response,
};
use crate::bus::Context;

/// One argument of a recorded call.
#[derive(Clone, Debug, PartialEq)]
pub enum Arg {
    Str(String),
    Bytes(Vec<u8>),
    /// A Unix mode, printed in octal.
    Mode(u32),
    Json(Value),
    /// A command's argv.
    List(Vec<String>),
}

impl fmt::Display for Arg {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Arg::Str(s) => f.write_str(s),
            Arg::Bytes(b) => write!(f, "{b:?}"),
            Arg::Mode(m) => write!(f, "0{m:o}"),
            Arg::Json(v) => write!(f, "{v}"),
            Arg::List(l) => write!(f, "[{}]", l.join(" ")),
        }
    }
}

/// A single recorded invocation: the method (`"FS.WriteFile"`) and its
/// arguments in declaration order.
#[derive(Clone, Debug, PartialEq)]
pub struct Call {
    pub method: String,
    pub args: Vec<Arg>,
}

impl Call {
    fn new(method: &str, args: Vec<Arg>) -> Self {
        Call {
            method: method.into(),
            args,
        }
    }
}

/// Renders the call in a stable shape for assertion errors:
/// `FS.Remove(/e)`.
impl fmt::Display for Call {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let args: Vec<_> = self.args.iter().map(Arg::to_string).collect();
        write!(f, "{}({})", self.method, args.join(", "))
    }
}

/// Reports whether a call is allowed.
pub type AllowFn = Box<dyn Fn(&Call) -> bool + Send + Sync>;

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    // A rejected call panics outside the lock, but a test panicking
    // elsewhere must not hide the recording.
    m.lock().unwrap_or_else(PoisonError::into_inner)
}

/// The recording shared by every fake: the calls and the allow-list.
#[derive(Default)]
struct Recorder {
    label: &'static str,
    calls: Mutex<Vec<Call>>,
    allow: Mutex<Vec<AllowFn>>,
}

impl Recorder {
    fn new(label: &'static str) -> Self {
        Recorder {
            label,
            ..Recorder::default()
        }
    }

    fn allow(&self, f: AllowFn) {
        lock(&self.allow).push(f);
    }

    fn calls(&self) -> Vec<Call> {
        lock(&self.calls).clone()
    }

    /// Records `c`, or panics if an allow-list exists and nothing in it
    /// matches (the call is then not recorded).
    fn record(&self, c: Call) {
        let allowed = {
            let allow = lock(&self.allow);
            allow.is_empty() || allow.iter().any(|f| f(&c))
        };
        assert!(allowed, "testfake/{}: unexpected call {c}", self.label);
        lock(&self.calls).push(c);
    }
}

/// Shared by the fakes with a queue of canned results and a default.
struct Script<T> {
    queue: Mutex<VecDeque<T>>,
}

impl<T> Default for Script<T> {
    fn default() -> Self {
        Script {
            queue: Mutex::new(VecDeque::new()),
        }
    }
}

impl<T> Script<T> {
    fn push(&self, v: T) {
        lock(&self.queue).push_back(v);
    }

    fn next(&self) -> Option<T> {
        lock(&self.queue).pop_front()
    }
}

macro_rules! recorder_api {
    ($ty:ident) => {
        impl $ty {
            /// Appends a predicate to the allow-list. A call is rejected
            /// (the fake panics) when at least one predicate is
            /// registered and none match; with none, everything is
            /// allowed.
            pub fn allow(self, f: impl Fn(&Call) -> bool + Send + Sync + 'static) -> Self {
                self.rec.allow(Box::new(f));
                self
            }

            /// A snapshot of the recorded calls, in invocation order.
            pub fn calls(&self) -> Vec<Call> {
                self.rec.calls()
            }
        }
    };
}

/// The recording [`Fs`](seams::Fs). Every method succeeds.
pub struct Fs {
    rec: Recorder,
}

impl Fs {
    pub fn new() -> Self {
        Fs {
            rec: Recorder::new("FS"),
        }
    }
}

impl Default for Fs {
    fn default() -> Self {
        Self::new()
    }
}

recorder_api!(Fs);

fn s(p: &Path) -> Arg {
    Arg::Str(p.display().to_string())
}

impl seams::Fs for Fs {
    fn write_file(&self, path: &Path, data: &[u8], mode: u32) -> io::Result<()> {
        let args = vec![s(path), Arg::Bytes(data.to_vec()), Arg::Mode(mode)];
        self.rec.record(Call::new("FS.WriteFile", args));
        Ok(())
    }

    fn mkdir_all(&self, path: &Path, mode: u32) -> io::Result<()> {
        self.rec
            .record(Call::new("FS.MkdirAll", vec![s(path), Arg::Mode(mode)]));
        Ok(())
    }

    fn rename(&self, from: &Path, to: &Path) -> io::Result<()> {
        self.rec
            .record(Call::new("FS.Rename", vec![s(from), s(to)]));
        Ok(())
    }

    fn remove(&self, path: &Path) -> io::Result<()> {
        self.rec.record(Call::new("FS.Remove", vec![s(path)]));
        Ok(())
    }
}

/// The recording [`Http`](seams::Http). Records `(method, url)`.
pub struct Http {
    rec: Recorder,
    script: Script<Result<Response, HttpError>>,
    default: Mutex<Option<Result<Response, HttpError>>>,
}

impl Http {
    pub fn new() -> Self {
        Http {
            rec: Recorder::new("HTTP"),
            script: Script::default(),
            default: Mutex::new(None),
        }
    }

    /// Queues an answer for the next `send`.
    pub fn expect(&self, r: Result<Response, HttpError>) -> &Self {
        self.script.push(r);
        self
    }

    /// Overrides the answer used once the queue empties (default: 200
    /// with an empty JSON body).
    pub fn set_default(&self, r: Result<Response, HttpError>) -> &Self {
        *lock(&self.default) = Some(r);
        self
    }
}

impl Default for Http {
    fn default() -> Self {
        Self::new()
    }
}

recorder_api!(Http);

impl seams::Http for Http {
    fn send(&self, req: Request) -> BoxFuture<'_, Result<Response, HttpError>> {
        let args = vec![Arg::Str(req.method), Arg::Str(req.url)];
        self.rec.record(Call::new("HTTP.Do", args));
        let r = self
            .script
            .next()
            .or_else(|| lock(&self.default).clone())
            .unwrap_or_else(|| Ok(Response::empty_json(200)));
        Box::pin(std::future::ready(r))
    }
}

/// The recording [`Bus`](seams::Bus). Records `(topic, source, payload)`.
pub struct Bus {
    rec: Recorder,
    script: Script<Result<(), PublishError>>,
    default: Mutex<Option<Box<DefaultFn>>>,
}

type DefaultFn = dyn Fn() -> Result<(), PublishError> + Send + Sync;

impl Bus {
    pub fn new() -> Self {
        Bus {
            rec: Recorder::new("Bus"),
            script: Script::default(),
            default: Mutex::new(None),
        }
    }

    /// Queues the result of the next `publish`.
    pub fn expect(&self, r: Result<(), PublishError>) -> &Self {
        self.script.push(r);
        self
    }

    /// Overrides the result once the queue empties (default: `Ok`).
    /// A function, as errors are not clonable.
    pub fn set_default(
        &self,
        f: impl Fn() -> Result<(), PublishError> + Send + Sync + 'static,
    ) -> &Self {
        *lock(&self.default) = Some(Box::new(f));
        self
    }
}

impl Default for Bus {
    fn default() -> Self {
        Self::new()
    }
}

recorder_api!(Bus);

impl seams::Bus for Bus {
    fn publish(
        &self,
        _: &Context,
        topic: &str,
        source: &str,
        payload: Value,
    ) -> Result<(), PublishError> {
        let args = vec![
            Arg::Str(topic.into()),
            Arg::Str(source.into()),
            Arg::Json(payload),
        ];
        self.rec.record(Call::new("Bus.Publish", args));
        match self.script.next() {
            Some(r) => r,
            None => lock(&self.default).as_ref().map_or(Ok(()), |f| f()),
        }
    }
}

/// The recording [`Exec`](seams::Exec). Records the argv.
pub struct Exec {
    rec: Recorder,
    run_q: Script<Result<(), ExecError>>,
    out_q: Script<Result<Vec<u8>, ExecError>>,
    run_default: Mutex<Result<(), ExecError>>,
    out_default: Mutex<Result<Vec<u8>, ExecError>>,
}

impl Exec {
    pub fn new() -> Self {
        Exec {
            rec: Recorder::new("Exec"),
            run_q: Script::default(),
            out_q: Script::default(),
            run_default: Mutex::new(Ok(())),
            out_default: Mutex::new(Ok(Vec::new())),
        }
    }

    /// Queues the result of the next `run`.
    pub fn expect_run(&self, r: Result<(), ExecError>) -> &Self {
        self.run_q.push(r);
        self
    }

    /// Queues the result of the next `output`.
    pub fn expect_output(&self, r: Result<Vec<u8>, ExecError>) -> &Self {
        self.out_q.push(r);
        self
    }

    /// Overrides the `run` result once its queue empties.
    pub fn set_default_run(&self, r: Result<(), ExecError>) -> &Self {
        *lock(&self.run_default) = r;
        self
    }

    /// Overrides the `output` result once its queue empties.
    pub fn set_default_output(&self, r: Result<Vec<u8>, ExecError>) -> &Self {
        *lock(&self.out_default) = r;
        self
    }
}

impl Default for Exec {
    fn default() -> Self {
        Self::new()
    }
}

recorder_api!(Exec);

impl seams::Exec for Exec {
    fn run(&self, cmd: &Command) -> Result<(), ExecError> {
        self.rec
            .record(Call::new("Exec.Run", vec![Arg::List(cmd.argv())]));
        self.run_q
            .next()
            .unwrap_or_else(|| lock(&self.run_default).clone())
    }

    fn output(&self, cmd: &Command) -> Result<Vec<u8>, ExecError> {
        self.rec
            .record(Call::new("Exec.Output", vec![Arg::List(cmd.argv())]));
        self.out_q
            .next()
            .unwrap_or_else(|| lock(&self.out_default).clone())
    }
}

/// Panics unless some call in `calls` satisfies `matches`.
#[track_caller]
pub fn assert_called(calls: &[Call], matches: impl Fn(&Call) -> bool) {
    assert!(
        calls.iter().any(matches),
        "AssertCalled: no recorded call matched\nrecorded: {}",
        render(calls)
    );
}

/// Panics if any call in `calls` satisfies `matches`.
#[track_caller]
pub fn assert_not_called(calls: &[Call], matches: impl Fn(&Call) -> bool) {
    if let Some(c) = calls.iter().find(|c| matches(c)) {
        panic!(
            "AssertNotCalled: unexpected call {c}\nrecorded: {}",
            render(calls)
        );
    }
}

fn render(calls: &[Call]) -> String {
    let v: Vec<_> = calls.iter().map(Call::to_string).collect();
    format!("[{}]", v.join(", "))
}
