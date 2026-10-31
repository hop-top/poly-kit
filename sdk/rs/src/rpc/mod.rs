//! Connect, gRPC and gRPC-Web on one port, a port of `go/transport/rpc`.
//!
//! [`RpcServer`] is a [`serve::Service`](crate::serve::Service) named
//! `rpc`: it answers all three protocols, binary or JSON, over HTTP/1.1
//! and (with prior knowledge) unencrypted HTTP/2 on the one listener.
//! Interceptors ([`request_id`], [`log`], [`authenticate`]) mirror the Go
//! ones and see every call, unary or streaming; a handler panic is
//! `internal`; a request body is bounded.
//!
//! # Boundary
//!
//! The server, interceptors, errors and call context are kit's own types
//! ([`RpcService`], [`Interceptor`], [`RpcError`], [`Call`],
//! [`RequestContext`]). The protocol stack (`connectrpc`, `buffa`, axum,
//! tower) stays inside. Two things cross by design: protobuf message
//! types, the data contract ([`proto`]), and the stubs `buf generate`
//! emits for a tool's own protos, which are connectrpc-typed and register
//! through [`Mount::generated`].
//!
//! # Generated code
//!
//! `gen/` holds the committed output of `make proto-rs`, from
//! `contracts/proto/crud/v1` with the plugins pinned in `buf.gen.rs.yaml`.
//! There is no `build.rs` and no `protoc` at build time.

// Generated: rustfmt must not rewrite what `make proto-rs-check` compares.
#[rustfmt::skip]
#[path = "gen/buffa/mod.rs"]
pub mod proto;

#[rustfmt::skip]
#[path = "gen/connect/mod.rs"]
pub mod connect;

mod call;
mod entity;
mod error;
mod interceptor;
mod interceptors;
mod server;

pub use call::{Attachments, Authenticated, Call, Headers, RequestContext, RequestId};
pub use entity::{EntityRpc, EntityService};
pub use error::{Code, RpcError};
pub use interceptor::{Interceptor, Next, RpcFuture};
pub use interceptors::{authenticate, log, request_id, Authenticate, RefusedCall, RpcLog};
pub use server::{
    Mount, RpcServer, RpcServerBuilder, RpcService, DEFAULT_MAX_BODY_BYTES, SERVICE_NAME,
};
