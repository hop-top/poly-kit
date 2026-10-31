//! The webhook sink: bus events POSTed to an HTTP endpoint. Gated behind
//! `notify-webhook`; native only.
//!
//! Ports `go/runtime/notify/sinks/webhook`. The pipeline per event is
//! `template render -> redactor -> breaker -> POST`. Egress goes through
//! the guarded client, so an offline [`NetPolicy`] refuses a remote
//! endpoint and permits loopback. A non-2xx response is an error, so a
//! [`RetrySink`](super::RetrySink) can act on it.
//!
//! `drain` is synchronous: it runs the request on a current-thread tokio
//! runtime on a helper thread, so it is safe to call from inside an async
//! task. It honours the context's cancellation and deadline.
//!
//! Redirects are not followed (Go's client follows up to ten): the body
//! and credentials would move to a host the server names. A 3xx is a
//! non-2xx error.

use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use crate::breaker::Breaker;
use crate::bus::{BusError, Context, ContextError, Event, Sink};
use crate::netpolicy::{GuardedClient, NetError, NetPolicy, OFFLINE_MESSAGE};
use crate::redact::Redactor;

use super::template::{Template, TemplateError, TextTemplate};

/// The default overall request deadline, Go's `defaultTimeout`.
pub const DEFAULT_TIMEOUT: Duration = Duration::from_secs(5);

/// The most of an error response's body kept for the error text.
const PREVIEW_BYTES: usize = 512;

/// Renders an event into the request body and its `Content-Type`.
/// Implementations must be safe for concurrent use.
pub trait WebhookTemplate: Send + Sync {
    /// The body bytes and the content type to send.
    fn render(&self, event: &Event) -> Result<(Vec<u8>, String), TemplateError>;
}

impl<F> WebhookTemplate for F
where
    F: Fn(&Event) -> Result<(Vec<u8>, String), TemplateError> + Send + Sync,
{
    fn render(&self, event: &Event) -> Result<(Vec<u8>, String), TemplateError> {
        self(event)
    }
}

/// The default template: the whole event as one JSON object,
/// `application/json`. The shape `JsonlSink` writes per line, so a
/// receiver can be a generic event collector. Go's `DefaultJSONTemplate`.
#[derive(Clone, Copy, Debug, Default)]
pub struct JsonTemplate;

impl WebhookTemplate for JsonTemplate {
    fn render(&self, event: &Event) -> Result<(Vec<u8>, String), TemplateError> {
        let body = serde_json::to_vec(event)
            .map_err(|e| TemplateError::from_message(format!("webhook: marshal event: {e}")))?;
        Ok((body, "application/json".to_string()))
    }
}

/// The Slack incoming-webhook shape: `{"text": "<rendered>"}`, the text
/// from a `{{.Field}}` [`TextTemplate`]. Go's `SlackTemplate`.
#[derive(Clone, Debug)]
pub struct SlackTemplate(TextTemplate);

impl SlackTemplate {
    /// Parses `tmpl` now, so a bad template fails at construction rather
    /// than on the first event.
    pub fn new(tmpl: &str) -> Result<Self, TemplateError> {
        TextTemplate::parse(tmpl)
            .map(SlackTemplate)
            .map_err(|e| TemplateError::from_message(format!("webhook: parse slack template: {e}")))
    }
}

impl WebhookTemplate for SlackTemplate {
    fn render(&self, event: &Event) -> Result<(Vec<u8>, String), TemplateError> {
        let text = self.0.render(event)?;
        let body = serde_json::to_vec(&serde_json::json!({ "text": text })).map_err(|e| {
            TemplateError::from_message(format!("webhook: marshal slack body: {e}"))
        })?;
        Ok((body, "application/json".to_string()))
    }
}

/// POSTs rendered events to a URL. Go's `webhooksink.Sink`.
///
/// Construction does no I/O and cannot fail; the HTTP client is built on
/// the first drain, from the policy and timeout, unless one was given
/// with [`WebhookSink::client`].
pub struct WebhookSink {
    url: String,
    headers: Vec<(String, String)>,
    template: Box<dyn WebhookTemplate>,
    timeout: Duration,
    policy: NetPolicy,
    custom: Option<GuardedClient>,
    built: Mutex<Option<GuardedClient>>,
    redactor: Option<Arc<Redactor>>,
    breaker: Option<Arc<Breaker>>,
}

impl WebhookSink {
    /// A sink for `url`: the JSON template, a five second timeout, an
    /// online policy, no redactor and no breaker.
    pub fn new(url: impl Into<String>) -> Self {
        WebhookSink {
            url: url.into(),
            headers: Vec::new(),
            template: Box::new(JsonTemplate),
            timeout: DEFAULT_TIMEOUT,
            policy: NetPolicy::online(),
            custom: None,
            built: Mutex::new(None),
            redactor: None,
            breaker: None,
        }
    }

    /// Adds a header to every request. Calls accumulate, and a repeated
    /// key keeps every value (an add, not a set): Go's `WithHeader`.
    pub fn header(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((key.into(), value.into()));
        self
    }

    /// Sets `Authorization: Bearer <token>`: Go's `WithAuthBearer`.
    pub fn auth_bearer(self, token: &str) -> Self {
        self.header("Authorization", format!("Bearer {token}"))
    }

    /// Replaces the default JSON template: Go's `WithTemplate`.
    pub fn template(mut self, t: impl WebhookTemplate + 'static) -> Self {
        self.template = Box::new(t);
        self
    }

    /// The overall request deadline. Ignored when a client is given.
    pub fn timeout(mut self, d: Duration) -> Self {
        self.timeout = d;
        self
    }

    /// The network policy of the client the sink builds, so `--offline`
    /// applies. Ignored when a client is given (it carries its own).
    pub fn policy(mut self, p: NetPolicy) -> Self {
        self.policy = p;
        self
    }

    /// Uses `c` instead of building one: Go's `WithHTTPClient`. The
    /// timeout and policy options are then ignored; the breaker still
    /// applies.
    pub fn client(mut self, c: GuardedClient) -> Self {
        self.custom = Some(c);
        self
    }

    /// Runs the rendered body through `r` before egress: Go's
    /// `WithRedactor`.
    pub fn redactor(mut self, r: Arc<Redactor>) -> Self {
        self.redactor = Some(r);
        self
    }

    /// Gates egress on `b`: Go's `WithBreaker`. An open circuit fails
    /// the drain with [`BROKEN_CIRCUIT`](super::BROKEN_CIRCUIT) in its
    /// message, before any I/O. The outcome recorded is the transport's,
    /// as in Go: a non-2xx response is a success to the breaker.
    pub fn breaker(mut self, b: Arc<Breaker>) -> Self {
        self.breaker = Some(b);
        self
    }

    fn guarded(&self) -> Result<GuardedClient, String> {
        if let Some(c) = &self.custom {
            return Ok(c.clone());
        }
        let mut slot = self.built.lock().unwrap_or_else(PoisonError::into_inner);
        if let Some(c) = &*slot {
            return Ok(c.clone());
        }
        let c = GuardedClient::build(
            self.policy,
            reqwest::Client::builder()
                .timeout(self.timeout)
                .redirect(reqwest::redirect::Policy::none()),
        )
        .map_err(|e| format!("webhook: http client: {e}"))?;
        *slot = Some(c.clone());
        Ok(c)
    }

    fn post(&self, ctx: &Context, body: Vec<u8>, content_type: String) -> Result<(), String> {
        let client = self.guarded()?;
        let mut req = client
            .post(self.url.as_str())
            .header("Content-Type", &content_type);
        for (k, v) in &self.headers {
            req = req.header(k, v);
        }
        let req = req.body(body);
        let dest = destination(&self.url);
        let sent = block_on_ctx(ctx, async move {
            let mut resp = req
                .send()
                .await
                .map_err(|e| format!("webhook: post {dest}: {}", failure(e)))?;
            let status = resp.status();
            if status.is_success() {
                return Ok(());
            }
            let mut preview = Vec::new();
            while preview.len() < PREVIEW_BYTES {
                match resp.chunk().await {
                    Ok(Some(c)) => preview.extend_from_slice(&c),
                    _ => break,
                }
            }
            preview.truncate(PREVIEW_BYTES);
            Err(format!(
                "webhook: http {}: {}",
                status.as_u16(),
                String::from_utf8_lossy(&preview).trim()
            ))
        });
        match sent {
            Ok(r) => r,
            Err(e) => Err(format!("webhook: post: {e}")),
        }
    }
}

impl Sink for WebhookSink {
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        let (mut body, content_type) = self
            .template
            .render(event)
            .map_err(|e| BusError::handler(format!("webhook: render: {e}")))?;
        if let Some(r) = &self.redactor {
            body = redact_bytes(r, body);
        }
        if let Some(b) = &self.breaker {
            if let Err(e) = b.allow() {
                return Err(BusError::handler(format!("webhook: post: {e}")));
            }
        }
        let out = self.post(ctx, body, content_type);
        if let Some(b) = &self.breaker {
            b.record(!matches!(&out, Err(m) if m.starts_with("webhook: post")), 0);
        }
        out.map_err(BusError::handler)
    }

    /// A no-op: the sink owns no connection and a caller-supplied client
    /// is the caller's.
    fn close(&self) -> Result<(), BusError> {
        Ok(())
    }
}

/// The scheme, host and port of `url`, and nothing else: a webhook URL
/// often carries its token in the path, query or userinfo, so none of
/// those may reach an error. Unparseable input names no destination.
fn destination(url: &str) -> String {
    match reqwest::Url::parse(url) {
        Ok(u) => match (u.host_str(), u.port()) {
            (Some(h), Some(p)) => format!("{}://{h}:{p}", u.scheme()),
            (Some(h), None) => format!("{}://{h}", u.scheme()),
            _ => format!("{}:", u.scheme()),
        },
        Err(_) => "<invalid url>".to_string(),
    }
}

/// The cause of a failed request without the URL reqwest quotes. An
/// offline refusal is reduced to its message, as its text names the path.
fn failure(e: NetError) -> String {
    match e {
        NetError::Offline(_) => OFFLINE_MESSAGE.to_string(),
        NetError::Transport(r) => chain(&r.without_url()),
    }
}

/// `e` and its sources joined with `: `, so a timeout or a refused
/// connection shows its cause and not just "error sending request".
fn chain(e: &dyn std::error::Error) -> String {
    let mut out = e.to_string();
    let mut src = e.source();
    while let Some(s) = src {
        let t = s.to_string();
        if !out.contains(&t) {
            out.push_str(": ");
            out.push_str(&t);
        }
        src = s.source();
    }
    out
}

/// Redacts a rendered body. Go's `ApplyBytes`; a body that is not UTF-8
/// goes through lossily.
fn redact_bytes(r: &Redactor, body: Vec<u8>) -> Vec<u8> {
    match String::from_utf8(body) {
        Ok(s) => r.apply(&s).into_owned().into_bytes(),
        Err(e) => r
            .apply(&String::from_utf8_lossy(e.as_bytes()))
            .into_owned()
            .into_bytes(),
    }
}

/// Runs `fut` to completion on a current-thread runtime on its own
/// thread, giving up with the context's error if it is canceled or its
/// deadline passes first. The thread keeps a caller already inside a
/// runtime from tripping tokio's nested `block_on` panic.
fn block_on_ctx<T: Send>(
    ctx: &Context,
    fut: impl std::future::Future<Output = T> + Send,
) -> Result<T, ContextError> {
    std::thread::scope(|s| {
        s.spawn(|| {
            let rt = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .expect("current-thread runtime");
            rt.block_on(run_until_done(ctx, fut))
        })
        .join()
        .unwrap_or_else(|p| std::panic::resume_unwind(p))
    })
}

async fn run_until_done<T>(
    ctx: &Context,
    fut: impl std::future::Future<Output = T>,
) -> Result<T, ContextError> {
    let canceled = Arc::new(tokio::sync::Notify::new());
    let wake = Arc::clone(&canceled);
    let _watch = ctx.on_cancel(Arc::new(move || wake.notify_one()));
    if let Some(e) = ctx.err() {
        return Err(e);
    }
    let left = ctx
        .deadline()
        .map(|d| d.saturating_duration_since(crate::bus::context::now()));
    let deadline = async {
        match left {
            Some(d) => tokio::time::sleep(d).await,
            None => std::future::pending().await,
        }
    };
    tokio::select! {
        out = fut => Ok(out),
        () = canceled.notified() => Err(ctx.err().unwrap_or(ContextError::Canceled)),
        () = deadline => Err(ContextError::DeadlineExceeded),
    }
}
