//! The email sink: bus events as email, through a pluggable [`Mailer`].
//! Gated behind `notify-email`; native only.
//!
//! Ports `go/runtime/notify/sinks/email`. The pipeline per event is
//! subject and body templates, a redactor on each rendered string, then
//! the breaker-gated [`Mailer::send`]. Construction cannot fail: the
//! recipients, the mailer and both templates are checked at drain time,
//! so a misconfigured sink reports per event.
//!
//! [`SmtpMailer`] is the shipped mailer.

mod smtp;

use std::sync::Arc;

use crate::breaker::Breaker;
use crate::bus::{BusError, Context, Event, Sink};
use crate::redact::Redactor;

use super::template::Template;

pub use smtp::{SmtpError, SmtpMailer};

/// The content type used when none is set.
pub const DEFAULT_CONTENT_TYPE: &str = "text/plain; charset=utf-8";

/// A fully rendered message: subject and body are redacted when the
/// sink has a redactor.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Message {
    /// The sender; the mailer's default applies when empty.
    pub from: String,
    /// The recipients.
    pub to: Vec<String>,
    /// The `Content-Type` header value.
    pub content_type: String,
    /// The subject.
    pub subject: String,
    /// The body.
    pub body: String,
}

/// Sends a rendered [`Message`]: over SMTP, an HTTP API, or captured by
/// a test. Go's `Mailer`; an error is a bus handler error, whose text the
/// sink returns as it is.
pub trait Mailer: Send + Sync {
    /// Sends `msg`, giving up when `ctx` is done where the transport can.
    fn send(&self, ctx: &Context, msg: &Message) -> Result<(), BusError>;
}

impl<F> Mailer for F
where
    F: Fn(&Context, &Message) -> Result<(), BusError> + Send + Sync,
{
    fn send(&self, ctx: &Context, msg: &Message) -> Result<(), BusError> {
        self(ctx, msg)
    }
}

/// Renders, redacts and sends an email per event. Go's `emailsink.Sink`.
pub struct EmailSink {
    mailer: Box<dyn Mailer>,
    from: String,
    content_type: String,
    recipients: Vec<String>,
    subject: Option<Box<dyn Template>>,
    body: Option<Box<dyn Template>>,
    redactor: Option<Arc<Redactor>>,
    breaker: Option<Arc<Breaker>>,
}

impl EmailSink {
    /// A sink sending through `mailer`: [`DEFAULT_CONTENT_TYPE`], no
    /// redactor, no breaker. Go's `New`.
    pub fn new(mailer: impl Mailer + 'static) -> Self {
        EmailSink {
            mailer: Box::new(mailer),
            from: String::new(),
            content_type: DEFAULT_CONTENT_TYPE.to_string(),
            recipients: Vec::new(),
            subject: None,
            body: None,
            redactor: None,
            breaker: None,
        }
    }

    /// The `From` address of each message: Go's `WithFrom`.
    pub fn from(mut self, addr: impl Into<String>) -> Self {
        self.from = addr.into();
        self
    }

    /// Overrides the content type: Go's `WithContentType`.
    pub fn content_type(mut self, ct: impl Into<String>) -> Self {
        self.content_type = ct.into();
        self
    }

    /// The `To` list. Required at drain time: Go's `WithRecipients`.
    pub fn recipients<I, S>(mut self, addrs: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        self.recipients = addrs.into_iter().map(Into::into).collect();
        self
    }

    /// The subject template. Required at drain time.
    pub fn subject(mut self, t: impl Template + 'static) -> Self {
        self.subject = Some(Box::new(t));
        self
    }

    /// The body template. Required at drain time.
    pub fn body(mut self, t: impl Template + 'static) -> Self {
        self.body = Some(Box::new(t));
        self
    }

    /// Runs the rendered subject and body through `r`: Go's
    /// `WithRedactor`.
    pub fn redactor(mut self, r: Arc<Redactor>) -> Self {
        self.redactor = Some(r);
        self
    }

    /// Gates the mailer on `b`: Go's `WithBreaker`. An open circuit
    /// returns [`BROKEN_CIRCUIT`](super::BROKEN_CIRCUIT) as the whole
    /// message, unwrapped, and the mailer is not called.
    pub fn breaker(mut self, b: Arc<Breaker>) -> Self {
        self.breaker = Some(b);
        self
    }
}

impl Sink for EmailSink {
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        let mailer = &self.mailer;
        if self.recipients.is_empty() {
            return Err(BusError::handler("emailsink: no recipients configured"));
        }
        let (Some(subject_t), Some(body_t)) = (&self.subject, &self.body) else {
            return Err(BusError::handler(
                "emailsink: subject and body templates required",
            ));
        };
        let mut subject = subject_t
            .render(event)
            .map_err(|e| BusError::handler(format!("emailsink: render subject: {e}")))?;
        let mut body = body_t
            .render(event)
            .map_err(|e| BusError::handler(format!("emailsink: render body: {e}")))?;
        if let Some(r) = &self.redactor {
            subject = r.apply(&subject).into_owned();
            body = r.apply(&body).into_owned();
        }
        let msg = Message {
            from: self.from.clone(),
            to: self.recipients.clone(),
            content_type: self.content_type.clone(),
            subject,
            body,
        };
        let Some(b) = &self.breaker else {
            return mailer.send(ctx, &msg);
        };
        if let Err(e) = b.allow() {
            return Err(BusError::handler(e.to_string()));
        }
        let out = mailer.send(ctx, &msg);
        b.record(out.is_ok(), 0);
        out
    }

    /// A no-op: the mailer owns its resources.
    fn close(&self) -> Result<(), BusError> {
        Ok(())
    }
}
