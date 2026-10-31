//! A small blocking SMTP client: the shipped [`Mailer`].
//!
//! Go leans on `net/smtp`; Rust's standard library has no SMTP, and a
//! mail crate would have to dial its own socket, past the network
//! policy. This speaks the few commands a notification needs (EHLO,
//! STARTTLS, AUTH PLAIN, MAIL, RCPT, DATA, QUIT) over a dial that is
//! checked against the [`NetPolicy`] first. One connection per `send`,
//! no pooling, as in Go.
//!
//! Differences from Go: `From`, recipients and the subject are refused
//! if they carry a line break (header injection), and the TCP connect
//! honours the context's deadline but a send already past the dial does
//! not poll for cancellation: its socket reads and writes time out
//! instead, at the context's remaining time or 60 seconds.

use std::fmt;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::{TcpStream, ToSocketAddrs};
use std::sync::Arc;
use std::time::Duration;

use base64::Engine as _;

use crate::bus::{BusError, Context};
use crate::netpolicy::{is_loopback, NetPolicy, OfflineError};

use super::{Mailer, Message, DEFAULT_CONTENT_TYPE};

const DEFAULT_DIAL_TIMEOUT: Duration = Duration::from_secs(30);
const IO_TIMEOUT: Duration = Duration::from_secs(60);

/// Why an SMTP send failed.
#[derive(Debug)]
pub enum SmtpError {
    /// The TCP dial failed (Go's `ErrSMTPDial`), including a refusal.
    Dial(String),
    /// The dial was refused by an offline policy. Also a dial failure:
    /// [`SmtpError::is_dial`] is true for it.
    Offline(OfflineError),
    /// A missing or unsafe address or header, found before any I/O.
    Invalid(String),
    /// The protocol exchange failed: a refused command or an I/O error.
    Protocol(String),
}

impl SmtpError {
    /// Whether the dial failed, for any reason a refusal included.
    pub fn is_dial(&self) -> bool {
        matches!(self, SmtpError::Dial(_) | SmtpError::Offline(_))
    }

    /// Whether the offline policy refused the dial.
    pub fn is_offline(&self) -> bool {
        matches!(self, SmtpError::Offline(_))
    }
}

impl fmt::Display for SmtpError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SmtpError::Dial(m) => write!(f, "emailsink: smtp dial failed: {m}"),
            SmtpError::Offline(e) => write!(f, "emailsink: smtp dial failed: {e}"),
            SmtpError::Invalid(m) | SmtpError::Protocol(m) => write!(f, "emailsink: {m}"),
        }
    }
}

impl std::error::Error for SmtpError {}

impl From<SmtpError> for BusError {
    fn from(e: SmtpError) -> Self {
        BusError::handler(e.to_string())
    }
}

/// A [`Mailer`] over SMTP. Go's `NewSMTPMailer`; dials on every send.
pub struct SmtpMailer {
    host: String,
    port: u16,
    auth: Option<(String, String)>,
    starttls: bool,
    from: String,
    tls: Option<Arc<rustls::ClientConfig>>,
    dial_timeout: Duration,
    policy: NetPolicy,
}

impl SmtpMailer {
    /// A mailer for `host:port`: no auth, no TLS, an online policy and a
    /// 30 second dial timeout.
    pub fn new(host: impl Into<String>, port: u16) -> Self {
        SmtpMailer {
            host: host.into(),
            port,
            auth: None,
            starttls: false,
            from: String::new(),
            tls: None,
            dial_timeout: DEFAULT_DIAL_TIMEOUT,
            policy: NetPolicy::online(),
        }
    }

    /// PLAIN authentication: Go's `WithSMTPAuth`. As in Go, the
    /// credentials are sent only over TLS or to a loopback host; any
    /// other send fails with `unencrypted connection`. Used only if the
    /// server advertises AUTH.
    pub fn auth(mut self, username: impl Into<String>, password: impl Into<String>) -> Self {
        self.auth = Some((username.into(), password.into()));
        self
    }

    /// Requires the STARTTLS upgrade: Go's `WithSMTPTLS`. A server
    /// that does not advertise it fails the send.
    pub fn starttls(mut self, enabled: bool) -> Self {
        self.starttls = enabled;
        self
    }

    /// The `From` used when a message has none: Go's `WithSMTPFrom`.
    pub fn from(mut self, addr: impl Into<String>) -> Self {
        self.from = addr.into();
        self
    }

    /// The TLS config for STARTTLS: Go's `WithSMTPTLSConfig`. The default
    /// is rustls on the ring provider trusting the platform's roots.
    pub fn tls_config(mut self, cfg: Arc<rustls::ClientConfig>) -> Self {
        self.tls = Some(cfg);
        self
    }

    /// The TCP connect timeout, bounded by the context's deadline: the
    /// `Timeout` of Go's `WithSMTPDialer`.
    pub fn dial_timeout(mut self, d: Duration) -> Self {
        self.dial_timeout = d;
        self
    }

    /// The network policy the dial is checked against, so `--offline`
    /// applies. Loopback stays reachable.
    pub fn policy(mut self, p: NetPolicy) -> Self {
        self.policy = p;
        self
    }

    /// [`Mailer::send`] with the typed error.
    pub fn send_message(&self, ctx: &Context, msg: &Message) -> Result<(), SmtpError> {
        let from = if msg.from.is_empty() {
            &self.from
        } else {
            &msg.from
        };
        if from.is_empty() {
            return Err(SmtpError::Invalid("empty From address".into()));
        }
        if msg.to.is_empty() {
            return Err(SmtpError::Invalid("empty To list".into()));
        }
        for v in std::iter::once(from)
            .chain(&msg.to)
            .chain([&msg.subject, &msg.content_type])
        {
            if v.contains(['\r', '\n']) {
                return Err(SmtpError::Invalid(
                    "line break in an address or header".into(),
                ));
            }
        }
        let stream = self.dial(ctx)?;
        let mut c = Client::new(Conn::Plain(stream));
        let r = self.converse(&mut c, from, msg);
        // Best effort: the outcome is the conversation's.
        drop(c);
        r.map_err(|e| SmtpError::Protocol(e.to_string()))
    }

    fn dial(&self, ctx: &Context) -> Result<TcpStream, SmtpError> {
        let addr = format!("{}:{}", self.host, self.port);
        self.policy
            .check_dial("tcp", &addr)
            .map_err(SmtpError::Offline)?;
        if let Some(e) = ctx.err() {
            return Err(SmtpError::Dial(e.to_string()));
        }
        let timeout = bound(ctx, self.dial_timeout);
        let mut last = None;
        let addrs = (self.host.as_str(), self.port)
            .to_socket_addrs()
            .map_err(|e| SmtpError::Dial(e.to_string()))?;
        for a in addrs {
            match TcpStream::connect_timeout(&a, timeout) {
                Ok(s) => {
                    let io = bound(ctx, IO_TIMEOUT);
                    s.set_read_timeout(Some(io)).ok();
                    s.set_write_timeout(Some(io)).ok();
                    return Ok(s);
                }
                Err(e) => last = Some(e),
            }
        }
        Err(SmtpError::Dial(last.map_or_else(
            || "no address".to_string(),
            |e| e.to_string(),
        )))
    }

    fn converse(&self, c: &mut Client, from: &str, msg: &Message) -> io::Result<()> {
        c.expect(220)?;
        let mut ext = c.ehlo()?;
        if self.starttls {
            if !ext.contains("STARTTLS") {
                return Err(other("server does not advertise STARTTLS"));
            }
            c.cmd("STARTTLS", 220)?;
            c.upgrade(&self.host, self.client_config()?)
                .map_err(|e| other(format!("starttls: {e}")))?;
            ext = c.ehlo()?;
        }
        if let Some((user, pass)) = &self.auth {
            if ext.contains("AUTH") {
                if !c.is_tls() && !is_loopback(&self.host) {
                    return Err(other("unencrypted connection"));
                }
                let token =
                    base64::engine::general_purpose::STANDARD.encode(format!("\0{user}\0{pass}"));
                c.cmd(&format!("AUTH PLAIN {token}"), 235)
                    .map_err(|e| other(format!("auth: {e}")))?;
            }
        }
        c.cmd(&format!("MAIL FROM:<{from}>"), 250)
            .map_err(|e| other(format!("MAIL FROM: {e}")))?;
        for to in &msg.to {
            c.cmd(&format!("RCPT TO:<{to}>"), 250)
                .map_err(|e| other(format!("RCPT TO {to:?}: {e}")))?;
        }
        c.cmd("DATA", 354)
            .map_err(|e| other(format!("DATA: {e}")))?;
        c.data(&build_rfc822(from, msg))
            .map_err(|e| other(format!("write body: {e}")))?;
        c.expect(250)
            .map_err(|e| other(format!("close body: {e}")))?;
        c.cmd("QUIT", 221)
    }

    fn client_config(&self) -> io::Result<Arc<rustls::ClientConfig>> {
        if let Some(c) = &self.tls {
            return Ok(Arc::clone(c));
        }
        let mut roots = rustls::RootCertStore::empty();
        for cert in rustls_native_certs::load_native_certs().certs {
            // A root rustls cannot parse is skipped, as the platform
            // store may hold some.
            roots.add(cert).ok();
        }
        let provider = Arc::new(rustls::crypto::ring::default_provider());
        let cfg = rustls::ClientConfig::builder_with_provider(provider)
            .with_safe_default_protocol_versions()
            .map_err(|e| other(e.to_string()))?
            .with_root_certificates(roots)
            .with_no_client_auth();
        Ok(Arc::new(cfg))
    }
}

impl Mailer for SmtpMailer {
    fn send(&self, ctx: &Context, msg: &Message) -> Result<(), BusError> {
        self.send_message(ctx, msg).map_err(BusError::from)
    }
}

/// `d`, cut to what the context has left (at least a millisecond: a
/// zero timeout is an error to the socket).
fn bound(ctx: &Context, d: Duration) -> Duration {
    let left = ctx
        .deadline()
        .map(|dl| dl.saturating_duration_since(crate::bus::context::now()));
    left.map_or(d, |l| d.min(l)).max(Duration::from_millis(1))
}

fn other(msg: impl Into<Box<dyn std::error::Error + Send + Sync>>) -> io::Error {
    io::Error::other(msg)
}

/// The minimal RFC 822 message Go builds: From, To, Subject,
/// Content-Type, a blank line, the body. Headers stay ASCII.
fn build_rfc822(from: &str, msg: &Message) -> Vec<u8> {
    let ct = if msg.content_type.is_empty() {
        DEFAULT_CONTENT_TYPE
    } else {
        &msg.content_type
    };
    format!(
        "From: {from}\r\nTo: {}\r\nSubject: {}\r\nContent-Type: {ct}\r\n\r\n{}",
        msg.to.join(", "),
        msg.subject,
        msg.body
    )
    .into_bytes()
}

enum Conn {
    Plain(TcpStream),
    Tls(Box<rustls::StreamOwned<rustls::ClientConnection, TcpStream>>),
    /// Held only while the stream is moved into the TLS wrapper.
    Moved,
}

impl Read for Conn {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        match self {
            Conn::Plain(s) => s.read(buf),
            Conn::Tls(s) => s.read(buf),
            Conn::Moved => Err(other("connection moved")),
        }
    }
}

impl Write for Conn {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        match self {
            Conn::Plain(s) => s.write(buf),
            Conn::Tls(s) => s.write(buf),
            Conn::Moved => Err(other("connection moved")),
        }
    }
    fn flush(&mut self) -> io::Result<()> {
        match self {
            Conn::Plain(s) => s.flush(),
            Conn::Tls(s) => s.flush(),
            Conn::Moved => Ok(()),
        }
    }
}

struct Client {
    conn: BufReader<Conn>,
}

/// The extensions an EHLO reply advertised, keyword upper-cased.
struct Extensions(Vec<String>);

impl Extensions {
    fn contains(&self, k: &str) -> bool {
        self.0.iter().any(|e| e == k)
    }
}

impl Client {
    fn new(c: Conn) -> Self {
        Client {
            conn: BufReader::new(c),
        }
    }

    fn is_tls(&self) -> bool {
        matches!(self.conn.get_ref(), Conn::Tls(_))
    }

    /// Reads one reply, joining continuation lines (`250-...`); returns
    /// its code and the text lines.
    fn reply(&mut self) -> io::Result<(u16, Vec<String>)> {
        let mut lines = Vec::new();
        loop {
            let mut line = String::new();
            if self.conn.read_line(&mut line)? == 0 {
                return Err(io::ErrorKind::UnexpectedEof.into());
            }
            let line = line.trim_end_matches(['\r', '\n']);
            let code = line
                .get(..3)
                .and_then(|c| c.parse::<u16>().ok())
                .ok_or_else(|| other(format!("malformed reply {line:?}")))?;
            lines.push(line.get(4..).unwrap_or("").to_string());
            if line.as_bytes().get(3) != Some(&b'-') {
                return Ok((code, lines));
            }
        }
    }

    fn expect(&mut self, want: u16) -> io::Result<Vec<String>> {
        let (code, lines) = self.reply()?;
        if code == want {
            Ok(lines)
        } else {
            Err(other(format!("{code} {}", lines.join(" "))))
        }
    }

    fn cmd(&mut self, line: &str, want: u16) -> io::Result<()> {
        let w = self.conn.get_mut();
        w.write_all(line.as_bytes())?;
        w.write_all(b"\r\n")?;
        w.flush()?;
        // A secret never reaches an error: AUTH replies carry none.
        self.expect(want).map(drop)
    }

    fn ehlo(&mut self) -> io::Result<Extensions> {
        let w = self.conn.get_mut();
        w.write_all(b"EHLO localhost\r\n")?;
        w.flush()?;
        let lines = self.expect(250)?;
        Ok(Extensions(
            lines
                .iter()
                .skip(1)
                .filter_map(|l| l.split_whitespace().next())
                .map(str::to_ascii_uppercase)
                .collect(),
        ))
    }

    /// Writes the message body as the DATA payload: lone `\n` becomes
    /// CRLF, a leading `.` on a line is doubled, and `.` ends it.
    fn data(&mut self, body: &[u8]) -> io::Result<()> {
        let mut out = Vec::with_capacity(body.len() + 8);
        let mut at_line_start = true;
        let mut prev = 0u8;
        for &b in body {
            if b == b'\n' && prev != b'\r' {
                out.push(b'\r');
            }
            if at_line_start && b == b'.' {
                out.push(b'.');
            }
            out.push(b);
            at_line_start = b == b'\n';
            prev = b;
        }
        if !out.ends_with(b"\r\n") {
            out.extend_from_slice(b"\r\n");
        }
        out.extend_from_slice(b".\r\n");
        let w = self.conn.get_mut();
        w.write_all(&out)?;
        w.flush()
    }

    fn upgrade(&mut self, host: &str, cfg: Arc<rustls::ClientConfig>) -> io::Result<()> {
        if !self.conn.buffer().is_empty() {
            return Err(other("server sent data before the TLS handshake"));
        }
        let name = rustls::pki_types::ServerName::try_from(host.to_string())
            .map_err(|e| other(e.to_string()))?;
        let tls = rustls::ClientConnection::new(cfg, name).map_err(|e| other(e.to_string()))?;
        let plain = std::mem::replace(self.conn.get_mut(), Conn::Moved);
        let Conn::Plain(tcp) = plain else {
            return Err(other("already encrypted"));
        };
        *self.conn.get_mut() = Conn::Tls(Box::new(rustls::StreamOwned::new(tls, tcp)));
        Ok(())
    }
}
