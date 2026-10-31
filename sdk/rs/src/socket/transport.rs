//! The connection loop and the [`Service`] that hosts it.

use std::io;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use tokio::io::{AsyncBufRead, AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::sync::{mpsc, Notify};
use tokio::task::JoinSet;

use crate::serve::{CancelToken, ReadySignal, ServeFuture, Service};

use super::bind::{self, rand32};
use super::handler::{CommandMeta, Handlers};
use super::ledger::{Admission, Claim, Ledger, DEFAULT_TTL, REFUSE_CODE};
use super::peer::{Authenticator, Connection, PeerCred};
use super::wire::*;

/// How long a handler gets to return after its token fires.
const CANCEL_GRACE: Duration = Duration::from_secs(2);

type RefusedFn = dyn Fn(&Invocation, &str) + Send + Sync;
type PermissionFn = dyn Fn(&Invocation, &CommandMeta) -> Result<(), String> + Send + Sync;

/// The `surface` rules see here: Go's `cmdsurface.SurfaceSocket`.
const SURFACE: &str = "socket";

/// The `socket` service: NDJSON over a Unix domain socket, answered in
/// order, owner-only.
pub struct SocketService {
    path: PathBuf,
    handlers: Handlers,
    auth: Option<Arc<dyn Authenticator>>,
    on_refused: Option<Arc<RefusedFn>>,
    permission: Option<Arc<PermissionFn>>,
    ttl: Option<Duration>,
    ready: AtomicBool,
    addr: Mutex<Option<String>>,
    halt: CancelToken,
    stopped: Notify,
    running: AtomicBool,
}

impl SocketService {
    /// A service listening at `path`, dispatching to `handlers`.
    pub fn new(path: impl Into<PathBuf>, handlers: Handlers) -> Self {
        Self {
            path: path.into(),
            handlers,
            auth: None,
            on_refused: None,
            permission: None,
            ttl: None,
            ready: AtomicBool::new(false),
            addr: Mutex::new(None),
            halt: CancelToken::new(),
            stopped: Notify::new(),
            running: AtomicBool::new(false),
        }
    }

    /// Verify every request before it runs. See [`Authenticator`].
    pub fn authenticator(mut self, auth: impl Authenticator + 'static) -> Self {
        self.auth = Some(Arc::new(auth));
        self
    }

    /// Called for a request refused before reaching a handler (a failed
    /// authentication, a permission refusal) with the invocation as it would have been
    /// dispatched and the reason; route it to the audit stream.
    pub fn on_refused<F>(mut self, f: F) -> Self
    where
        F: Fn(&Invocation, &str) + Send + Sync + 'static,
    {
        self.on_refused = Some(Arc::new(f));
        self
    }

    /// Decides whether the caller may run the command: asked once the
    /// path resolves and before anything runs (Go's `PermissionFunc`
    /// slot). `Err` is the reason; the request answers `DENIED` with
    /// `cmdsurface: permission denied: <path> on socket: <reason>`, and
    /// `on_refused` hears of it.
    pub fn permission<F>(mut self, f: F) -> Self
    where
        F: Fn(&Invocation, &CommandMeta) -> Result<(), String> + Send + Sync + 'static,
    {
        self.permission = Some(Arc::new(f));
        self
    }

    /// Gates every command on `gate`: the policy's `callers:` rules,
    /// `permissions:` rules and `max_ops` budgets. The caller is the
    /// authenticator's verdict (verified), else the owner the `0600`
    /// socket file vouches for; a claim in the request never names it.
    /// A command's tier and scopes come from [`Handlers::annotate`].
    #[cfg(feature = "cli-permissions")]
    pub fn served_gate(self, gate: Arc<crate::cli::ServedGate>) -> Self {
        self.permission(move |inv, meta| {
            use crate::cli::{CallerSource, PermissionRequest, ServedCall};
            let mut req = PermissionRequest {
                surface: SURFACE.into(),
                path: inv.path.clone(),
                tier: meta.side_effect.clone(),
                args: inv.args.clone(),
                flags: inv.flags.clone().into_iter().collect(),
                ..PermissionRequest::default()
            };
            if inv.meta.established == Established::Verified {
                req.caller = inv.meta.caller.clone();
                req.tenant = inv.meta.tenant.clone();
                req.scopes = inv.meta.scopes.clone();
                req.source = CallerSource::Verified;
            } else {
                req.source = CallerSource::Transport;
            }
            gate.decide(&ServedCall {
                request: &req,
                permissions: Some(meta.permissions.as_str()).filter(|p| !p.is_empty()),
                probe: false,
            })
        })
    }

    /// Turn `idempotency_key` replay on, replaying a recorded result
    /// for `ttl` ([`DEFAULT_TTL`] when zero). Off by default: a key is
    /// then ignored.
    pub fn idempotency(mut self, ttl: Duration) -> Self {
        self.ttl = Some(if ttl.is_zero() { DEFAULT_TTL } else { ttl });
        self
    }
}

struct Shared {
    handlers: Handlers,
    auth: Option<Arc<dyn Authenticator>>,
    on_refused: Option<Arc<RefusedFn>>,
    permission: Option<Arc<PermissionFn>>,
    ledger: Option<Ledger>,
    server_uid: u32,
}

impl Service for SocketService {
    fn name(&self) -> &str {
        "socket"
    }

    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            if self.running.swap(true, Ordering::SeqCst) {
                return Err("socket: already started".to_string());
            }
            let bound = match bind::bind(&self.path) {
                Ok(b) => b,
                Err(e) => {
                    // Nothing runs, so stop has nothing to wait for.
                    self.running.store(false, Ordering::SeqCst);
                    return Err(e.to_string());
                }
            };
            *self.addr.lock().unwrap_or_else(PoisonError::into_inner) =
                Some(bound.path.display().to_string());
            self.ready.store(true, Ordering::SeqCst);
            ready.report();

            let shared = Arc::new(Shared {
                handlers: self.handlers.clone(),
                auth: self.auth.clone(),
                on_refused: self.on_refused.clone(),
                permission: self.permission.clone(),
                ledger: self.ttl.map(Ledger::new),
                server_uid: bound.uid,
            });
            let conn_cancel = CancelToken::new();
            let mut conns = JoinSet::new();
            let result = loop {
                tokio::select! {
                    _ = cancel.cancelled() => break Ok(()),
                    _ = self.halt.cancelled() => break Ok(()),
                    accepted = bound.listener.accept() => match accepted {
                        Ok((stream, _)) => {
                            conns.spawn(serve_conn(stream, shared.clone(), conn_cancel.clone()));
                        }
                        Err(e) => break Err(e.to_string()),
                    },
                    Some(_) = conns.join_next(), if !conns.is_empty() => {}
                }
            };
            self.ready.store(false, Ordering::SeqCst);
            drop(bound.listener);
            bind::unlink(&bound.path, bound.dev_ino);
            // Tear down in-flight connections so the drain stays inside
            // the stop budget.
            conn_cancel.cancel();
            while conns.join_next().await.is_some() {}
            *self.addr.lock().unwrap_or_else(PoisonError::into_inner) = None;
            self.running.store(false, Ordering::SeqCst);
            self.stopped.notify_waiters();
            result
        })
    }

    fn ready(&self) -> bool {
        self.ready.load(Ordering::SeqCst)
    }

    fn stop<'a>(&'a self, cancel: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            self.halt.cancel();
            while self.running.load(Ordering::SeqCst) {
                let notified = self.stopped.notified();
                tokio::pin!(notified);
                notified.as_mut().enable();
                if !self.running.load(Ordering::SeqCst) {
                    break;
                }
                tokio::select! {
                    _ = &mut notified => {}
                    _ = cancel.cancelled() => break,
                }
            }
            Ok(())
        })
    }

    fn addr(&self) -> Option<String> {
        self.addr
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }
}

enum Line {
    Eof,
    Text(Vec<u8>),
    TooLong,
}

/// One newline-terminated line, bounded by [`MAX_LINE_BYTES`]. A final
/// unterminated line counts, and a trailing `\r` is dropped.
async fn read_line<R: AsyncBufRead + Unpin>(r: &mut R) -> io::Result<Line> {
    let mut buf = Vec::new();
    loop {
        let chunk = r.fill_buf().await?;
        if chunk.is_empty() {
            return Ok(if buf.is_empty() {
                Line::Eof
            } else {
                Line::Text(buf)
            });
        }
        let (take, found) = match chunk.iter().position(|b| *b == b'\n') {
            Some(i) => (i, true),
            None => (chunk.len(), false),
        };
        if buf.len() + take > MAX_LINE_BYTES {
            return Ok(Line::TooLong);
        }
        buf.extend_from_slice(&chunk[..take]);
        r.consume(take + usize::from(found));
        if found {
            if buf.last() == Some(&b'\r') {
                buf.pop();
            }
            return Ok(Line::Text(buf));
        }
    }
}

/// Serves one connection: a request line in, a response line out, in
/// order, until the peer closes or the service stops.
///
/// Lines are read ahead of being served: a reader task keeps consuming
/// while a command runs, so a peer that hangs up mid-command is noticed
/// at once and the invocation in flight is canceled.
async fn serve_conn(stream: UnixStream, sh: Arc<Shared>, svc_cancel: CancelToken) {
    let conn = Connection::new(
        stream
            .peer_cred()
            .map(|c| PeerCred {
                uid: c.uid(),
                gid: c.gid(),
                pid: c.pid(),
            })
            .map_err(|e| e.to_string()),
        sh.server_uid,
    );
    let conn_cancel = svc_cancel.child();
    let (rd, mut wr) = stream.into_split();

    let (tx, mut rx) = mpsc::channel::<Line>(PIPELINE_DEPTH);
    let reader_cancel = conn_cancel.clone();
    let reader = tokio::spawn(async move {
        let mut rd = BufReader::new(rd);
        loop {
            let read = tokio::select! {
                r = read_line(&mut rd) => r,
                _ = reader_cancel.cancelled() => return,
            };
            match read {
                Ok(Line::Text(l)) => {
                    tokio::select! {
                        sent = tx.send(Line::Text(l)) => if sent.is_err() { return },
                        _ = reader_cancel.cancelled() => return,
                    }
                }
                Ok(Line::TooLong) => {
                    // Answered after the lines read before it; the rest
                    // of that line cannot be told from the next request.
                    let _ = tx.send(Line::TooLong).await;
                    return;
                }
                // The peer closing or a read error: no caller is left.
                Ok(Line::Eof) | Err(_) => {
                    reader_cancel.cancel();
                    return;
                }
            }
        }
    });

    loop {
        let line = tokio::select! {
            l = rx.recv() => match l { Some(l) => l, None => break },
            _ = conn_cancel.cancelled() => break,
        };
        if conn_cancel.is_cancelled() {
            break;
        }
        let resp = match line {
            Line::TooLong => {
                let r = Response::error(
                    CODE_INVALID,
                    format!("request line exceeds {MAX_LINE_BYTES} bytes"),
                );
                let _ = write_response(&mut wr, &r).await;
                break;
            }
            Line::Eof => break,
            Line::Text(l) if l.is_empty() => continue,
            Line::Text(l) => match dispatch(&sh, &conn, &l, &conn_cancel).await {
                Some(r) => r,
                None => break, // canceled mid-command; the result is discarded
            },
        };
        if write_response(&mut wr, &resp).await.is_err() {
            break;
        }
    }
    conn_cancel.cancel();
    reader.abort();
    let _ = wr.shutdown().await;
}

async fn write_response(wr: &mut tokio::net::unix::OwnedWriteHalf, r: &Response) -> io::Result<()> {
    let mut out = serde_json::to_vec(r).map_err(io::Error::other)?;
    out.push(b'\n');
    wr.write_all(&out).await
}

/// Decodes one line and runs it; `None` when the connection was
/// canceled before the command finished.
async fn dispatch(
    sh: &Shared,
    conn: &Connection,
    line: &[u8],
    cancel: &CancelToken,
) -> Option<Response> {
    let mut req: Request = match serde_json::from_slice(line) {
        Ok(r) => r,
        Err(e) => return Some(Response::error(CODE_INVALID, e.to_string())),
    };
    if req.path.is_empty() {
        return Some(Response::error(CODE_INVALID, "path is required"));
    }
    if req.request_id.is_empty() {
        req.request_id = new_request_id();
    }

    let mut meta = Meta {
        caller: req.caller.clone(),
        tenant: req.tenant.clone(),
        request_id: req.request_id.clone(),
        trace_id: req.trace_id.clone(),
        idempotency_key: req.idempotency_key.clone(),
        established: Established::Transport,
        scopes: Vec::new(),
        extra: Default::default(),
    };
    if let Some(auth) = &sh.auth {
        match auth.authenticate(conn, &req) {
            Err(refusal) => {
                meta.extra = refusal.extra.clone();
                meta.extra.remove("scopes");
                if let Some(f) = &sh.on_refused {
                    f(&invocation(&req, meta), &refusal.message);
                }
                return Some(Response::error(CODE_UNAUTHENTICATED, refusal.message));
            }
            Ok(id) => {
                // The verified identity replaces the claim.
                meta.caller = id.principal;
                meta.tenant = id.tenant;
                meta.established = Established::Verified;
                meta.extra = id.extra;
                meta.extra.remove("scopes");
                meta.scopes = id.scopes;
            }
        }
    }
    let inv = invocation(&req, meta);

    let Some((canonical, handler)) = sh.handlers.get(&inv.path) else {
        return Some(Response::error(
            CODE_NOT_FOUND,
            format!("unknown command: {}", inv.path.join(" ")),
        ));
    };
    // The route key joins segments with a space, so a segment holding one
    // resolves like two. The gate and the handler must see the command
    // that will run, not a spelling of it: refuse anything but the
    // registered segments.
    if inv.path != canonical {
        return Some(Response::error(
            CODE_INVALID,
            format!("non-canonical command path: {:?}", inv.path),
        ));
    }

    if let Some(permit) = &sh.permission {
        if let Err(reason) = permit(&inv, &sh.handlers.meta(&inv.path)) {
            let msg = format!(
                "cmdsurface: permission denied: {} on {SURFACE}: {reason}",
                inv.path.join(" ")
            );
            if let Some(f) = &sh.on_refused {
                f(&inv, &msg);
            }
            return Some(Response::error(CODE_DENIED, msg));
        }
    }

    let mut claim: Option<Claim> = None;
    if let (Some(ledger), false) = (&sh.ledger, inv.meta.idempotency_key.is_empty()) {
        match ledger.admit(&inv) {
            Admission::Refuse(msg) => return Some(Response::error(REFUSE_CODE, msg)),
            Admission::Replay(result) => {
                return Some(Response {
                    ok: true,
                    result: Some(result),
                    error: None,
                    replayed: true,
                })
            }
            Admission::Run(c) => claim = Some(c),
        }
    }

    // A task of its own isolates a panicking handler.
    let mut task = tokio::spawn(handler.call(inv, cancel.clone()));
    let joined = tokio::select! {
        j = &mut task => j,
        _ = cancel.cancelled() => {
            // The caller is gone or the service is stopping. The handler
            // has seen its token fire; it gets a moment to return, then
            // is abandoned. Its result is discarded either way, and the
            // claim drops, releasing the key.
            if tokio::time::timeout(CANCEL_GRACE, &mut task).await.is_err() {
                task.abort();
            }
            return None;
        }
    };
    match joined {
        Ok(Ok(result)) => {
            if let Some(c) = claim {
                c.settle(Some(&result));
            }
            Some(Response {
                ok: true,
                result: Some(result),
                error: None,
                replayed: false,
            })
        }
        Ok(Err(e)) => {
            if let Some(c) = claim {
                c.settle(None);
            }
            let mut r = Response::error(&e.code, e.message);
            if let (Some(after), Some(err)) = (e.retry_after, r.error.as_mut()) {
                err.retry_after_ms = Some(retry_after_ms(after));
            }
            Some(r)
        }
        Err(_) => {
            if let Some(c) = claim {
                c.settle(None);
            }
            Some(Response::error(CODE_INTERNAL, "handler panicked"))
        }
    }
}

fn invocation(req: &Request, meta: Meta) -> Invocation {
    Invocation {
        path: req.path.clone(),
        args: req.args.clone(),
        flags: req.flags.clone(),
        meta,
    }
}

/// Random rather than sequential: ids from two server instances must
/// not collide in a shared audit stream.
fn new_request_id() -> String {
    let mut s = String::with_capacity(32);
    for _ in 0..4 {
        s.push_str(&format!("{:08x}", rand32()));
    }
    s
}

/// `retry_after_ms`: whole milliseconds, rounded up, never below one.
fn retry_after_ms(d: Duration) -> i64 {
    i64::try_from(d.as_nanos().div_ceil(1_000_000)).map_or(i64::MAX, |ms| ms.max(1))
}
