//! The auth middleware: authn's `AuthFunc` in front of every route.

use std::sync::Arc;

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::{Authenticated, PeerCertificate};
use super::error::{unauthenticated, DEFAULT_AUTH_CHALLENGE};
use crate::authn::{AuthFunc, AuthnError, Claims, Credentials};

/// Reads the caller from a verified client certificate (DER). Under
/// mTLS the certificate is the credential and `func` is not consulted.
pub(crate) type CertAuth = Arc<dyn Fn(&[u8]) -> Result<Claims, String> + Send + Sync>;

/// Called with every refusal, for audit.
pub type OnAuthRefused = Arc<dyn Fn(&AuthnError) + Send + Sync>;

#[derive(Clone)]
pub(crate) struct AuthState {
    pub(crate) func: Option<AuthFunc>,
    pub(crate) cert: Option<CertAuth>,
    pub(crate) challenge: String,
    pub(crate) on_refused: Option<OnAuthRefused>,
}

impl AuthState {
    pub(crate) fn new(func: Option<AuthFunc>, cert: Option<CertAuth>) -> Self {
        Self {
            func,
            cert,
            challenge: DEFAULT_AUTH_CHALLENGE.to_string(),
            on_refused: None,
        }
    }
}

fn header<'a>(req: &'a Request, name: &str) -> Option<&'a str> {
    req.headers().get(name).and_then(|v| v.to_str().ok())
}

pub(crate) async fn auth(State(st): State<AuthState>, mut req: Request, next: Next) -> Response {
    let verdict = match (&st.cert, &st.func) {
        (Some(cert), _) => match req.extensions().get::<PeerCertificate>() {
            None => Err(AuthnError::InvalidToken(
                "client certificate required".into(),
            )),
            Some(pc) => cert(&pc.0).map_err(AuthnError::InvalidToken),
        },
        (None, Some(func)) => func(&Credentials {
            authorization: header(&req, "authorization"),
            api_key: header(&req, "x-api-key"),
        }),
        (None, None) => return next.run(req).await,
    };
    match verdict {
        Ok(claims) => {
            req.extensions_mut().insert(claims);
            req.extensions_mut().insert(Authenticated);
            next.run(req).await
        }
        Err(e) => {
            if let Some(f) = &st.on_refused {
                f(&e);
            }
            let msg = match (&st.cert, &e) {
                (Some(_), AuthnError::InvalidToken(m)) => m.clone(),
                _ => e.to_string(),
            };
            unauthenticated(&st.challenge, &msg)
        }
    }
}
