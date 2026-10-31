//! The interceptors Go's `go/transport/rpc` ships, in kit's types:
//! request id, log, and `Authenticate` over kit-rs authn.

use std::sync::Arc;
use std::time::{Duration, Instant};

use super::call::{Authenticated, Call, RequestId};
use super::error::RpcError;
use super::interceptor::{Interceptor, Next, RpcFuture};
use crate::authn::{AuthFunc, AuthnError, Credentials};

const REQUEST_ID_HEADER: &str = "x-request-id";

/// Gives every call a request id: the caller's `X-Request-ID` when it
/// sent a plain token, else a fresh 128-bit hex id. The id is set back
/// on the request header and attached as [`RequestId`].
pub fn request_id() -> impl Interceptor {
    RequestIdInterceptor
}

struct RequestIdInterceptor;

impl Interceptor for RequestIdInterceptor {
    fn intercept<'a>(
        &'a self,
        mut call: Call,
        next: Next<'a>,
    ) -> RpcFuture<'a, Result<(), RpcError>> {
        Box::pin(async move {
            let id = call
                .headers
                .get(REQUEST_ID_HEADER)
                .filter(|v| valid_id(v))
                .map(str::to_string)
                .unwrap_or_else(new_id);
            call.headers.set(REQUEST_ID_HEADER, &id);
            call.ext.insert(RequestId(id));
            next.run(call).await
        })
    }
}

/// A plain token: what is safe to echo into a log line or a header.
fn valid_id(v: &str) -> bool {
    !v.is_empty()
        && v.len() <= 128
        && v.bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_' | b'.' | b':'))
}

fn new_id() -> String {
    let mut b = [0u8; 16];
    if getrandom::fill(&mut b).is_err() {
        let n = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.as_nanos());
        return format!("{n:x}");
    }
    b.iter().map(|x| format!("{x:02x}")).collect()
}

/// One finished call, as [`log`] reports it.
#[derive(Debug, Clone)]
pub struct RpcLog {
    pub procedure: String,
    pub duration: Duration,
    /// The failure's text, when the call failed.
    pub error: Option<String>,
}

impl RpcLog {
    /// `rpc request procedure=... duration=Nms error=...`, the fields
    /// Go logs.
    pub fn line(&self) -> String {
        format!(
            "rpc request procedure={} duration={}ms error={}",
            self.procedure,
            self.duration.as_millis(),
            self.error.as_deref().unwrap_or("<nil>")
        )
    }
}

/// Reports every call, with its procedure, duration and failure, to
/// `sink` once it ends. A stream is reported when it is established.
pub fn log<F>(sink: F) -> impl Interceptor
where
    F: Fn(&RpcLog) + Send + Sync + 'static,
{
    LogInterceptor(Arc::new(sink))
}

struct LogInterceptor(Arc<dyn Fn(&RpcLog) + Send + Sync>);

impl Interceptor for LogInterceptor {
    fn intercept<'a>(&'a self, call: Call, next: Next<'a>) -> RpcFuture<'a, Result<(), RpcError>> {
        Box::pin(async move {
            let procedure = call.procedure.clone();
            let start = Instant::now();
            let r = next.run(call).await;
            (self.0)(&RpcLog {
                procedure,
                duration: start.elapsed(),
                error: r.as_ref().err().map(|e| e.to_string()),
            });
            r
        })
    }
}

/// A call [`authenticate`] refused: what the transport knows before any
/// handler ran.
#[derive(Debug, Clone)]
pub struct RefusedCall {
    pub procedure: String,
    pub peer: Option<std::net::SocketAddr>,
}

/// Verifies every call, unary and streaming, with kit-rs authn before
/// the handler runs. A refusal is `unauthenticated` in the protocol the
/// client spoke; a call it accepts carries the verified
/// [`Claims`](crate::authn::Claims) and the [`Authenticated`] mark for
/// the handler's [`RequestContext`](super::RequestContext).
///
/// `func` sees the call's `Authorization` and `X-API-Key` headers, as in
/// the REST server; build it with `Verifier::auth_func` or
/// `ApiKeys::auth_func`.
///
/// Cost: an interceptor runs after the request body is read, within the
/// server's body limit. A flood of unauthenticated calls pays that read.
/// A credential check that must refuse earlier belongs in a tower layer
/// in front of the server, as the REST server does.
pub fn authenticate(func: AuthFunc) -> Authenticate {
    Authenticate {
        func,
        challenge: None,
        on_refused: None,
    }
}

type OnRefused = Arc<dyn Fn(&RefusedCall, &AuthnError) + Send + Sync>;

/// The `authenticate` interceptor; see its options.
pub struct Authenticate {
    func: AuthFunc,
    challenge: Option<String>,
    on_refused: Option<OnRefused>,
}

impl Authenticate {
    /// The `WWW-Authenticate` challenge a refusal carries, such as an
    /// OAuth protected resource's, so a client refused here finds the
    /// authorization server. Without it a refusal carries none.
    pub fn challenge(mut self, challenge: impl Into<String>) -> Self {
        self.challenge = Some(challenge.into());
        self
    }

    /// Called once for every refused call, before the refusal is sent:
    /// the audit seam, since a refused call reaches no handler.
    pub fn on_refused<F>(mut self, f: F) -> Self
    where
        F: Fn(&RefusedCall, &AuthnError) + Send + Sync + 'static,
    {
        self.on_refused = Some(Arc::new(f));
        self
    }
}

impl Interceptor for Authenticate {
    fn intercept<'a>(
        &'a self,
        mut call: Call,
        next: Next<'a>,
    ) -> RpcFuture<'a, Result<(), RpcError>> {
        Box::pin(async move {
            let verdict = (self.func)(&Credentials {
                authorization: call.headers.get("authorization"),
                api_key: call.headers.get("x-api-key"),
            });
            match verdict {
                Ok(claims) => {
                    call.ext.insert(claims);
                    call.ext.insert(Authenticated);
                    next.run(call).await
                }
                Err(e) => {
                    if let Some(f) = &self.on_refused {
                        f(
                            &RefusedCall {
                                procedure: call.procedure.clone(),
                                peer: call.peer,
                            },
                            &e,
                        );
                    }
                    let mut err = RpcError::unauthenticated(e.to_string());
                    if let Some(c) = &self.challenge {
                        err = err.with_meta("www-authenticate", c);
                    }
                    Err(err)
                }
            }
        })
    }
}
