//! The stdio binding: newline-delimited JSON-RPC over a pair of byte
//! streams (typically the process's stdin/stdout), driving the same era
//! dispatch [`Surface::call`] uses.
//!
//! Wire rules (stdio binding, 2024-11-05 and 2026-07-28):
//!
//! - one JSON-RPC message per line; output lines carry no embedded
//!   newline, and the output stream carries nothing but protocol
//!   messages — diagnostics go to the log stream;
//! - a notification (no `id` member) is never answered;
//! - each message is a single request: a JSON array (a batch) is
//!   refused with `-32600` and `id: null`;
//! - a line that is not JSON is answered `-32700` with `id: null`;
//! - requests run concurrently, one scoped thread each, so replies may
//!   arrive out of order — each carries its request's id.
//!
//! stdio has no header layer: era detection uses the body markers only
//! (`params._meta` protocolVersion, `server/discover`), and the
//! 2026-07-28 header checks (V4, V6, V7) do not apply. Gates that read
//! headers see none: the mount's verifier is called with a header-less
//! request, and the `X-Confirm-Token` header gate cannot be satisfied.

use std::any::Any;
use std::io::{self, BufRead, Write};
use std::panic::{self, AssertUnwindSafe};
use std::sync::Mutex;
use std::thread;

use serde_json::Value;

use super::dispatch::{go_parse_message, HttpRequest, Surface, Transport};
use super::wire::{codes, error_response, ErrorObject, Request};

/// Prefix on every diagnostic line written to the log stream.
const LOG_PREFIX: &str = "mcp stdio: ";

impl Surface {
    /// Serves this surface over stdio until `input` ends.
    ///
    /// Reads one JSON-RPC message per line from `input`, writes each
    /// reply as one line to `output`, and writes diagnostics to `log`;
    /// `output` carries protocol messages only. Requests run
    /// concurrently on scoped threads, so replies may arrive out of
    /// order, each with its request's id. Returns once `input` ends and
    /// every in-flight reply has been written.
    ///
    /// Mount options apply as for [`Surface::call`]. stdio has no header
    /// layer, so the 2026-07-28 header checks (V4, V6, V7) are skipped
    /// and era detection reads the body alone; the verifier sees a
    /// header-less request.
    ///
    /// # Errors
    ///
    /// The first read error on `input` or write error on `output`. Either
    /// ends the session (after in-flight requests finish) and is noted
    /// on `log` too.
    ///
    /// ```no_run
    /// use hop_top_kit::mcp::{Bridge, CallResult, Leaf, MountOptions, Surface};
    ///
    /// let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
    ///     Ok(CallResult::ok("pong\n"))
    /// }));
    /// let surface = Surface::mount(bridge, MountOptions::default()).unwrap();
    /// surface
    ///     .serve_stdio(std::io::stdin().lock(), std::io::stdout(), std::io::stderr())
    ///     .unwrap(); // the host closed stdin
    /// ```
    pub fn serve_stdio<R, W, L>(&self, input: R, output: W, log: L) -> io::Result<()>
    where
        R: BufRead,
        W: Write + Send,
        L: Write + Send,
    {
        let session = Session {
            surface: self,
            output: Mutex::new(output),
            log: Mutex::new(log),
            failure: Mutex::new(None),
        };
        thread::scope(|scope| session.read_loop(scope, input));
        match session
            .failure
            .into_inner()
            .unwrap_or_else(|e| e.into_inner())
        {
            Some(err) => Err(err),
            None => Ok(()),
        }
    }
}

/// One stdio session's shared state.
struct Session<'a, W, L> {
    surface: &'a Surface,
    output: Mutex<W>,
    log: Mutex<L>,
    /// The first I/O failure; once set, no further input is read.
    failure: Mutex<Option<io::Error>>,
}

impl<'a, W: Write + Send, L: Write + Send> Session<'a, W, L> {
    /// Reads lines until EOF or an I/O failure, spawning one scoped
    /// thread per dispatched request.
    fn read_loop<'scope, R: BufRead>(
        &'scope self,
        scope: &'scope thread::Scope<'scope, '_>,
        mut input: R,
    ) {
        let mut line = Vec::new();
        while !self.failed() {
            line.clear();
            match input.read_until(b'\n', &mut line) {
                Ok(0) => return,
                Ok(_) => {}
                // read_until already retries Interrupted.
                Err(err) => {
                    self.fail("input", err);
                    return;
                }
            }
            if let Some((request, body)) = self.frame(&line) {
                scope.spawn(move || self.serve(&request, body));
            }
        }
    }

    /// Frames and parses one input line. Returns the request to
    /// dispatch, or `None` when the line was answered (or dropped)
    /// here.
    fn frame(&self, line: &[u8]) -> Option<(Request, Vec<u8>)> {
        let text = line.trim_ascii();
        if text.is_empty() {
            return None;
        }

        let decoded: Value = match serde_json::from_slice(text) {
            Ok(decoded) => decoded,
            Err(err) => {
                self.write_error(
                    Some(&Value::Null),
                    codes::PARSE,
                    format!("parse error: {}", go_parse_message(text, &err)),
                );
                return None;
            }
        };
        let obj = match decoded {
            Value::Object(obj) => obj,
            Value::Array(_) => {
                self.write_error(
                    Some(&Value::Null),
                    codes::INVALID_REQUEST,
                    "invalid request: batch requests are not supported".into(),
                );
                return None;
            }
            _ => {
                self.write_error(
                    Some(&Value::Null),
                    codes::INVALID_REQUEST,
                    "invalid request: not a JSON-RPC object".into(),
                );
                return None;
            }
        };

        let Some(method) = obj.get("method").and_then(Value::as_str) else {
            // A response (result / error, no method): this server sends
            // no requests, so there is nothing to correlate it with.
            if obj.contains_key("result") || obj.contains_key("error") {
                self.warn(format_args!("ignored a JSON-RPC response from the client"));
                return None;
            }
            if let Some(id) = obj.get("id") {
                self.write_error(
                    Some(id),
                    codes::INVALID_REQUEST,
                    "invalid request: missing method".into(),
                );
            }
            return None;
        };

        let request = Request {
            jsonrpc: obj
                .get("jsonrpc")
                .and_then(Value::as_str)
                .map(ToOwned::to_owned),
            id: obj.get("id").cloned(),
            method: method.to_owned(),
            params: obj.get("params").cloned(),
        };
        Some((request, text.to_vec()))
    }

    /// Dispatches one request and writes its reply, if any.
    fn serve(&self, request: &Request, body: Vec<u8>) {
        // No headers: stdio has none, and none are invented.
        let http = HttpRequest {
            method: "POST".into(),
            path: self.surface.path().to_owned(),
            headers: Vec::new(),
            body,
        };
        let outcome = panic::catch_unwind(AssertUnwindSafe(|| {
            self.surface.dispatch(&http, request, Transport::Stdio)
        }));
        // A notification is never answered, whatever the handler said.
        let is_notification = request.id.is_none();
        match outcome {
            Ok(response) => {
                if !is_notification && !response.body.is_empty() {
                    self.write_line(&response.body);
                }
            }
            Err(payload) => {
                self.warn(format_args!(
                    "{}: {}",
                    request.method,
                    panic_message(payload.as_ref())
                ));
                if !is_notification {
                    self.write_error(
                        request.id.as_ref(),
                        codes::INTERNAL,
                        "internal error".into(),
                    );
                }
            }
        }
    }

    /// Writes one JSON-RPC error line.
    fn write_error(&self, id: Option<&Value>, code: i64, message: String) {
        let response = error_response(
            0,
            id,
            &ErrorObject {
                code,
                message,
                data: None,
            },
        );
        self.write_line(&response.body);
    }

    /// Writes one already-encoded message, newline-terminated, and
    /// flushes it so the host sees each reply as it completes.
    fn write_line(&self, body: &[u8]) {
        if self.failed() {
            return;
        }
        let result = {
            let mut out = self.output.lock().unwrap_or_else(|e| e.into_inner());
            let mut res = out.write_all(body);
            if res.is_ok() && !body.ends_with(b"\n") {
                res = out.write_all(b"\n");
            }
            res.and_then(|()| out.flush())
        };
        if let Err(err) = result {
            self.fail("output", err);
        }
    }

    /// Writes one diagnostic line to the log stream. A failing log is
    /// not a protocol failure, so its errors are dropped.
    fn warn(&self, msg: std::fmt::Arguments<'_>) {
        let mut log = self.log.lock().unwrap_or_else(|e| e.into_inner());
        let _ = writeln!(log, "{LOG_PREFIX}{msg}");
        let _ = log.flush();
    }

    /// Records the first I/O failure, which stops reading.
    fn fail(&self, stream: &str, err: io::Error) {
        self.warn(format_args!("{stream} error: {err}"));
        let mut failure = self.failure.lock().unwrap_or_else(|e| e.into_inner());
        if failure.is_none() {
            *failure = Some(err);
        }
    }

    fn failed(&self) -> bool {
        self.failure
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .is_some()
    }
}

/// The message a panic carried, when it was a string.
fn panic_message(payload: &(dyn Any + Send)) -> &str {
    if let Some(s) = payload.downcast_ref::<&str>() {
        s
    } else if let Some(s) = payload.downcast_ref::<String>() {
        s
    } else {
        "panic"
    }
}
