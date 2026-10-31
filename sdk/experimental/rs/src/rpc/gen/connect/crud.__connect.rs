///Shorthand for `OwnedView<CreateRequestView<'static>>`.
pub type OwnedCreateRequestView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::CreateRequestView<'static>,
>;
///Shorthand for `OwnedView<CreateResponseView<'static>>`.
pub type OwnedCreateResponseView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::CreateResponseView<'static>,
>;
///Shorthand for `OwnedView<GetRequestView<'static>>`.
pub type OwnedGetRequestView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::GetRequestView<'static>,
>;
///Shorthand for `OwnedView<GetResponseView<'static>>`.
pub type OwnedGetResponseView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::GetResponseView<'static>,
>;
///Shorthand for `OwnedView<ListRequestView<'static>>`.
pub type OwnedListRequestView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::ListRequestView<'static>,
>;
///Shorthand for `OwnedView<ListResponseView<'static>>`.
pub type OwnedListResponseView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::ListResponseView<'static>,
>;
///Shorthand for `OwnedView<UpdateRequestView<'static>>`.
pub type OwnedUpdateRequestView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::UpdateRequestView<'static>,
>;
///Shorthand for `OwnedView<UpdateResponseView<'static>>`.
pub type OwnedUpdateResponseView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::UpdateResponseView<'static>,
>;
///Shorthand for `OwnedView<DeleteRequestView<'static>>`.
pub type OwnedDeleteRequestView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::DeleteRequestView<'static>,
>;
///Shorthand for `OwnedView<DeleteResponseView<'static>>`.
pub type OwnedDeleteResponseView = ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::DeleteResponseView<'static>,
>;
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::CreateResponse>
for crate::rpc::proto::crud::v1::__buffa::view::CreateResponseView<'_> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self, codec)
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::CreateResponse>
for ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::CreateResponseView<'static>,
> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self.reborrow(), codec)
    }
    /// An `OwnedView` still holds the buffer it was decoded from, so
    /// its large fields can be handed to the response body by
    /// reference count instead of copied. The bare view impl above
    /// cannot do this: it has borrows but no buffer to name.
    fn encode_segments(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::connectrpc::EncodedBody, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body_segments(
            self.reborrow(),
            self.bytes(),
            codec,
        )
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::GetResponse>
for crate::rpc::proto::crud::v1::__buffa::view::GetResponseView<'_> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self, codec)
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::GetResponse>
for ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::GetResponseView<'static>,
> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self.reborrow(), codec)
    }
    /// An `OwnedView` still holds the buffer it was decoded from, so
    /// its large fields can be handed to the response body by
    /// reference count instead of copied. The bare view impl above
    /// cannot do this: it has borrows but no buffer to name.
    fn encode_segments(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::connectrpc::EncodedBody, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body_segments(
            self.reborrow(),
            self.bytes(),
            codec,
        )
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::ListResponse>
for crate::rpc::proto::crud::v1::__buffa::view::ListResponseView<'_> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self, codec)
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::ListResponse>
for ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::ListResponseView<'static>,
> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self.reborrow(), codec)
    }
    /// An `OwnedView` still holds the buffer it was decoded from, so
    /// its large fields can be handed to the response body by
    /// reference count instead of copied. The bare view impl above
    /// cannot do this: it has borrows but no buffer to name.
    fn encode_segments(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::connectrpc::EncodedBody, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body_segments(
            self.reborrow(),
            self.bytes(),
            codec,
        )
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::UpdateResponse>
for crate::rpc::proto::crud::v1::__buffa::view::UpdateResponseView<'_> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self, codec)
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::UpdateResponse>
for ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::UpdateResponseView<'static>,
> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self.reborrow(), codec)
    }
    /// An `OwnedView` still holds the buffer it was decoded from, so
    /// its large fields can be handed to the response body by
    /// reference count instead of copied. The bare view impl above
    /// cannot do this: it has borrows but no buffer to name.
    fn encode_segments(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::connectrpc::EncodedBody, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body_segments(
            self.reborrow(),
            self.bytes(),
            codec,
        )
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::DeleteResponse>
for crate::rpc::proto::crud::v1::__buffa::view::DeleteResponseView<'_> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self, codec)
    }
}
impl ::connectrpc::Encodable<crate::rpc::proto::crud::v1::DeleteResponse>
for ::buffa::view::OwnedView<
    crate::rpc::proto::crud::v1::__buffa::view::DeleteResponseView<'static>,
> {
    fn encode(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::buffa::bytes::Bytes, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body(self.reborrow(), codec)
    }
    /// An `OwnedView` still holds the buffer it was decoded from, so
    /// its large fields can be handed to the response body by
    /// reference count instead of copied. The bare view impl above
    /// cannot do this: it has borrows but no buffer to name.
    fn encode_segments(
        &self,
        codec: ::connectrpc::CodecFormat,
    ) -> ::std::result::Result<::connectrpc::EncodedBody, ::connectrpc::ConnectError> {
        ::connectrpc::__codegen::encode_view_body_segments(
            self.reborrow(),
            self.bytes(),
            codec,
        )
    }
}
/// Full service name for this service.
pub const ENTITY_SERVICE_SERVICE_NAME: &str = "crud.v1.EntityService";
/// Static [`Spec`](::connectrpc::Spec) for the `Create` RPC, as seen by the server; the generated client passes it with [`origin`](::connectrpc::Spec::origin) `Client` (compare across sides with [`Spec::same_method`](::connectrpc::Spec::same_method)).
pub const ENTITY_SERVICE_CREATE_SPEC: ::connectrpc::Spec = ::connectrpc::Spec::server(
        "/crud.v1.EntityService/Create",
        ::connectrpc::StreamType::Unary,
    )
    .with_idempotency_level(::connectrpc::IdempotencyLevel::Unknown);
/// Static [`Spec`](::connectrpc::Spec) for the `Get` RPC, as seen by the server; the generated client passes it with [`origin`](::connectrpc::Spec::origin) `Client` (compare across sides with [`Spec::same_method`](::connectrpc::Spec::same_method)).
pub const ENTITY_SERVICE_GET_SPEC: ::connectrpc::Spec = ::connectrpc::Spec::server(
        "/crud.v1.EntityService/Get",
        ::connectrpc::StreamType::Unary,
    )
    .with_idempotency_level(::connectrpc::IdempotencyLevel::Unknown);
/// Static [`Spec`](::connectrpc::Spec) for the `List` RPC, as seen by the server; the generated client passes it with [`origin`](::connectrpc::Spec::origin) `Client` (compare across sides with [`Spec::same_method`](::connectrpc::Spec::same_method)).
pub const ENTITY_SERVICE_LIST_SPEC: ::connectrpc::Spec = ::connectrpc::Spec::server(
        "/crud.v1.EntityService/List",
        ::connectrpc::StreamType::Unary,
    )
    .with_idempotency_level(::connectrpc::IdempotencyLevel::Unknown);
/// Static [`Spec`](::connectrpc::Spec) for the `Update` RPC, as seen by the server; the generated client passes it with [`origin`](::connectrpc::Spec::origin) `Client` (compare across sides with [`Spec::same_method`](::connectrpc::Spec::same_method)).
pub const ENTITY_SERVICE_UPDATE_SPEC: ::connectrpc::Spec = ::connectrpc::Spec::server(
        "/crud.v1.EntityService/Update",
        ::connectrpc::StreamType::Unary,
    )
    .with_idempotency_level(::connectrpc::IdempotencyLevel::Unknown);
/// Static [`Spec`](::connectrpc::Spec) for the `Delete` RPC, as seen by the server; the generated client passes it with [`origin`](::connectrpc::Spec::origin) `Client` (compare across sides with [`Spec::same_method`](::connectrpc::Spec::same_method)).
pub const ENTITY_SERVICE_DELETE_SPEC: ::connectrpc::Spec = ::connectrpc::Spec::server(
        "/crud.v1.EntityService/Delete",
        ::connectrpc::StreamType::Unary,
    )
    .with_idempotency_level(::connectrpc::IdempotencyLevel::Unknown);
/// EntityService provides generic CRUD operations over arbitrary
/// domain entities. Payloads use google.protobuf.Struct so callers
/// can work with any schema without per-type proto definitions.
///
/// # Implementing handlers
///
/// Implement methods with plain `async fn`; the returned future satisfies
/// the `Send` bound automatically.
///
/// **Unary and server-streaming requests** arrive as
/// [`ServiceRequest<'_, Req>`](::connectrpc::ServiceRequest): a zero-copy
/// view of the request plus its body, valid for the duration of the call.
/// Fields are read directly (`request.name` is a `&str` into the decoded
/// buffer) and the borrow may be held across `.await` points. Anything
/// that must outlive the call — `tokio::spawn`, channels, server state,
/// or data captured by a returned response stream — takes owned data:
/// call `request.to_owned_message()` (or copy the specific fields)
/// first.
///
/// **Client-streaming and bidi requests** arrive as
/// [`InboundStream<Req>`](::connectrpc::InboundStream) — a
/// `ServiceStream` of [`StreamMessage`](::connectrpc::StreamMessage)s.
/// Each item owns its decoded buffer and is `Send + 'static`, so items
/// can be buffered or moved into spawned tasks; read fields zero-copy
/// through the generated accessor methods (`item.name()`) or `.view()`,
/// convert with `.to_owned_message()`, or yield an item back unchanged —
/// `StreamMessage<M>` implements `Encodable<M>`.
///
/// Request types resolved through `extern_path` (e.g. well-known types
/// from another crate) use the same wrappers; the crate that owns the
/// type must be generated with buffa ≥ 0.9.0 and views enabled so the
/// backing `HasMessageView` impl exists.
///
/// The `impl Encodable<Out>` return bound accepts the owned `Out`, the
/// generated `OutView<'_>` / `OwnedOutView`,
/// [`MaybeBorrowed`](::connectrpc::MaybeBorrowed), or
/// [`PreEncoded`](::connectrpc::PreEncoded) for handlers that encode a
/// non-`'static` view internally and pass the bytes across the handler
/// boundary. View bodies are not emitted for output types mapped via
/// `extern_path` (the impl would be an orphan); return owned for
/// WKT/extern outputs.
///
/// Server-streaming and bidi-streaming methods return
/// `ServiceStream<impl Encodable<Out> + Send + use<Self>>`. The
/// `use<Self>` precise-capturing clause excludes `&self`'s lifetime and
/// the request's lifetime (unary methods use `use<'a, Self>` and may
/// borrow from `&self`), so stream items must be `'static` and cannot
/// borrow from the request. To stream view-encoded data, encode each
/// item inside the stream body and yield
/// [`PreEncoded`](::connectrpc::PreEncoded) — see its `# Streaming
/// example` doc.
#[allow(clippy::type_complexity)]
pub trait EntityService: Send + Sync + 'static {
    /// Create persists a new entity and returns the stored result.
    ///
    /// `'a` lets the response body borrow from `&self` (e.g. server-resident state).
    ///
    /// `request` is borrowed from the request body and is valid for the
    /// duration of the call; message fields are read directly on it
    /// (zero-copy). The response cannot borrow from `request` — use
    /// `.to_owned_message()` (or copy the specific fields) for anything
    /// returned, stored, or moved into `tokio::spawn`.
    fn create<'a>(
        &'a self,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::ServiceRequest<
            '_,
            crate::rpc::proto::crud::v1::CreateRequest,
        >,
    ) -> impl ::std::future::Future<
        Output = ::connectrpc::ServiceResult<
            impl ::connectrpc::Encodable<
                crate::rpc::proto::crud::v1::CreateResponse,
            > + Send + use<'a, Self>,
        >,
    > + Send;
    /// Get retrieves a single entity by ID.
    ///
    /// `'a` lets the response body borrow from `&self` (e.g. server-resident state).
    ///
    /// `request` is borrowed from the request body and is valid for the
    /// duration of the call; message fields are read directly on it
    /// (zero-copy). The response cannot borrow from `request` — use
    /// `.to_owned_message()` (or copy the specific fields) for anything
    /// returned, stored, or moved into `tokio::spawn`.
    fn get<'a>(
        &'a self,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::ServiceRequest<
            '_,
            crate::rpc::proto::crud::v1::GetRequest,
        >,
    ) -> impl ::std::future::Future<
        Output = ::connectrpc::ServiceResult<
            impl ::connectrpc::Encodable<
                crate::rpc::proto::crud::v1::GetResponse,
            > + Send + use<'a, Self>,
        >,
    > + Send;
    /// List returns entities matching the query parameters.
    ///
    /// `'a` lets the response body borrow from `&self` (e.g. server-resident state).
    ///
    /// `request` is borrowed from the request body and is valid for the
    /// duration of the call; message fields are read directly on it
    /// (zero-copy). The response cannot borrow from `request` — use
    /// `.to_owned_message()` (or copy the specific fields) for anything
    /// returned, stored, or moved into `tokio::spawn`.
    fn list<'a>(
        &'a self,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::ServiceRequest<
            '_,
            crate::rpc::proto::crud::v1::ListRequest,
        >,
    ) -> impl ::std::future::Future<
        Output = ::connectrpc::ServiceResult<
            impl ::connectrpc::Encodable<
                crate::rpc::proto::crud::v1::ListResponse,
            > + Send + use<'a, Self>,
        >,
    > + Send;
    /// Update replaces an existing entity and returns the result.
    ///
    /// `'a` lets the response body borrow from `&self` (e.g. server-resident state).
    ///
    /// `request` is borrowed from the request body and is valid for the
    /// duration of the call; message fields are read directly on it
    /// (zero-copy). The response cannot borrow from `request` — use
    /// `.to_owned_message()` (or copy the specific fields) for anything
    /// returned, stored, or moved into `tokio::spawn`.
    fn update<'a>(
        &'a self,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::ServiceRequest<
            '_,
            crate::rpc::proto::crud::v1::UpdateRequest,
        >,
    ) -> impl ::std::future::Future<
        Output = ::connectrpc::ServiceResult<
            impl ::connectrpc::Encodable<
                crate::rpc::proto::crud::v1::UpdateResponse,
            > + Send + use<'a, Self>,
        >,
    > + Send;
    /// Delete removes an entity by ID.
    ///
    /// `'a` lets the response body borrow from `&self` (e.g. server-resident state).
    ///
    /// `request` is borrowed from the request body and is valid for the
    /// duration of the call; message fields are read directly on it
    /// (zero-copy). The response cannot borrow from `request` — use
    /// `.to_owned_message()` (or copy the specific fields) for anything
    /// returned, stored, or moved into `tokio::spawn`.
    fn delete<'a>(
        &'a self,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::ServiceRequest<
            '_,
            crate::rpc::proto::crud::v1::DeleteRequest,
        >,
    ) -> impl ::std::future::Future<
        Output = ::connectrpc::ServiceResult<
            impl ::connectrpc::Encodable<
                crate::rpc::proto::crud::v1::DeleteResponse,
            > + Send + use<'a, Self>,
        >,
    > + Send;
}
/// Extension trait for registering a service implementation with a Router.
///
/// This trait is automatically implemented for all types that implement the service trait.
/// Prefer [`Router::add_service`](::connectrpc::Router::add_service) for
/// top-down registration; `register` remains available for compatibility
/// and cases where the service-first call shape is more convenient.
///
/// # Example
///
/// ```rust,ignore
/// use std::sync::Arc;
///
/// let service = Arc::new(MyServiceImpl);
/// let router = service.register(Router::new());
/// ```
pub trait EntityServiceExt: EntityService {
    /// Register this service implementation with a Router.
    ///
    /// Takes ownership of the `Arc<Self>` and returns a new Router with
    /// this service's methods registered.
    fn register(
        self: ::std::sync::Arc<Self>,
        router: ::connectrpc::Router,
    ) -> ::connectrpc::Router;
}
impl<S: EntityService> EntityServiceExt for S {
    fn register(
        self: ::std::sync::Arc<Self>,
        router: ::connectrpc::Router,
    ) -> ::connectrpc::Router {
        router
            .route_view(
                ENTITY_SERVICE_SERVICE_NAME,
                "Create",
                {
                    let svc = ::std::sync::Arc::clone(&self);
                    ::connectrpc::view_handler_fn(move |
                        ctx,
                        req: ::buffa::view::OwnedView<
                            crate::rpc::proto::crud::v1::__buffa::view::CreateRequestView<
                                'static,
                            >,
                        >,
                        format|
                    {
                        let svc = ::std::sync::Arc::clone(&svc);
                        async move {
                            let sreq = ::connectrpc::ServiceRequest::<
                                crate::rpc::proto::crud::v1::CreateRequest,
                            >::from_parts(req.reborrow(), req.bytes());
                            svc.create(ctx, sreq)
                                .await?
                                .encode::<
                                    crate::rpc::proto::crud::v1::CreateResponse,
                                >(format)
                        }
                    })
                },
            )
            .with_spec(ENTITY_SERVICE_CREATE_SPEC)
            .route_view(
                ENTITY_SERVICE_SERVICE_NAME,
                "Get",
                {
                    let svc = ::std::sync::Arc::clone(&self);
                    ::connectrpc::view_handler_fn(move |
                        ctx,
                        req: ::buffa::view::OwnedView<
                            crate::rpc::proto::crud::v1::__buffa::view::GetRequestView<
                                'static,
                            >,
                        >,
                        format|
                    {
                        let svc = ::std::sync::Arc::clone(&svc);
                        async move {
                            let sreq = ::connectrpc::ServiceRequest::<
                                crate::rpc::proto::crud::v1::GetRequest,
                            >::from_parts(req.reborrow(), req.bytes());
                            svc.get(ctx, sreq)
                                .await?
                                .encode::<crate::rpc::proto::crud::v1::GetResponse>(format)
                        }
                    })
                },
            )
            .with_spec(ENTITY_SERVICE_GET_SPEC)
            .route_view(
                ENTITY_SERVICE_SERVICE_NAME,
                "List",
                {
                    let svc = ::std::sync::Arc::clone(&self);
                    ::connectrpc::view_handler_fn(move |
                        ctx,
                        req: ::buffa::view::OwnedView<
                            crate::rpc::proto::crud::v1::__buffa::view::ListRequestView<
                                'static,
                            >,
                        >,
                        format|
                    {
                        let svc = ::std::sync::Arc::clone(&svc);
                        async move {
                            let sreq = ::connectrpc::ServiceRequest::<
                                crate::rpc::proto::crud::v1::ListRequest,
                            >::from_parts(req.reborrow(), req.bytes());
                            svc.list(ctx, sreq)
                                .await?
                                .encode::<crate::rpc::proto::crud::v1::ListResponse>(format)
                        }
                    })
                },
            )
            .with_spec(ENTITY_SERVICE_LIST_SPEC)
            .route_view(
                ENTITY_SERVICE_SERVICE_NAME,
                "Update",
                {
                    let svc = ::std::sync::Arc::clone(&self);
                    ::connectrpc::view_handler_fn(move |
                        ctx,
                        req: ::buffa::view::OwnedView<
                            crate::rpc::proto::crud::v1::__buffa::view::UpdateRequestView<
                                'static,
                            >,
                        >,
                        format|
                    {
                        let svc = ::std::sync::Arc::clone(&svc);
                        async move {
                            let sreq = ::connectrpc::ServiceRequest::<
                                crate::rpc::proto::crud::v1::UpdateRequest,
                            >::from_parts(req.reborrow(), req.bytes());
                            svc.update(ctx, sreq)
                                .await?
                                .encode::<
                                    crate::rpc::proto::crud::v1::UpdateResponse,
                                >(format)
                        }
                    })
                },
            )
            .with_spec(ENTITY_SERVICE_UPDATE_SPEC)
            .route_view(
                ENTITY_SERVICE_SERVICE_NAME,
                "Delete",
                {
                    let svc = ::std::sync::Arc::clone(&self);
                    ::connectrpc::view_handler_fn(move |
                        ctx,
                        req: ::buffa::view::OwnedView<
                            crate::rpc::proto::crud::v1::__buffa::view::DeleteRequestView<
                                'static,
                            >,
                        >,
                        format|
                    {
                        let svc = ::std::sync::Arc::clone(&svc);
                        async move {
                            let sreq = ::connectrpc::ServiceRequest::<
                                crate::rpc::proto::crud::v1::DeleteRequest,
                            >::from_parts(req.reborrow(), req.bytes());
                            svc.delete(ctx, sreq)
                                .await?
                                .encode::<
                                    crate::rpc::proto::crud::v1::DeleteResponse,
                                >(format)
                        }
                    })
                },
            )
            .with_spec(ENTITY_SERVICE_DELETE_SPEC)
    }
}
/// Type-inference marker used by [`Router::add_service`](::connectrpc::Router::add_service).
#[doc(hidden)]
pub struct EntityServiceRegisterMarker;
impl<S: EntityService> ::connectrpc::ServiceRegister<EntityServiceRegisterMarker>
for ::std::sync::Arc<S> {
    fn register_service(self, router: ::connectrpc::Router) -> ::connectrpc::Router {
        <S as EntityServiceExt>::register(self, router)
    }
}
/// Monomorphic dispatcher for `EntityService`.
///
/// Unlike `.register(Router)` which type-erases each method into an `Arc<dyn ErasedHandler>` stored in a `HashMap`, this struct dispatches via a compile-time `match` on method name: no vtable, no hash lookup.
///
/// # Example
///
/// ```rust,ignore
/// use connectrpc::ConnectRpcService;
///
/// let server = EntityServiceServer::new(MyImpl);
/// let service = ConnectRpcService::new(server);
/// // hand `service` to axum/hyper as a fallback_service
/// ```
pub struct EntityServiceServer<T> {
    inner: ::std::sync::Arc<T>,
}
impl<T: EntityService> EntityServiceServer<T> {
    /// Wrap a service implementation in a monomorphic dispatcher.
    pub fn new(service: T) -> Self {
        Self {
            inner: ::std::sync::Arc::new(service),
        }
    }
    /// Wrap an already-`Arc`'d service implementation.
    pub fn from_arc(inner: ::std::sync::Arc<T>) -> Self {
        Self { inner }
    }
}
impl<T> Clone for EntityServiceServer<T> {
    fn clone(&self) -> Self {
        Self {
            inner: ::std::sync::Arc::clone(&self.inner),
        }
    }
}
impl<T: EntityService> ::connectrpc::Dispatcher for EntityServiceServer<T> {
    #[inline]
    fn lookup(
        &self,
        path: &str,
    ) -> Option<::connectrpc::dispatcher::codegen::MethodDescriptor> {
        let method = path.strip_prefix("crud.v1.EntityService/")?;
        match method {
            "Create" => {
                Some(
                    ::connectrpc::dispatcher::codegen::MethodDescriptor::unary(false)
                        .with_spec(ENTITY_SERVICE_CREATE_SPEC),
                )
            }
            "Get" => {
                Some(
                    ::connectrpc::dispatcher::codegen::MethodDescriptor::unary(false)
                        .with_spec(ENTITY_SERVICE_GET_SPEC),
                )
            }
            "List" => {
                Some(
                    ::connectrpc::dispatcher::codegen::MethodDescriptor::unary(false)
                        .with_spec(ENTITY_SERVICE_LIST_SPEC),
                )
            }
            "Update" => {
                Some(
                    ::connectrpc::dispatcher::codegen::MethodDescriptor::unary(false)
                        .with_spec(ENTITY_SERVICE_UPDATE_SPEC),
                )
            }
            "Delete" => {
                Some(
                    ::connectrpc::dispatcher::codegen::MethodDescriptor::unary(false)
                        .with_spec(ENTITY_SERVICE_DELETE_SPEC),
                )
            }
            _ => None,
        }
    }
    fn call_unary(
        &self,
        path: &str,
        ctx: ::connectrpc::RequestContext,
        request: ::connectrpc::Payload,
        format: ::connectrpc::CodecFormat,
    ) -> ::connectrpc::dispatcher::codegen::UnaryResult {
        let Some(method) = path.strip_prefix("crud.v1.EntityService/") else {
            return ::connectrpc::dispatcher::codegen::unimplemented_unary(path);
        };
        let _ = (&ctx, &request, &format);
        match method {
            "Create" => {
                let svc = ::std::sync::Arc::clone(&self.inner);
                Box::pin(async move {
                    let body = ::connectrpc::dispatcher::codegen::request_proto_bytes::<
                        crate::rpc::proto::crud::v1::CreateRequest,
                    >(request.encoded()?, format)?;
                    let req: crate::rpc::proto::crud::v1::__buffa::view::CreateRequestView<
                        '_,
                    > = ::connectrpc::dispatcher::codegen::decode_borrowed_request_view(
                        &body,
                        ctx.decode_options(),
                    )?;
                    let req = ::connectrpc::ServiceRequest::<
                        crate::rpc::proto::crud::v1::CreateRequest,
                    >::from_parts(&req, &body);
                    svc.create(ctx, req)
                        .await?
                        .encode::<crate::rpc::proto::crud::v1::CreateResponse>(format)
                })
            }
            "Get" => {
                let svc = ::std::sync::Arc::clone(&self.inner);
                Box::pin(async move {
                    let body = ::connectrpc::dispatcher::codegen::request_proto_bytes::<
                        crate::rpc::proto::crud::v1::GetRequest,
                    >(request.encoded()?, format)?;
                    let req: crate::rpc::proto::crud::v1::__buffa::view::GetRequestView<
                        '_,
                    > = ::connectrpc::dispatcher::codegen::decode_borrowed_request_view(
                        &body,
                        ctx.decode_options(),
                    )?;
                    let req = ::connectrpc::ServiceRequest::<
                        crate::rpc::proto::crud::v1::GetRequest,
                    >::from_parts(&req, &body);
                    svc.get(ctx, req)
                        .await?
                        .encode::<crate::rpc::proto::crud::v1::GetResponse>(format)
                })
            }
            "List" => {
                let svc = ::std::sync::Arc::clone(&self.inner);
                Box::pin(async move {
                    let body = ::connectrpc::dispatcher::codegen::request_proto_bytes::<
                        crate::rpc::proto::crud::v1::ListRequest,
                    >(request.encoded()?, format)?;
                    let req: crate::rpc::proto::crud::v1::__buffa::view::ListRequestView<
                        '_,
                    > = ::connectrpc::dispatcher::codegen::decode_borrowed_request_view(
                        &body,
                        ctx.decode_options(),
                    )?;
                    let req = ::connectrpc::ServiceRequest::<
                        crate::rpc::proto::crud::v1::ListRequest,
                    >::from_parts(&req, &body);
                    svc.list(ctx, req)
                        .await?
                        .encode::<crate::rpc::proto::crud::v1::ListResponse>(format)
                })
            }
            "Update" => {
                let svc = ::std::sync::Arc::clone(&self.inner);
                Box::pin(async move {
                    let body = ::connectrpc::dispatcher::codegen::request_proto_bytes::<
                        crate::rpc::proto::crud::v1::UpdateRequest,
                    >(request.encoded()?, format)?;
                    let req: crate::rpc::proto::crud::v1::__buffa::view::UpdateRequestView<
                        '_,
                    > = ::connectrpc::dispatcher::codegen::decode_borrowed_request_view(
                        &body,
                        ctx.decode_options(),
                    )?;
                    let req = ::connectrpc::ServiceRequest::<
                        crate::rpc::proto::crud::v1::UpdateRequest,
                    >::from_parts(&req, &body);
                    svc.update(ctx, req)
                        .await?
                        .encode::<crate::rpc::proto::crud::v1::UpdateResponse>(format)
                })
            }
            "Delete" => {
                let svc = ::std::sync::Arc::clone(&self.inner);
                Box::pin(async move {
                    let body = ::connectrpc::dispatcher::codegen::request_proto_bytes::<
                        crate::rpc::proto::crud::v1::DeleteRequest,
                    >(request.encoded()?, format)?;
                    let req: crate::rpc::proto::crud::v1::__buffa::view::DeleteRequestView<
                        '_,
                    > = ::connectrpc::dispatcher::codegen::decode_borrowed_request_view(
                        &body,
                        ctx.decode_options(),
                    )?;
                    let req = ::connectrpc::ServiceRequest::<
                        crate::rpc::proto::crud::v1::DeleteRequest,
                    >::from_parts(&req, &body);
                    svc.delete(ctx, req)
                        .await?
                        .encode::<crate::rpc::proto::crud::v1::DeleteResponse>(format)
                })
            }
            _ => ::connectrpc::dispatcher::codegen::unimplemented_unary(path),
        }
    }
    fn call_server_streaming(
        &self,
        path: &str,
        ctx: ::connectrpc::RequestContext,
        request: ::buffa::bytes::Bytes,
        format: ::connectrpc::CodecFormat,
    ) -> ::connectrpc::dispatcher::codegen::StreamingResult {
        let Some(method) = path.strip_prefix("crud.v1.EntityService/") else {
            return ::connectrpc::dispatcher::codegen::unimplemented_streaming(path);
        };
        let _ = (&ctx, &request, &format);
        match method {
            _ => ::connectrpc::dispatcher::codegen::unimplemented_streaming(path),
        }
    }
    fn call_client_streaming(
        &self,
        path: &str,
        ctx: ::connectrpc::RequestContext,
        requests: ::connectrpc::dispatcher::codegen::RequestStream,
        format: ::connectrpc::CodecFormat,
    ) -> ::connectrpc::dispatcher::codegen::UnaryResult {
        let Some(method) = path.strip_prefix("crud.v1.EntityService/") else {
            return ::connectrpc::dispatcher::codegen::unimplemented_unary(path);
        };
        let _ = (&ctx, &requests, &format);
        match method {
            _ => ::connectrpc::dispatcher::codegen::unimplemented_unary(path),
        }
    }
    fn call_bidi_streaming(
        &self,
        path: &str,
        ctx: ::connectrpc::RequestContext,
        requests: ::connectrpc::dispatcher::codegen::RequestStream,
        format: ::connectrpc::CodecFormat,
    ) -> ::connectrpc::dispatcher::codegen::StreamingResult {
        let Some(method) = path.strip_prefix("crud.v1.EntityService/") else {
            return ::connectrpc::dispatcher::codegen::unimplemented_streaming(path);
        };
        let _ = (&ctx, &requests, &format);
        match method {
            _ => ::connectrpc::dispatcher::codegen::unimplemented_streaming(path),
        }
    }
}
/// Client for this service.
///
/// Generic over `T: ClientTransport`. For **gRPC** (HTTP/2), use
/// `Http2Connection` — it has honest `poll_ready` and composes with
/// `tower::balance` for multi-connection load balancing. For **Connect
/// over HTTP/1.1** (or unknown protocol), use `HttpClient`.
///
/// # Example (gRPC / HTTP/2)
///
/// ```rust,ignore
/// use connectrpc::client::{Http2Connection, ClientConfig};
/// use connectrpc::Protocol;
///
/// let uri: http::Uri = "http://localhost:8080".parse()?;
/// let conn = Http2Connection::connect_plaintext(uri.clone()).await?.shared(1024);
/// let config = ClientConfig::new(uri).with_protocol(Protocol::Grpc);
///
/// let client = EntityServiceClient::new(conn, config);
/// let response = client.create(request).await?;
/// ```
///
/// # Example (Connect / HTTP/1.1 or ALPN)
///
/// ```rust,ignore
/// use connectrpc::client::{HttpClient, ClientConfig};
///
/// let http = HttpClient::plaintext();  // cleartext http:// only
/// let config = ClientConfig::new("http://localhost:8080".parse()?);
///
/// let client = EntityServiceClient::new(http, config);
/// let response = client.create(request).await?;
/// ```
///
/// # Working with the response
///
/// Unary calls return [`UnaryResponse<OwnedView<FooView>>`](::connectrpc::client::UnaryResponse).
/// [`view()`](::connectrpc::client::UnaryResponse::view) borrows the response
/// message, so field access is zero-copy:
///
/// ```rust,ignore
/// let resp = client.create(request).await?;
/// let name: &str = resp.view().name;  // borrow into the response buffer
/// ```
///
/// If you need the owned struct (e.g. to store or pass by value), use
/// [`into_owned()`](::connectrpc::client::UnaryResponse::into_owned):
///
/// ```rust,ignore
/// let owned = client.create(request).await?.into_owned();
/// ```
///
/// [`into_view()`](::connectrpc::client::UnaryResponse::into_view) keeps the
/// zero-copy decoded body (an `OwnedView`) without copying; field access on it
/// goes through `.reborrow()`. Streaming responses yield one
/// [`StreamMessage`](::connectrpc::StreamMessage) per received message from
/// `.message().await` — read fields zero-copy through the generated accessor
/// methods (`msg.name()`) or `.view()`, or convert with `.to_owned_message()`.
#[derive(Clone)]
pub struct EntityServiceClient<T> {
    transport: T,
    config: ::connectrpc::client::ClientConfig,
}
impl<T> EntityServiceClient<T>
where
    T: ::connectrpc::client::ClientTransport,
    <T::ResponseBody as ::connectrpc::http_body::Body>::Error: ::std::fmt::Display,
{
    /// Create a new client with the given transport and configuration.
    pub fn new(transport: T, config: ::connectrpc::client::ClientConfig) -> Self {
        Self { transport, config }
    }
    /// Get the client configuration.
    pub fn config(&self) -> &::connectrpc::client::ClientConfig {
        &self.config
    }
    /// Get a mutable reference to the client configuration.
    pub fn config_mut(&mut self) -> &mut ::connectrpc::client::ClientConfig {
        &mut self.config
    }
    /// Call the Create RPC. Sends a request to /crud.v1.EntityService/Create.
    pub async fn create(
        &self,
        request: crate::rpc::proto::crud::v1::CreateRequest,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::CreateResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        self.create_with_options(request, ::connectrpc::client::CallOptions::default())
            .await
    }
    /// Call the Create RPC with explicit per-call options. Options override [`ClientConfig`](::connectrpc::client::ClientConfig) defaults.
    pub async fn create_with_options(
        &self,
        request: crate::rpc::proto::crud::v1::CreateRequest,
        options: ::connectrpc::client::CallOptions,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::CreateResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        ::connectrpc::client::call_unary(
                &self.transport,
                &self.config,
                ENTITY_SERVICE_CREATE_SPEC.with_origin(::connectrpc::SpecOrigin::Client),
                request,
                options,
            )
            .await
    }
    /// Call the Get RPC. Sends a request to /crud.v1.EntityService/Get.
    pub async fn get(
        &self,
        request: crate::rpc::proto::crud::v1::GetRequest,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::GetResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        self.get_with_options(request, ::connectrpc::client::CallOptions::default())
            .await
    }
    /// Call the Get RPC with explicit per-call options. Options override [`ClientConfig`](::connectrpc::client::ClientConfig) defaults.
    pub async fn get_with_options(
        &self,
        request: crate::rpc::proto::crud::v1::GetRequest,
        options: ::connectrpc::client::CallOptions,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::GetResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        ::connectrpc::client::call_unary(
                &self.transport,
                &self.config,
                ENTITY_SERVICE_GET_SPEC.with_origin(::connectrpc::SpecOrigin::Client),
                request,
                options,
            )
            .await
    }
    /// Call the List RPC. Sends a request to /crud.v1.EntityService/List.
    pub async fn list(
        &self,
        request: crate::rpc::proto::crud::v1::ListRequest,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::ListResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        self.list_with_options(request, ::connectrpc::client::CallOptions::default())
            .await
    }
    /// Call the List RPC with explicit per-call options. Options override [`ClientConfig`](::connectrpc::client::ClientConfig) defaults.
    pub async fn list_with_options(
        &self,
        request: crate::rpc::proto::crud::v1::ListRequest,
        options: ::connectrpc::client::CallOptions,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::ListResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        ::connectrpc::client::call_unary(
                &self.transport,
                &self.config,
                ENTITY_SERVICE_LIST_SPEC.with_origin(::connectrpc::SpecOrigin::Client),
                request,
                options,
            )
            .await
    }
    /// Call the Update RPC. Sends a request to /crud.v1.EntityService/Update.
    pub async fn update(
        &self,
        request: crate::rpc::proto::crud::v1::UpdateRequest,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::UpdateResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        self.update_with_options(request, ::connectrpc::client::CallOptions::default())
            .await
    }
    /// Call the Update RPC with explicit per-call options. Options override [`ClientConfig`](::connectrpc::client::ClientConfig) defaults.
    pub async fn update_with_options(
        &self,
        request: crate::rpc::proto::crud::v1::UpdateRequest,
        options: ::connectrpc::client::CallOptions,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::UpdateResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        ::connectrpc::client::call_unary(
                &self.transport,
                &self.config,
                ENTITY_SERVICE_UPDATE_SPEC.with_origin(::connectrpc::SpecOrigin::Client),
                request,
                options,
            )
            .await
    }
    /// Call the Delete RPC. Sends a request to /crud.v1.EntityService/Delete.
    pub async fn delete(
        &self,
        request: crate::rpc::proto::crud::v1::DeleteRequest,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::DeleteResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        self.delete_with_options(request, ::connectrpc::client::CallOptions::default())
            .await
    }
    /// Call the Delete RPC with explicit per-call options. Options override [`ClientConfig`](::connectrpc::client::ClientConfig) defaults.
    pub async fn delete_with_options(
        &self,
        request: crate::rpc::proto::crud::v1::DeleteRequest,
        options: ::connectrpc::client::CallOptions,
    ) -> Result<
        ::connectrpc::client::UnaryResponse<
            ::buffa::view::OwnedView<
                crate::rpc::proto::crud::v1::__buffa::view::DeleteResponseView<'static>,
            >,
        >,
        ::connectrpc::ConnectError,
    > {
        ::connectrpc::client::call_unary(
                &self.transport,
                &self.config,
                ENTITY_SERVICE_DELETE_SPEC.with_origin(::connectrpc::SpecOrigin::Client),
                request,
                options,
            )
            .await
    }
}
