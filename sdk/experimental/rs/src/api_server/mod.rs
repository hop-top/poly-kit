//! The REST server, a port of `go/transport/api` and the Go api
//! service's middleware plane. Feature `api-server`; native only.
//!
//! An axum [`Router`](axum::Router) of the adopter's routes, wrapped in
//! kit's stack, served as a [`serve::Service`](crate::serve::Service):
//! [`Service::addr`](crate::serve::Service::addr) reports where it
//! bound, ready is reported after the bind, `/readyz` answers 503 once
//! stop begins, and stop drains in-flight requests.
//!
//! # The stack
//!
//! Outer to inner:
//!
//! 1. request id (`X-Request-ID`, kept if sent), then the client
//!    address behind [trusted proxies](clientaddr::parse_trusted_proxies),
//!    the access log through [`redact`](crate::redact), panic
//!    recovery, security headers;
//! 2. `/healthz` and `/readyz`, answered before everything below;
//! 3. Host check, then Origin check, both on by default;
//! 4. CORS, body limit, gzip, rate limit, authentication;
//! 5. per matched route, the request event pair on the bus.
//!
//! Every refusal is `{"status", "code", "message"}` JSON; see
//! [`error`]. Authentication is an [`AuthFunc`](crate::authn::AuthFunc)
//! from `authn`; the verified [`Claims`](crate::authn::Claims) are in
//! request extensions for handlers.
//!
//! No axum extractor or tower-http type appears in kit's own contract
//! beyond the documented platform boundary (`Router`).
//!
//! # Example
//!
//! ```
//! use axum::routing::get;
//! use hop_top_kit::api_server::{ApiServerConfig, ApiService};
//!
//! let routes = axum::Router::new().route("/ping", get(|| async { "pong" }));
//! let svc = ApiService::build(ApiServerConfig::new("127.0.0.1:0", routes)).unwrap();
//! let _app = svc.router();
//! ```

mod accesslog;
mod auth;
pub mod clientaddr;
mod cors;
mod ctx;
mod error;
mod events;
mod headers;
mod health;
mod hosts;
mod openapi;
mod origin;
mod ratelimit;
mod server;
mod sse;
#[cfg(feature = "webhook")]
pub mod webhook;

pub use accesslog::{access_line, stderr_sink, LogSink};
pub use auth::OnAuthRefused;
pub use clientaddr::{parse_trusted_proxies, Prefix};
pub use cors::{parse_origin, CorsConfig};
pub use ctx::{is_https, Authenticated, ClientIp, ForwardedProto, PeerAddr};
pub use error::{
    json_response, unauthenticated, ApiError, ApiServerError, RefusalWriter, CODE_BODY_TOO_LARGE,
    CODE_HOST_REJECTED, CODE_METHOD_NOT_ALLOWED, CODE_ORIGIN_REJECTED, CODE_RATE_LIMITED,
    CODE_UNAUTHENTICATED, DEFAULT_AUTH_CHALLENGE,
};
pub use events::{EventPublisher, Topics, SOURCE as EVENT_SOURCE};
pub use headers::{SecurityHeadersConfig, DEFAULT_CSP, DEFAULT_HSTS_MAX_AGE};
pub use health::{
    HealthStatus, ReadinessCheck, LIVENESS_PATH, READINESS_PATH, STATUS_OK, STATUS_UNAVAILABLE,
};
pub use hosts::{host_permits, listener_hosts, split_host, HostCheckConfig, LOOPBACK_HOSTS};
pub use openapi::{
    OpenApi, Operation, Param, RequestBody, Response, Server, OPENAPI_PATH, OPENAPI_VERSION,
};
pub use origin::OriginCheckConfig;
pub use ratelimit::RateLimit;
pub use server::{
    ApiServerConfig, ApiService, COMPRESS_MIN_BYTES, DEFAULT_MAX_BODY_BYTES, SERVICE_NAME,
};
pub use sse::{sse, KEEP_ALIVE};
