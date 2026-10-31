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
    /// Clients tracked at once; at the cap, idle buckets go first, then
    /// the least recently seen, so a new client always has its own
    /// budget. IPv6 clients are keyed by /64. Zero means 10,000.
    pub max_clients: usize,
    /// The clock, for tests. Default the system clock.
    pub clock: Option<Arc<dyn Clock>>,
}

impl RateLimit {
    /// `max` requests per `per`, per client.
    pub fn new(max: u32, per: Duration) -> Self {
        Self {
            max,
            per,
            max_clients: 0,
            clock: None,
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

impl Key {
    fn of(ip: IpAddr) -> Key {
        match ip {
            IpAddr::V4(a) => Key::V4(u32::from(a)),
            IpAddr::V6(a) => match a.to_ipv4_mapped() {
                Some(v4) => Key::V4(u32::from(v4)),
                None => Key::V6((u128::from(a) >> 64) as u64),
            },
        }
    }
}

struct Entry {
    breaker: Arc<Breaker>,
    last_seen: SystemTime,
}

pub(crate) struct Limiter {
    cfg: RateLimit,
    by_client: Mutex<HashMap<Key, Entry>>,
    unknown: Arc<Breaker>,
}

impl Limiter {
    pub(crate) fn new(cfg: RateLimit) -> Self {
        let unknown = Self::bucket(&cfg);
        Self {
            cfg,
            by_client: Mutex::new(HashMap::new()),
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

    fn breaker_for(&self, ip: Option<IpAddr>) -> Arc<Breaker> {
        let Some(ip) = ip else {
            return self.unknown.clone();
        };
        let key = Key::of(ip);
        let cap = if self.cfg.max_clients == 0 {
            10_000
        } else {
            self.cfg.max_clients
        };
        let now = self.now();
        let mut map = self.by_client.lock().unwrap_or_else(|p| p.into_inner());
        if let Some(e) = map.get_mut(&key) {
            e.last_seen = now;
            return e.breaker.clone();
        }
        if map.len() >= cap {
            // A bucket idle for a full period has refilled: dropping it
            // loses nothing. If none is, drop the least recently seen,
            // so a new client always gets its own budget.
            map.retain(|_, e| now.duration_since(e.last_seen).unwrap_or_default() < self.cfg.per);
            if map.len() >= cap {
                if let Some(oldest) = map.iter().min_by_key(|(_, e)| e.last_seen).map(|(k, _)| *k) {
                    map.remove(&oldest);
                }
            }
        }
        let breaker = Self::bucket(&self.cfg);
        map.insert(
            key,
            Entry {
                breaker: breaker.clone(),
                last_seen: now,
            },
        );
        breaker
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
