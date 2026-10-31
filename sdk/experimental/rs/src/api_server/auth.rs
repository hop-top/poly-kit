//! The auth middleware: authn's `AuthFunc` in front of every route.

use std::sync::Arc;

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::Authenticated;
use super::error::{unauthenticated, DEFAULT_AUTH_CHALLENGE};
use crate::authn::{AuthFunc, AuthnError, Credentials};

/// Called with every refusal, for audit.
pub type OnAuthRefused = Arc<dyn Fn(&AuthnError) + Send + Sync>;

#[derive(Clone)]
pub(crate) struct AuthState {
    pub(crate) func: AuthFunc,
    pub(crate) challenge: String,
    pub(crate) on_refused: Option<OnAuthRefused>,
}

impl AuthState {
    pub(crate) fn new(func: AuthFunc) -> Self {
        Self {
            func,
            challenge: DEFAULT_AUTH_CHALLENGE.to_string(),
            on_refused: None,
        }
    }
}

fn header<'a>(req: &'a Request, name: &str) -> Option<&'a str> {
    req.headers().get(name).and_then(|v| v.to_str().ok())
}

pub(crate) async fn auth(State(st): State<AuthState>, mut req: Request, next: Next) -> Response {
    let creds = Credentials {
        authorization: header(&req, "authorization"),
        api_key: header(&req, "x-api-key"),
    };
    match (st.func)(&creds) {
        Ok(claims) => {
            req.extensions_mut().insert(claims);
            req.extensions_mut().insert(Authenticated);
            next.run(req).await
        }
        Err(e) => {
            if let Some(f) = &st.on_refused {
                f(&e);
            }
            unauthenticated(&st.challenge, &e.to_string())
        }
    }
}
