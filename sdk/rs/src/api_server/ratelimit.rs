//! A per-client rate limit built on `breaker`'s bursty limiter.

use std::collections::HashMap;
use std::net::IpAddr;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use axum::extract::{Request, State};
use axum::http::{header, HeaderValue};
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::ClientIp;
use super::error::{ApiError, CODE_RATE_LIMITED};
use crate::breaker::{Breaker, Clock, Record};

/// The limit: `max` requests per `per`, per client address.
#[derive(Clone)]
pub struct RateLimit {
    /// Requests admitted per period, per client.
    pub max: u32,
    /// The period.
    pub per: Duration,
    /// Clients tracked individually (IPv6 by /64). At the cap, buckets
    /// idle for a full period are reclaimed (at most once per quarter
    /// period); a live bucket is never evicted. Newcomers beyond that
    /// share an aggregate bucket per /24 (IPv4) or /48 (IPv6), at most
    /// cap/4 of them, then one shared overflow bucket. Zero means 10,000.
    pub max_clients: usize,
    /// The clock, for tests. Default the system clock.
    pub clock: Option<Arc<dyn Clock>>,
    /// Individually tracked clients allowed per /24 (IPv4) or /48 (IPv6);
    /// past it a prefix's newcomers use its aggregate bucket, so one
    /// prefix cannot take every slot. Zero means 16.
    pub per_prefix: usize,
    /// Called each time the idle sweep runs; a test hook.
    pub on_sweep: Option<Arc<dyn Fn() + Send + Sync>>,
}

impl RateLimit {
    /// `max` requests per `per`, per client.
    pub fn new(max: u32, per: Duration) -> Self {
        Self {
            max,
            per,
            max_clients: 0,
            clock: None,
            per_prefix: 0,
            on_sweep: None,
        }
    }
}

/// A client bucket key: IPv4 by address, IPv6 by its /64, so rotating
/// through a delegated prefix does not mint fresh budgets.
#[derive(Clone, Copy, PartialEq, Eq, Hash)]
enum Key {
    V4(u32),
    V6(u64),
}

/// The coarser key the aggregate buckets use: IPv4 /24, IPv6 /48.
#[derive(Clone, Copy, PartialEq, Eq, Hash)]
enum Coarse {
    V4(u32),
    V6(u64),
}

impl Key {
    fn coarse(self) -> Coarse {
        match self {
            Key::V4(v) => Coarse::V4(v >> 8),
            Key::V6(v) => Coarse::V6(v >> 16),
        }
    }
}

fn parts(ip: IpAddr) -> (Key, Coarse) {
    match ip {
        IpAddr::V4(a) => {
            let v = u32::from(a);
            (Key::V4(v), Coarse::V4(v >> 8))
        }
        IpAddr::V6(a) => match a.to_ipv4_mapped() {
            Some(v4) => parts(IpAddr::V4(v4)),
            None => {
                let v = u128::from(a);
                (Key::V6((v >> 64) as u64), Coarse::V6((v >> 80) as u64))
            }
        },
    }
}

struct Entry {
    breaker: Arc<Breaker>,
    last_seen: SystemTime,
}

struct Table {
    exact: HashMap<Key, Entry>,
    per_prefix: HashMap<Coarse, usize>,
    coarse: HashMap<Coarse, Entry>,
    last_sweep: Option<SystemTime>,
}

pub(crate) struct Limiter {
    cfg: RateLimit,
    state: Mutex<Table>,
    overflow: Arc<Breaker>,
    unknown: Arc<Breaker>,
}

impl Limiter {
    pub(crate) fn new(cfg: RateLimit) -> Self {
        let overflow = Self::bucket(&cfg);
        let unknown = Self::bucket(&cfg);
        Self {
            cfg,
            state: Mutex::new(Table {
                exact: HashMap::new(),
                per_prefix: HashMap::new(),
                coarse: HashMap::new(),
                last_sweep: None,
            }),
            overflow,
            unknown,
        }
    }

    fn bucket(cfg: &RateLimit) -> Arc<Breaker> {
        let mut b = Breaker::builder("api-client")
            .max_per_interval(cfg.max, cfg.per)
            .logger(Arc::new(|_: &Record| {}));
        if let Some(c) = &cfg.clock {
            b = b.clock(c.clone());
        }
        Arc::new(b.build())
    }

    fn now(&self) -> SystemTime {
        match &self.cfg.clock {
            Some(c) => c.now(),
            None => SystemTime::now(),
        }
    }

    fn idle(&self, now: SystemTime, e: &Entry) -> bool {
        now.duration_since(e.last_seen).unwrap_or_default() >= self.cfg.per
    }

    /// Drops buckets idle for a full period: they have refilled, so
    /// dropping one is the same as keeping it. At most once per quarter
    /// period, so a flood of new keys cannot make every insert a scan.
    fn sweep(&self, st: &mut Table, now: SystemTime) {
        let every = self.cfg.per / 4;
        if st
            .last_sweep
            .is_some_and(|t| now.duration_since(t).unwrap_or_default() < every)
        {
            return;
        }
        st.last_sweep = Some(now);
        st.exact.retain(|_, e| !self.idle(now, e));
        st.per_prefix.clear();
        for k in st.exact.keys() {
            *st.per_prefix.entry(k.coarse()).or_insert(0) += 1;
        }
        st.coarse.retain(|_, e| !self.idle(now, e));
        if let Some(f) = &self.cfg.on_sweep {
            f();
        }
    }

    fn breaker_for(&self, ip: Option<IpAddr>) -> Arc<Breaker> {
        let Some(ip) = ip else {
            return self.unknown.clone();
        };
        let (key, coarse) = parts(ip);
        let cap = if self.cfg.max_clients == 0 {
            10_000
        } else {
            self.cfg.max_clients
        };
        let coarse_cap = (cap / 4).max(2);
        let quota = if self.cfg.per_prefix == 0 {
            16
        } else {
            self.cfg.per_prefix
        };
        let now = self.now();
        let mut st = self.state.lock().unwrap_or_else(|p| p.into_inner());
        self.sweep(&mut st, now);
        if let Some(e) = st.exact.get_mut(&key) {
            e.last_seen = now;
            return e.breaker.clone();
        }
        let fresh = |b: Arc<Breaker>| Entry {
            breaker: b,
            last_seen: now,
        };
        if st.exact.len() < cap && st.per_prefix.get(&coarse).copied().unwrap_or(0) < quota {
            let b = Self::bucket(&self.cfg);
            st.exact.insert(key, fresh(b.clone()));
            *st.per_prefix.entry(coarse).or_insert(0) += 1;
            return b;
        }
        // At the cap with nothing idle to reclaim. Evicting a live
        // bucket would hand its owner a fresh budget, so the newcomer
        // shares its /24 (/48) with the others that arrived under
        // pressure, and past that, one last bucket.
        if let Some(e) = st.coarse.get_mut(&coarse) {
            e.last_seen = now;
            return e.breaker.clone();
        }
        if st.coarse.len() < coarse_cap {
            let b = Self::bucket(&self.cfg);
            st.coarse.insert(coarse, fresh(b.clone()));
            return b;
        }
        self.overflow.clone()
    }

    /// Admits one request from `ip`, or the seconds to wait.
    pub(crate) fn admit(&self, ip: Option<IpAddr>) -> Result<(), u64> {
        let b = self.breaker_for(ip);
        match b.allow() {
            Ok(()) => {
                b.record(true, 0);
                Ok(())
            }
            Err(_) => Err(self.cfg.per.as_secs_f64().ceil().max(1.0) as u64),
        }
    }
}

pub(crate) async fn rate_limit(
    State(l): State<Arc<Limiter>>,
    req: Request,
    next: Next,
) -> Response {
    let ip = req.extensions().get::<ClientIp>().map(|c| c.0);
    match l.admit(ip) {
        Ok(()) => next.run(req).await,
        Err(secs) => {
            let mut resp =
                ApiError::new(429, CODE_RATE_LIMITED, "rate limit exceeded").into_response();
            if let Ok(v) = HeaderValue::from_str(&secs.to_string()) {
                resp.headers_mut().insert(header::RETRY_AFTER, v);
            }
            resp
        }
    }
}
