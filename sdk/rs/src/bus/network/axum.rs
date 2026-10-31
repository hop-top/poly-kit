//! The axum adapter: Go's `Handler` mounted on an axum router.

use std::net::SocketAddr;
use std::sync::atomic::{AtomicU64, Ordering};

use axum::body::Body;
use axum::extract::{ConnectInfo, Request};
use axum::response::Response;
use hyper_util::rt::TokioIo;

use super::adapter::NetworkAdapter;
use super::upgrade::check_upgrade;

impl NetworkAdapter {
    /// An axum handler accepting peers on whatever route it is mounted:
    /// `Router::new().route("/bus", any(net.axum_handler()))`.
    ///
    /// Requests failing [`check_upgrade`] get its response; the rest get
    /// `101` and are handed to [`accept_upgraded`](Self::accept_upgraded).
    /// Links are keyed by the peer's address when the router is served
    /// with `into_make_service_with_connect_info::<SocketAddr>()`, else by
    /// a unique placeholder.
    pub fn axum_handler(
        &self,
    ) -> impl Fn(Request) -> std::pin::Pin<Box<dyn std::future::Future<Output = Response> + Send>>
           + Clone
           + Send
           + Sync
           + 'static {
        let net = self.clone();
        move |req: Request| {
            let net = net.clone();
            Box::pin(async move { net.handle_axum(req) })
        }
    }

    fn handle_axum(&self, mut req: Request) -> Response {
        let upgrade = match check_upgrade(&req) {
            Ok(u) => u,
            Err(r) => return r.response().map(Body::from),
        };
        let addr = req
            .extensions()
            .get::<ConnectInfo<SocketAddr>>()
            .map_or_else(placeholder_addr, |ci| ci.0.to_string());
        let on_upgrade = hyper::upgrade::on(&mut req);
        let net = self.clone();
        self.0.spawn_tracked(async move {
            if let Ok(io) = on_upgrade.await {
                net.accept_upgraded(TokioIo::new(io), addr);
            }
        });
        upgrade.response().map(|()| Body::empty())
    }
}

/// A unique link key for a peer whose address the router did not record.
fn placeholder_addr() -> String {
    static NEXT: AtomicU64 = AtomicU64::new(1);
    format!("axum-peer-{}", NEXT.fetch_add(1, Ordering::Relaxed))
}
