//! A per-client rate limit built on `breaker`'s bursty limiter.

use std::collections::HashMap;
use std::net::IpAddr;
use std::sync::{Arc, Mutex};
use std::time::Duration;

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
    /// Clients tracked at once; beyond it, new clients share one
    /// bucket instead of growing memory without bound. Zero means
    /// 10,000.
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

pub(crate) struct Limiter {
    cfg: RateLimit,
    by_client: Mutex<HashMap<IpAddr, Arc<Breaker>>>,
    overflow: Arc<Breaker>,
}

impl Limiter {
    pub(crate) fn new(cfg: RateLimit) -> Self {
        let overflow = Self::bucket(&cfg, "api-overflow");
        Self {
            cfg,
            by_client: Mutex::new(HashMap::new()),
            overflow,
        }
    }

    fn bucket(cfg: &RateLimit, name: &str) -> Arc<Breaker> {
        let mut b = Breaker::builder(name)
            .max_per_interval(cfg.max, cfg.per)
            .logger(Arc::new(|_: &Record| {}));
        if let Some(c) = &cfg.clock {
            b = b.clock(c.clone());
        }
        Arc::new(b.build())
    }

    fn breaker_for(&self, ip: Option<IpAddr>) -> Arc<Breaker> {
        let Some(ip) = ip else {
            return self.overflow.clone();
        };
        let cap = if self.cfg.max_clients == 0 {
            10_000
        } else {
            self.cfg.max_clients
        };
        let mut map = self.by_client.lock().unwrap_or_else(|p| p.into_inner());
        if let Some(b) = map.get(&ip) {
            return b.clone();
        }
        if map.len() >= cap {
            return self.overflow.clone();
        }
        let b = Self::bucket(&self.cfg, "api-client");
        map.insert(ip, b.clone());
        b
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
