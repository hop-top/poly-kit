use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use axum::extract::Request;
use axum::http::{header, HeaderName, HeaderValue, StatusCode};
use axum::middleware::{from_fn, from_fn_with_state};
use axum::response::Response;
use axum::routing::get;
use axum::Router;
use tower_http::catch_panic::CatchPanicLayer;
use tower_http::compression::predicate::{Predicate, SizeAbove};
use tower_http::compression::CompressionLayer;
use tower_http::limit::RequestBodyLimitLayer;
use tower_http::request_id::{
    MakeRequestId, PropagateRequestIdLayer, RequestId, SetRequestIdLayer,
};
use tower_http::sensitive_headers::SetSensitiveRequestHeadersLayer;

use super::accesslog::{access_log, panic_line, stderr_sink, LogSink};
use super::auth::{auth, AuthState, CertAuth, OnAuthRefused};
use super::clientaddr::{client_address, parse_trusted_proxies};
use super::cors::CorsConfig;
use super::error::{json_response, ApiError, ApiServerError, RefusalWriter, CODE_BODY_TOO_LARGE};
use super::events::{events, EventPublisher, EventState, Topics};
use super::headers::{hsts, hsts_value, static_layers, SecurityHeadersConfig};
use super::health::{health, HealthState, ReadinessCheck};
use super::hosts::{host_check, listener_hosts, HostAllowlist, HostCheckConfig};
use super::metrics::{metrics, MetricsConfig, MetricsState};
use super::openapi::{OpenApi, OPENAPI_PATH};
use super::origin::{origin_check, OriginCheckConfig, OriginPolicy};
use super::ratelimit::{rate_limit, Limiter, RateLimit};
use super::serve_loop::{serve, Acceptor};
use super::timeouts::ServerTimeouts;
use crate::authn::AuthFunc;
use crate::serve::{CancelToken, ReadySignal, ServeFuture, Service};

/// The default body cap: 1 MiB.
pub const DEFAULT_MAX_BODY_BYTES: i64 = 1 << 20;
/// The service name the server registers under.
pub const SERVICE_NAME: &str = "api";
/// Smallest response gzip is applied to.
pub const COMPRESS_MIN_BYTES: u16 = 1024;

/// The server's configuration. Every default is the safe one: Host and
/// Origin checks and security headers on, body capped, loopback bind.
pub struct ApiServerConfig {
    /// The listen address (`127.0.0.1:8080`).
    pub addr: String,
    /// The adopter's routes.
    pub router: Router,
    /// Authentication; `None` serves unauthenticated.
    pub auth: Option<AuthFunc>,
    /// The `WWW-Authenticate` challenge of a 401; default `Bearer`.
    pub auth_challenge: Option<String>,
    /// Called with every authentication refusal.
    pub on_auth_refused: Option<OnAuthRefused>,
    /// Serve beyond loopback without authentication. Refused otherwise.
    pub insecure_remote: bool,
    /// Host check on (default true).
    pub host_check: bool,
    /// Hosts allowed beyond the listener's own.
    pub allow_hosts: Vec<String>,
    /// Origin check on (default true).
    pub origin_check: bool,
    /// Origins trusted beyond same-origin; `*` turns the check off.
    pub allow_origins: Vec<String>,
    /// Security headers on (default true).
    pub security_headers: bool,
    /// Security header values.
    pub security_headers_config: SecurityHeadersConfig,
    /// CORS; `None` or no origins leaves it off.
    pub cors: Option<CorsConfig>,
    /// Trusted proxies (CIDRs or addresses) whose forwarding headers
    /// are believed.
    pub trusted_proxies: Vec<String>,
    /// Body cap in bytes: 0 is [`DEFAULT_MAX_BODY_BYTES`], negative is
    /// none.
    pub max_body_bytes: i64,
    /// gzip responses of 1 KiB and up (default true).
    pub compress: bool,
    /// Per-client rate limit; `None` is none.
    pub rate_limit: Option<RateLimit>,
    /// Where request start/end events go.
    pub publisher: Option<Arc<dyn EventPublisher>>,
    /// The request event topics.
    pub topics: Topics,
    /// Access log sink; default stderr.
    pub log: Option<LogSink>,
    /// Further readiness dependencies for `/readyz`.
    pub readiness: Vec<ReadinessCheck>,
    /// Name the failing checks in a 503 `/readyz`. Default: only on a
    /// loopback bind.
    pub health_detail: Option<bool>,
    /// Serve this document at `/openapi.json`.
    pub openapi: Option<OpenApi>,
    /// Answers host and origin refusals in the transport's envelope.
    pub refuse: Option<RefusalWriter>,
    /// Services whose readiness `/readyz` includes, by name.
    pub depends_on: Vec<String>,
    /// Read, header and write timeouts; default
    /// [`ServerTimeouts::default`] (5s, 5s, 10s). Streams are exempt
    /// from the write deadline, see [`LiftWriteDeadline`](super::LiftWriteDeadline).
    pub timeouts: ServerTimeouts,
    /// The metrics scrape endpoint; `None` serves none.
    pub metrics: Option<MetricsConfig>,
    /// Serve TLS (and, with a client CA, mTLS). `None` serves plaintext.
    #[cfg(feature = "api-server-tls")]
    pub tls: Option<super::tls::TlsConfig>,
    /// Idempotency keys: replay a recorded response for a repeated
    /// `Idempotency-Key`. `None` leaves the header alone.
    #[cfg(feature = "api-server-idempotency")]
    pub idempotency: Option<super::idempotency::IdempotencyConfig>,
}

impl ApiServerConfig {
    /// A configuration serving `router` at `addr`.
    pub fn new(addr: impl Into<String>, router: Router) -> Self {
        Self {
            addr: addr.into(),
            router,
            auth: None,
            auth_challenge: None,
            on_auth_refused: None,
            insecure_remote: false,
            host_check: true,
            allow_hosts: Vec::new(),
            origin_check: true,
            allow_origins: Vec::new(),
            security_headers: true,
            security_headers_config: SecurityHeadersConfig::default(),
            cors: None,
            trusted_proxies: Vec::new(),
            max_body_bytes: 0,
            compress: true,
            rate_limit: None,
            publisher: None,
            topics: Topics::default(),
            log: None,
            readiness: Vec::new(),
            health_detail: None,
            openapi: None,
            refuse: None,
            depends_on: Vec::new(),
            timeouts: ServerTimeouts::default(),
            metrics: None,
            #[cfg(feature = "api-server-tls")]
            tls: None,
            #[cfg(feature = "api-server-idempotency")]
            idempotency: None,
        }
    }
}

fn is_loopback_addr(addr: &str) -> bool {
    let host = addr.rsplit_once(':').map_or(addr, |(h, _)| h);
    let host = host.trim_start_matches('[').trim_end_matches(']');
    host == "localhost"
        || host
            .parse::<std::net::IpAddr>()
            .is_ok_and(|ip| ip.is_loopback())
}

/// 1 to 128 of `[A-Za-z0-9._-]`.
fn valid_request_id(v: &[u8]) -> bool {
    (1..=128).contains(&v.len())
        && v.iter()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
}

#[derive(Clone)]
struct RandomIds;

impl MakeRequestId for RandomIds {
    fn make_request_id<B>(&mut self, _: &axum::http::Request<B>) -> Option<RequestId> {
        let mut b = [0u8; 16];
        let id = if getrandom::fill(&mut b).is_ok() {
            b.iter().map(|x| format!("{x:02x}")).collect::<String>()
        } else {
            return None;
        };
        HeaderValue::from_str(&id).ok().map(RequestId::new)
    }
}

/// Compress what is text-like and not an event stream.
#[derive(Clone, Copy)]
struct Compressible;

impl Predicate for Compressible {
    fn should_compress<B>(&self, resp: &axum::http::Response<B>) -> bool
    where
        B: axum::body::HttpBody,
    {
        let ct = resp
            .headers()
            .get(header::CONTENT_TYPE)
            .and_then(|v| v.to_str().ok())
            .unwrap_or("");
        let mt = ct.split(';').next().unwrap_or("").trim().to_lowercase();
        if mt.is_empty() || mt == "text/event-stream" {
            return false;
        }
        mt.starts_with("text/")
            || matches!(
                mt.as_str(),
                "application/json"
                    | "application/yaml"
                    | "application/x-yaml"
                    | "application/xml"
                    | "application/javascript"
            )
            || mt.ends_with("+json")
            || mt.ends_with("+yaml")
            || mt.ends_with("+xml")
    }
}

/// Whether the request should skip compression: streams, upgrades and
/// the Connect and gRPC protocols compress their own frames.
fn skip_compression(req: &Request) -> bool {
    let h = req.headers();
    let accept = h.get_all(header::ACCEPT).iter().any(|v| {
        v.to_str()
            .is_ok_and(|s| s.to_lowercase().contains("text/event-stream"))
    });
    let ct = h
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_lowercase();
    req.method() == axum::http::Method::CONNECT
        || h.contains_key(header::UPGRADE)
        || accept
        || h.contains_key("connect-protocol-version")
        || ct.starts_with("application/grpc")
        || ct.starts_with("application/connect+")
}

async fn body_limit(
    axum::extract::State(limit): axum::extract::State<i64>,
    req: Request,
    next: axum::middleware::Next,
) -> Response {
    let declared = req
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.parse::<i64>().ok());
    if declared.is_some_and(|n| n > limit) {
        return ApiError::new(
            413,
            CODE_BODY_TOO_LARGE,
            format!("request body exceeds {limit} bytes"),
        )
        .into_response();
    }
    next.run(req).await
}

/// The listener, as a `serve::Service`.
pub struct ApiService {
    addr: String,
    app: Router,
    up: Arc<AtomicBool>,
    bound: Arc<Mutex<Option<String>>>,
    stop: CancelToken,
    done: CancelToken,
    depends_on: Vec<String>,
    validation: Option<String>,
    timeouts: ServerTimeouts,
    acceptor: Option<Acceptor>,
}

impl ApiService {
    /// Builds the layered server. Fails on an invalid trusted proxy,
    /// allowed origin or CORS configuration.
    pub fn build(cfg: ApiServerConfig) -> Result<Self, ApiServerError> {
        let up = Arc::new(AtomicBool::new(false));
        let (acceptor, cert_auth) = Self::tls(&cfg)?;
        let validation = Self::validate_exposure(&cfg, cert_auth.is_some());
        let app = Self::layered(&cfg, up.clone(), cert_auth)?;
        Ok(Self {
            addr: cfg.addr,
            app,
            up,
            bound: Arc::new(Mutex::new(None)),
            stop: CancelToken::new(),
            done: CancelToken::new(),
            depends_on: cfg.depends_on,
            validation,
            timeouts: cfg.timeouts,
            acceptor,
        })
    }

    /// The TLS acceptor, and the mTLS identity reader when the setting
    /// asks for client certificates.
    #[allow(clippy::type_complexity)]
    fn tls(cfg: &ApiServerConfig) -> Result<(Option<Acceptor>, Option<CertAuth>), ApiServerError> {
        #[cfg(feature = "api-server-tls")]
        if let Some(t) = &cfg.tls {
            let setup = super::tls::build(t)?;
            let cert_auth = setup
                .identity
                .map(|id| -> CertAuth { Arc::new(move |der| id.claims(der)) });
            return Ok((
                Some(tokio_rustls::TlsAcceptor::from(setup.server)),
                cert_auth,
            ));
        }
        let _ = cfg;
        Ok((None, None))
    }

    /// `https` when the listener terminates TLS, else `http`.
    pub fn scheme(&self) -> &'static str {
        if self.acceptor.is_some() {
            "https"
        } else {
            "http"
        }
    }

    /// The router with the whole stack, for tests and custom hosting.
    /// Peer addresses come from `ConnectInfo<SocketAddr>` in request
    /// extensions, which `axum::serve` provides.
    pub fn router(&self) -> Router {
        self.app.clone()
    }

    /// The bound address once started, e.g. a kernel-picked port.
    pub fn local_addr(&self) -> Option<String> {
        self.bound.lock().ok().and_then(|g| g.clone())
    }

    fn validate_exposure(cfg: &ApiServerConfig, mtls: bool) -> Option<String> {
        if is_loopback_addr(&cfg.addr) || cfg.auth.is_some() || mtls || cfg.insecure_remote {
            return None;
        }
        Some(format!(
            "addr: {:?} is not a loopback address and the api service has no authentication; \
             set an auth function, or insecure_remote to serve unauthenticated beyond loopback",
            cfg.addr
        ))
    }

    fn layered(
        cfg: &ApiServerConfig,
        up: Arc<AtomicBool>,
        cert_auth: Option<CertAuth>,
    ) -> Result<Router, ApiServerError> {
        let mut app = cfg.router.clone();

        if let Some(doc) = &cfg.openapi {
            let body = Arc::new(doc.to_value());
            app = app.route(
                OPENAPI_PATH,
                get(move || {
                    let b = body.clone();
                    async move { json_response(200, &*b) }
                }),
            );
        }

        // Per matched route: the request event pair.
        if let Some(p) = &cfg.publisher {
            app = app.route_layer(from_fn_with_state(
                EventState {
                    publisher: p.clone(),
                    topics: cfg.topics.clone(),
                },
                events,
            ));
        }
        // Whole router, inner to outer: auth, rate limit, gzip, body
        // limit, CORS.
        #[cfg(feature = "api-server-idempotency")]
        if let Some(c) = &cfg.idempotency {
            // Inside authentication: the key is scoped to the verified caller.
            app = app.layer(from_fn_with_state(
                super::idempotency::IdemState::new(c),
                super::idempotency::idempotency,
            ));
        }
        if cfg.auth.is_some() || cert_auth.is_some() {
            let mut st = AuthState::new(cfg.auth.clone(), cert_auth);
            if let Some(c) = &cfg.auth_challenge {
                st.challenge = c.clone();
            }
            st.on_refused = cfg.on_auth_refused.clone();
            app = app.layer(from_fn_with_state(st, auth));
        }
        if let Some(rl) = &cfg.rate_limit {
            app = app.layer(from_fn_with_state(
                Arc::new(Limiter::new(rl.clone())),
                rate_limit,
            ));
        }
        if cfg.compress {
            let predicate = SizeAbove::new(u64::from(COMPRESS_MIN_BYTES)).and(Compressible);
            app = app
                .layer(CompressionLayer::new().compress_when(predicate))
                // Outside the compression layer: a request that must
                // not be compressed (stream, upgrade, Connect, gRPC)
                // reaches it without Accept-Encoding.
                .layer(from_fn(
                    |mut req: Request, next: axum::middleware::Next| async move {
                        if skip_compression(&req) {
                            req.headers_mut().remove(header::ACCEPT_ENCODING);
                        }
                        next.run(req).await
                    },
                ));
        }
        let limit = if cfg.max_body_bytes == 0 {
            DEFAULT_MAX_BODY_BYTES
        } else {
            cfg.max_body_bytes
        };
        if limit >= 0 {
            app = app
                .layer(RequestBodyLimitLayer::new(limit as usize))
                .layer(from_fn_with_state(limit, body_limit));
        }
        if let Some(c) = &cfg.cors {
            c.validate().map_err(ApiServerError)?;
            if let Some(layer) = c.layer() {
                app = app.layer(layer);
            }
        }

        // The scrape endpoint: inside the Host and Origin checks, ahead of
        // everything above (body limit, rate limit, authentication).
        if let Some(m) = &cfg.metrics {
            m.validate(is_loopback_addr(&cfg.addr), &cfg.addr)?;
            app = app.layer(from_fn_with_state(MetricsState::new(m), metrics));
        }

        // Host and Origin checks.
        if cfg.origin_check {
            let mut allow = cfg.allow_origins.clone();
            if let Some(c) = &cfg.cors {
                allow.extend(c.allow_origins.iter().filter(|o| o.trim() != "*").cloned());
            }
            let policy = OriginPolicy::new(&OriginCheckConfig {
                allow,
                refuse: cfg.refuse.clone(),
            })?;
            if let Some(p) = policy {
                app = app.layer(from_fn_with_state(p, origin_check));
            }
        }
        if cfg.host_check {
            let (mut hosts, wildcard) = listener_hosts(&cfg.addr);
            hosts.extend(cfg.allow_hosts.iter().cloned());
            if !wildcard || !cfg.allow_hosts.is_empty() {
                app = app.layer(from_fn_with_state(
                    HostAllowlist::new(&HostCheckConfig {
                        allow: hosts,
                        refuse: cfg.refuse.clone(),
                    }),
                    host_check,
                ));
            }
        }

        // Health routes sit in front of the checks above.
        let mut checks = vec![ReadinessCheck::new(SERVICE_NAME, {
            let up = up.clone();
            move || up.load(Ordering::SeqCst)
        })];
        checks.extend(cfg.readiness.iter().cloned());
        app = app.layer(from_fn_with_state(
            HealthState {
                prefix: String::new(),
                checks,
                detail: cfg
                    .health_detail
                    .unwrap_or_else(|| is_loopback_addr(&cfg.addr)),
            },
            health,
        ));

        // Edge, inner to outer: security headers, recovery, access log,
        // client address, request id.
        if cfg.security_headers {
            if let Some(v) = hsts_value(&cfg.security_headers_config) {
                app = app.layer(from_fn_with_state(v, hsts));
            }
            for l in static_layers(&cfg.security_headers_config) {
                app = app.layer(l);
            }
        }
        let sink = cfg.log.clone().unwrap_or_else(stderr_sink);
        let panic_sink = sink.clone();
        app = app.layer(CatchPanicLayer::custom(
            move |err: Box<dyn std::any::Any + Send>| {
                let what = err
                    .downcast_ref::<&str>()
                    .map(|s| s.to_string())
                    .or_else(|| err.downcast_ref::<String>().cloned())
                    .unwrap_or_else(|| "panic".into());
                panic_sink(&panic_line(&what));
                let mut resp = Response::new(axum::body::Body::from("Internal Server Error\n"));
                *resp.status_mut() = StatusCode::INTERNAL_SERVER_ERROR;
                let h = resp.headers_mut();
                h.insert(
                    header::CONTENT_TYPE,
                    HeaderValue::from_static("text/plain; charset=utf-8"),
                );
                h.insert(
                    HeaderName::from_static("x-content-type-options"),
                    HeaderValue::from_static("nosniff"),
                );
                resp
            },
        ));
        app = app.layer(from_fn_with_state(sink, access_log));
        let proxies = parse_trusted_proxies(&cfg.trusted_proxies).map_err(ApiServerError)?;
        app = app.layer(from_fn_with_state(Arc::new(proxies), client_address));
        let id_header = HeaderName::from_static("x-request-id");
        app = app
            .layer(PropagateRequestIdLayer::new(id_header.clone()))
            .layer(SetRequestIdLayer::new(id_header.clone(), RandomIds))
            // Outermost: an id that is not a plain token is dropped, so a
            // fresh one is minted and never reaches a log line or header.
            .layer(from_fn(
                move |mut req: Request, next: axum::middleware::Next| {
                    let h = id_header.clone();
                    async move {
                        if req
                            .headers()
                            .get(&h)
                            .is_some_and(|v| !valid_request_id(v.as_bytes()))
                        {
                            req.headers_mut().remove(&h);
                        }
                        next.run(req).await
                    }
                },
            ));
        app = app.layer(SetSensitiveRequestHeadersLayer::new([
            header::AUTHORIZATION,
            HeaderName::from_static("x-api-key"),
            header::COOKIE,
        ]));
        Ok(app)
    }
}

impl Service for ApiService {
    fn name(&self) -> &str {
        SERVICE_NAME
    }

    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            let listener = tokio::net::TcpListener::bind(&self.addr)
                .await
                .map_err(|e| format!("listen: {e}"))?;
            let local = listener.local_addr().map_err(|e| e.to_string())?;
            if let Ok(mut g) = self.bound.lock() {
                *g = Some(local.to_string());
            }
            self.up.store(true, Ordering::SeqCst);
            ready.report();

            let stop = self.stop.clone();
            let shutdown = async move {
                tokio::select! {
                    () = cancel.cancelled() => {}
                    () = stop.cancelled() => {}
                }
            };
            let res = serve(
                listener,
                self.app.clone(),
                self.timeouts,
                self.acceptor.clone(),
                shutdown,
            )
            .await;
            self.up.store(false, Ordering::SeqCst);
            self.done.cancel();
            res
        })
    }

    fn ready(&self) -> bool {
        self.up.load(Ordering::SeqCst)
    }

    fn stop<'a>(&'a self, cancel: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            self.up.store(false, Ordering::SeqCst);
            self.stop.cancel();
            if self.local_addr().is_some() {
                tokio::select! {
                    () = self.done.cancelled() => {}
                    () = cancel.cancelled() => {}
                }
            }
            Ok(())
        })
    }

    fn validate(&self) -> Option<String> {
        self.validation.clone()
    }

    fn depends_on(&self) -> Vec<String> {
        self.depends_on.clone()
    }

    fn addr(&self) -> Option<String> {
        self.local_addr()
    }

    fn class(&self) -> Option<(String, String)> {
        Some(("write-shared".to_string(), "listen".to_string()))
    }
}
