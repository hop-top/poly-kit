//! `crud.v1.EntityService`, the shared generic-CRUD contract, as a kit
//! port: implement [`EntityService`] with owned protobuf messages and
//! kit's [`RequestContext`] / [`RpcError`], mount [`EntityRpc`]. The
//! adapter onto the generated connectrpc stub is internal.
//!
//! This is the typed counterpart of Go's `RPCResource` minus its
//! `api.Service[T]` generic, which waits on a Rust `api.Service`.

use std::future::Future;
use std::sync::Arc;

use connectrpc::{Response, ServiceRequest, ServiceResult};

use super::call::RequestContext;
use super::error::RpcError;
use super::proto::crud::v1::{
    CreateRequest, CreateResponse, DeleteRequest, DeleteResponse, GetRequest, GetResponse,
    ListRequest, ListResponse, UpdateRequest, UpdateResponse,
};
use super::server::{Mount, RpcService};
use crate::rpc::connect::crud::v1::EntityService as Stub;

/// Generic CRUD over `google.protobuf.Struct` payloads.
pub trait EntityService: Send + Sync + 'static {
    fn create(
        &self,
        ctx: &RequestContext,
        req: CreateRequest,
    ) -> impl Future<Output = Result<CreateResponse, RpcError>> + Send;
    fn get(
        &self,
        ctx: &RequestContext,
        req: GetRequest,
    ) -> impl Future<Output = Result<GetResponse, RpcError>> + Send;
    fn list(
        &self,
        ctx: &RequestContext,
        req: ListRequest,
    ) -> impl Future<Output = Result<ListResponse, RpcError>> + Send;
    fn update(
        &self,
        ctx: &RequestContext,
        req: UpdateRequest,
    ) -> impl Future<Output = Result<UpdateResponse, RpcError>> + Send;
    fn delete(
        &self,
        ctx: &RequestContext,
        req: DeleteRequest,
    ) -> impl Future<Output = Result<DeleteResponse, RpcError>> + Send;
}

/// Mounts an [`EntityService`] at `/crud.v1.EntityService/`.
pub struct EntityRpc<T>(Arc<T>);

impl<T: EntityService> EntityRpc<T> {
    pub fn new(service: T) -> Self {
        Self(Arc::new(service))
    }

    /// Mount a service that is shared elsewhere too.
    pub fn shared(service: Arc<T>) -> Self {
        Self(service)
    }
}

struct Adapter<T>(Arc<T>);

impl<T: EntityService> RpcService for EntityRpc<T> {
    fn mount(self: Arc<Self>, mount: &mut Mount) {
        mount.generated(Arc::new(Adapter(self.0.clone())));
    }
}

macro_rules! method {
    ($name:ident, $req:ident, $resp:ident) => {
        fn $name<'a>(
            &'a self,
            ctx: ::connectrpc::RequestContext,
            request: ServiceRequest<'_, $req>,
        ) -> impl Future<
            Output = ServiceResult<impl ::connectrpc::Encodable<$resp> + Send + use<'a, T>>,
        > + Send {
            let kit = RequestContext::from_connect(&ctx);
            let req = request.to_owned_message();
            async move {
                match self.0.$name(&kit, req).await {
                    Ok(r) => Response::ok(r),
                    Err(e) => Err(e.into()),
                }
            }
        }
    };
}

impl<T: EntityService> Stub for Adapter<T> {
    method!(create, CreateRequest, CreateResponse);
    method!(get, GetRequest, GetResponse);
    method!(list, ListRequest, ListResponse);
    method!(update, UpdateRequest, UpdateResponse);
    method!(delete, DeleteRequest, DeleteResponse);
}
