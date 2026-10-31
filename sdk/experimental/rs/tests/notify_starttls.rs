//! The SMTP mailer's STARTTLS path against a loopback server that
//! upgrades with rustls. The certificate is generated at run time with
//! rcgen; no key material is committed.

#![cfg(feature = "notify-email")]

use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use hop_top_kit::bus::Context;
use hop_top_kit::notify::email::{Message, SmtpMailer};
use rustls::pki_types::{CertificateDer, PrivatePkcs8KeyDer};

/// One command the server read, and whether it arrived encrypted.
type Seen = (bool, String);

struct Tls {
    port: u16,
    seen: Arc<Mutex<Vec<Seen>>>,
    data: Arc<Mutex<Vec<String>>>,
}

fn provider() -> Arc<rustls::crypto::CryptoProvider> {
    Arc::new(rustls::crypto::ring::default_provider())
}

/// A fresh self-signed certificate for `localhost`.
fn make_cert() -> (CertificateDer<'static>, PrivatePkcs8KeyDer<'static>) {
    let ck = rcgen::generate_simple_self_signed(vec!["localhost".to_string()]).unwrap();
    (
        ck.cert.der().clone(),
        PrivatePkcs8KeyDer::from(ck.signing_key.serialize_der()),
    )
}

fn client_config(trust: Option<&CertificateDer<'static>>) -> Arc<rustls::ClientConfig> {
    let mut roots = rustls::RootCertStore::empty();
    if let Some(c) = trust {
        roots.add(c.clone()).unwrap();
    }
    Arc::new(
        rustls::ClientConfig::builder_with_provider(provider())
            .with_safe_default_protocol_versions()
            .unwrap()
            .with_root_certificates(roots)
            .with_no_client_auth(),
    )
}

impl Tls {
    fn start(cert: CertificateDer<'static>, key: PrivatePkcs8KeyDer<'static>) -> Tls {
        let server = Arc::new(
            rustls::ServerConfig::builder_with_provider(provider())
                .with_safe_default_protocol_versions()
                .unwrap()
                .with_no_client_auth()
                .with_single_cert(vec![cert], key.into())
                .unwrap(),
        );
        let ln = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = ln.local_addr().unwrap().port();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let data = Arc::new(Mutex::new(Vec::new()));
        let (s, d) = (Arc::clone(&seen), Arc::clone(&data));
        std::thread::spawn(move || {
            for conn in ln.incoming() {
                let Ok(conn) = conn else { return };
                let (server, s, d) = (Arc::clone(&server), Arc::clone(&s), Arc::clone(&d));
                std::thread::spawn(move || session(conn, &server, &s, &d));
            }
        });
        Tls { port, seen, data }
    }

    fn seen(&self) -> Vec<Seen> {
        self.seen
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }
}

/// Runs SMTP commands over `r` until it ends or a STARTTLS is accepted;
/// true means the caller must upgrade the connection.
fn commands<S: Read + Write>(
    r: &mut BufReader<S>,
    tls: bool,
    seen: &Mutex<Vec<Seen>>,
    data: &Mutex<Vec<String>>,
) -> bool {
    let say = |r: &mut BufReader<S>, t: &str| {
        r.get_mut().write_all(t.as_bytes()).ok();
        r.get_mut().flush().ok();
    };
    let mut line = String::new();
    loop {
        line.clear();
        if r.read_line(&mut line).unwrap_or(0) == 0 {
            return false;
        }
        let cmd = line.trim_end().to_string();
        seen.lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push((tls, cmd.clone()));
        let up = cmd.to_ascii_uppercase();
        if up.starts_with("EHLO") {
            let mut reply = String::from("250-fake\r\n");
            if !tls {
                reply.push_str("250-STARTTLS\r\n");
            }
            reply.push_str("250-AUTH PLAIN\r\n250 8BITMIME\r\n");
            say(r, &reply);
        } else if up == "STARTTLS" {
            say(r, "220 ready\r\n");
            return true;
        } else if up.starts_with("AUTH") {
            say(r, "235 ok\r\n");
        } else if up.starts_with("MAIL") || up.starts_with("RCPT") {
            say(r, "250 ok\r\n");
        } else if up == "DATA" {
            say(r, "354 go\r\n");
            let mut body = String::new();
            loop {
                line.clear();
                if r.read_line(&mut line).unwrap_or(0) == 0 || line == ".\r\n" {
                    break;
                }
                body.push_str(&line);
            }
            data.lock()
                .unwrap_or_else(PoisonError::into_inner)
                .push(body);
            say(r, "250 queued\r\n");
        } else if up == "QUIT" {
            say(r, "221 bye\r\n");
            return false;
        } else {
            say(r, "500 what\r\n");
        }
    }
}

fn session(
    conn: TcpStream,
    cfg: &Arc<rustls::ServerConfig>,
    seen: &Mutex<Vec<Seen>>,
    data: &Mutex<Vec<String>>,
) {
    conn.set_read_timeout(Some(Duration::from_secs(10))).ok();
    let mut w = conn.try_clone().unwrap();
    w.write_all(b"220 fake ESMTP\r\n").ok();
    let mut plain = BufReader::new(conn);
    if !commands(&mut plain, false, seen, data) {
        return;
    }
    // The client waits for the reply before its ClientHello, so nothing
    // is buffered; hand the socket to rustls.
    let tcp = plain.into_inner();
    let Ok(sc) = rustls::ServerConnection::new(Arc::clone(cfg)) else {
        return;
    };
    let mut enc = BufReader::new(rustls::StreamOwned::new(sc, tcp));
    commands(&mut enc, true, seen, data);
}

fn msg() -> Message {
    Message {
        from: "a@b.io".into(),
        to: vec!["c@d.io".into()],
        content_type: String::new(),
        subject: "tls".into(),
        body: "secret body\n.dot".into(),
    }
}

fn mailer(port: u16, cfg: Arc<rustls::ClientConfig>) -> SmtpMailer {
    SmtpMailer::new("localhost", port)
        .dial_timeout(Duration::from_secs(5))
        .starttls(true)
        .auth("user", "pass")
        .tls_config(cfg)
}

#[test]
fn starttls_upgrades_before_auth_and_the_message_arrives_intact() {
    let (cert, key) = make_cert();
    let srv = Tls::start(cert.clone(), key);
    mailer(srv.port, client_config(Some(&cert)))
        .send_message(&Context::background(), &msg())
        .unwrap();

    let seen = srv.seen();
    let cmds: Vec<(bool, &str)> = seen.iter().map(|(t, c)| (*t, c.as_str())).collect();
    assert_eq!(
        cmds,
        [
            (false, "EHLO localhost"),
            (false, "STARTTLS"),
            (true, "EHLO localhost"),
            (true, "AUTH PLAIN AHVzZXIAcGFzcw=="),
            (true, "MAIL FROM:<a@b.io>"),
            (true, "RCPT TO:<c@d.io>"),
            (true, "DATA"),
            (true, "QUIT"),
        ]
    );
    assert!(
        seen.iter()
            .filter(|(_, c)| c.starts_with("AUTH"))
            .all(|(tls, _)| *tls),
        "AUTH crossed the wire in clear text"
    );
    let data = srv.data.lock().unwrap().clone();
    assert_eq!(
        data,
        ["From: a@b.io\r\nTo: c@d.io\r\nSubject: tls\r\n\
          Content-Type: text/plain; charset=utf-8\r\n\r\n\
          secret body\r\n..dot\r\n"]
    );
}

#[test]
fn an_untrusted_certificate_fails_the_handshake_and_sends_no_auth() {
    let (cert, key) = make_cert();
    let srv = Tls::start(cert, key);
    // The client trusts a different, unrelated certificate.
    let (other, _) = make_cert();
    let err = mailer(srv.port, client_config(Some(&other)))
        .send_message(&Context::background(), &msg())
        .unwrap_err()
        .to_string();
    assert!(err.contains("starttls"), "{err}");

    // Give the server's session time to see the aborted handshake.
    std::thread::sleep(Duration::from_millis(200));
    let seen = srv.seen();
    assert!(
        !seen
            .iter()
            .any(|(_, c)| c.starts_with("AUTH") || c.starts_with("MAIL")),
        "{seen:?}"
    );
    assert!(srv.data.lock().unwrap().is_empty());
}

#[test]
fn no_trusted_roots_at_all_fails_the_handshake() {
    let (cert, key) = make_cert();
    let srv = Tls::start(cert, key);
    let err = mailer(srv.port, client_config(None))
        .send_message(&Context::background(), &msg())
        .unwrap_err()
        .to_string();
    assert!(err.contains("starttls"), "{err}");
    assert!(!srv.seen().iter().any(|(_, c)| c.starts_with("AUTH")));
}
