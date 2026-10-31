//! A scripted loopback SMTP server: records the commands and the DATA
//! payload of each session.

#![allow(dead_code)]

use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex, PoisonError};

#[derive(Clone, Default)]
pub struct Config {
    /// Advertise STARTTLS in the EHLO reply.
    pub starttls: bool,
    /// Advertise AUTH PLAIN.
    pub auth: bool,
    /// Refuse RCPT TO for this address with a 550.
    pub reject_rcpt: Option<String>,
}

#[derive(Clone, Debug, Default)]
pub struct Session {
    pub commands: Vec<String>,
    pub data: String,
}

pub struct Server {
    pub port: u16,
    sessions: Arc<Mutex<Vec<Session>>>,
}

impl Server {
    pub fn start(cfg: Config) -> Server {
        let ln = TcpListener::bind("127.0.0.1:0").expect("bind");
        let port = ln.local_addr().unwrap().port();
        let sessions = Arc::new(Mutex::new(Vec::new()));
        let rec = Arc::clone(&sessions);
        std::thread::spawn(move || {
            for conn in ln.incoming() {
                let Ok(conn) = conn else { return };
                let (cfg, rec) = (cfg.clone(), Arc::clone(&rec));
                std::thread::spawn(move || serve(conn, &cfg, &rec));
            }
        });
        Server { port, sessions }
    }

    pub fn sessions(&self) -> Vec<Session> {
        self.sessions
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    /// Waits briefly for `n` completed sessions.
    pub fn wait_sessions(&self, n: usize) -> Vec<Session> {
        for _ in 0..200 {
            let s = self.sessions();
            if s.len() >= n {
                return s;
            }
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        self.sessions()
    }
}

fn serve(conn: TcpStream, cfg: &Config, rec: &Mutex<Vec<Session>>) {
    let mut w = conn.try_clone().unwrap();
    let mut r = BufReader::new(conn);
    let mut s = Session::default();
    let say = |w: &mut TcpStream, t: &str| {
        w.write_all(t.as_bytes()).ok();
    };
    say(&mut w, "220 fake ESMTP\r\n");
    let mut line = String::new();
    loop {
        line.clear();
        if r.read_line(&mut line).unwrap_or(0) == 0 {
            break;
        }
        let cmd = line.trim_end().to_string();
        s.commands.push(cmd.clone());
        let up = cmd.to_ascii_uppercase();
        if up.starts_with("EHLO") {
            let mut reply = String::from("250-fake\r\n");
            if cfg.starttls {
                reply.push_str("250-STARTTLS\r\n");
            }
            if cfg.auth {
                reply.push_str("250-AUTH PLAIN LOGIN\r\n");
            }
            reply.push_str("250 8BITMIME\r\n");
            say(&mut w, &reply);
        } else if up.starts_with("AUTH") {
            say(&mut w, "235 ok\r\n");
        } else if up.starts_with("MAIL") {
            say(&mut w, "250 ok\r\n");
        } else if up.starts_with("RCPT") {
            match &cfg.reject_rcpt {
                Some(a) if cmd.contains(a.as_str()) => say(&mut w, "550 no such user\r\n"),
                _ => say(&mut w, "250 ok\r\n"),
            }
        } else if up == "DATA" {
            say(&mut w, "354 go\r\n");
            let mut data = String::new();
            loop {
                line.clear();
                if r.read_line(&mut line).unwrap_or(0) == 0 {
                    break;
                }
                if line == ".\r\n" {
                    break;
                }
                data.push_str(&line);
            }
            s.data = data;
            say(&mut w, "250 queued\r\n");
        } else if up == "QUIT" {
            say(&mut w, "221 bye\r\n");
            break;
        } else {
            say(&mut w, "500 what\r\n");
        }
    }
    rec.lock().unwrap_or_else(PoisonError::into_inner).push(s);
}
