//! What an interceptor and a handler see of a call, in kit's own types.

use std::net::SocketAddr;

use axum::http::{Extensions, HeaderMap, HeaderName, HeaderValue};
use connectrpc::RequestContext as ConnectContext;

use crate::authn::Claims;

/// Request headers, by case-insensitive name.
#[derive(Debug, Clone, Default)]
pub struct Headers(HeaderMap);

impl Headers {
    /// The first value of `name`, if it is text.
    pub fn get(&self, name: &str) -> Option<&str> {
        self.0.get(name).and_then(|v| v.to_str().ok())
    }

    /// Replace `name` with `value`. A name or value HTTP cannot carry
    /// is ignored.
    pub fn set(&mut self, name: &str, value: &str) {
        if let (Ok(n), Ok(v)) = (
            HeaderName::from_bytes(name.as_bytes()),
            HeaderValue::from_str(value),
        ) {
            self.0.insert(n, v);
        }
    }

    pub fn remove(&mut self, name: &str) {
        self.0.remove(name);
    }

    pub(crate) fn from_map(map: &HeaderMap) -> Self {
        Self(map.clone())
    }

    pub(crate) fn into_map(self) -> HeaderMap {
        self.0
    }
}

/// Typed values an interceptor attaches for the handlers behind it
/// (claims, a request id), keyed by type.
#[derive(Debug, Default)]
pub struct Attachments(Extensions);

impl Attachments {
    pub fn insert<T: Clone + Send + Sync + 'static>(&mut self, v: T) {
        self.0.insert(v);
    }

    pub fn get<T: Send + Sync + 'static>(&self) -> Option<&T> {
        self.0.get::<T>()
    }

    pub(crate) fn from_inner(e: Extensions) -> Self {
        Self(e)
    }

    pub(crate) fn into_inner(self) -> Extensions {
        self.0
    }
}

/// A call as an [`Interceptor`](super::Interceptor) sees it, at the
/// moment its headers are known and before the message is decoded.
#[derive(Debug)]
pub struct Call {
    /// The full procedure name, `/pkg.Service/Method`.
    pub procedure: String,
    /// The caller's headers; edits reach the handler.
    pub headers: Headers,
    /// The remote end, where the transport reports it.
    pub peer: Option<SocketAddr>,
    /// Values for the handler's [`RequestContext`]; also the place an
    /// interceptor reads what an earlier one attached.
    pub ext: Attachments,
}

impl Call {
    pub(crate) fn from_context(ctx: &ConnectContext) -> Self {
        Self {
            procedure: ctx.path().unwrap_or_default().to_string(),
            headers: Headers::from_map(ctx.headers()),
            peer: ctx
                .extensions()
                .get::<axum::extract::ConnectInfo<SocketAddr>>()
                .map(|c| c.0),
            ext: Attachments::from_inner(ctx.extensions().clone()),
        }
    }

    /// Write what the interceptors changed back onto the call.
    pub(crate) fn apply(self, ctx: &mut ConnectContext) {
        *ctx.headers_mut() = self.headers.into_map();
        *ctx.extensions_mut() = self.ext.into_inner();
    }
}

/// The id [`request_id`](super::request_id) attaches to a call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RequestId(pub String);

/// Marks a call [`authenticate`](super::authenticate) verified.
#[derive(Debug, Clone, Copy)]
pub struct Authenticated;

/// A call as a handler sees it.
#[derive(Debug)]
pub struct RequestContext {
    pub procedure: String,
    pub headers: Headers,
    pub peer: Option<SocketAddr>,
    ext: Attachments,
}

impl RequestContext {
    pub(crate) fn from_connect(ctx: &ConnectContext) -> Self {
        let call = Call::from_context(ctx);
        Self {
            procedure: call.procedure,
            headers: call.headers,
            peer: call.peer,
            ext: call.ext,
        }
    }

    /// The request id, when [`request_id`](super::request_id) ran.
    pub fn request_id(&self) -> Option<&str> {
        self.ext.get::<RequestId>().map(|r| r.0.as_str())
    }

    /// Whether [`authenticate`](super::authenticate) verified the
    /// caller: the answer to "did this caller authenticate", which an
    /// absent claims value cannot give.
    pub fn authenticated(&self) -> bool {
        self.ext.get::<Authenticated>().is_some()
    }

    /// The verified caller's claims.
    pub fn claims(&self) -> Option<&Claims> {
        self.ext.get::<Claims>()
    }

    /// A value an interceptor attached.
    pub fn extension<T: Send + Sync + 'static>(&self) -> Option<&T> {
        self.ext.get::<T>()
    }
}
