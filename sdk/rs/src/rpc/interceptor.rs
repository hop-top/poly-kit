//! The kit-owned interceptor port and its adapter onto connectrpc's.
//!
//! One method wraps a call, unary or streaming (at stream establishment
//! for the latter): the shape Go's `connect.Interceptor` gives
//! `Authenticate`. connectrpc's own interceptor, which sees payloads and
//! response bodies, stays inside this module.

use std::future::Future;
use std::pin::Pin;
use std::sync::{Arc, Mutex, PoisonError};

use connectrpc::interceptor::{
    Interceptor as ConnectInterceptor, Next as ConnectNext, NextStream, PayloadStream,
    StreamRequest, StreamResponse, UnaryRequest, UnaryResponse,
};
use connectrpc::{async_trait, ConnectError, ErrorCode};

use super::call::Call;
use super::error::RpcError;

/// A boxed future an interceptor returns.
pub type RpcFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// Wraps every call. Call `next.run(call)` to continue; returning
/// without it short-circuits (the handler never runs). The first
/// registered runs outermost.
///
/// `Ok(())` from `next.run` means the handler ran and succeeded (for a
/// stream: it was established). An `Err` is the call's failure.
pub trait Interceptor: Send + Sync + 'static {
    fn intercept<'a>(&'a self, call: Call, next: Next<'a>) -> RpcFuture<'a, Result<(), RpcError>>;
}

type Terminal<'a> = Box<dyn FnOnce(Call) -> RpcFuture<'a, Result<(), RpcError>> + Send + 'a>;

/// The rest of the chain.
pub struct Next<'a>(Terminal<'a>);

impl Next<'_> {
    /// Run the rest of the chain and the handler.
    pub async fn run(self, call: Call) -> Result<(), RpcError> {
        (self.0)(call).await
    }
}

fn run_chain<'a>(
    chain: &'a [Arc<dyn Interceptor>],
    call: Call,
    terminal: Terminal<'a>,
) -> RpcFuture<'a, Result<(), RpcError>> {
    match chain.split_first() {
        None => terminal(call),
        Some((head, tail)) => {
            head.intercept(call, Next(Box::new(move |c| run_chain(tail, c, terminal))))
        }
    }
}

/// connectrpc's interceptor running the kit chain around its `next`.
pub(crate) struct Adapter {
    pub(crate) chain: Arc<[Arc<dyn Interceptor>]>,
}

type Slot<T> = Arc<Mutex<Option<Result<T, ConnectError>>>>;

fn settle<T>(verdict: Result<(), RpcError>, slot: Slot<T>) -> Result<T, ConnectError> {
    let ran = slot.lock().unwrap_or_else(PoisonError::into_inner).take();
    match (verdict, ran) {
        // The handler's own error keeps its details.
        (_, Some(Err(e))) => Err(e),
        (Ok(()), Some(Ok(resp))) => Ok(resp),
        // An interceptor failed the call after the handler answered, or
        // before it ran: the answer, if any, is discarded.
        (Err(e), _) => Err(e.into()),
        (Ok(()), None) => Err(ConnectError::new(
            ErrorCode::Internal,
            "interceptor ended the call without running the handler",
        )),
    }
}

#[async_trait]
impl ConnectInterceptor for Adapter {
    async fn intercept_unary(
        &self,
        mut req: UnaryRequest,
        next: ConnectNext<'_>,
    ) -> Result<UnaryResponse, ConnectError> {
        let call = Call::from_context(&req.ctx);
        let slot: Slot<UnaryResponse> = Arc::default();
        let out = slot.clone();
        let verdict = run_chain(
            &self.chain,
            call,
            Box::new(move |call| {
                Box::pin(async move {
                    call.apply(&mut req.ctx);
                    let r = next.run(req).await;
                    let status = r.as_ref().map(|_| ()).map_err(RpcError::from);
                    *out.lock().unwrap_or_else(PoisonError::into_inner) = Some(r);
                    status
                })
            }),
        )
        .await;
        settle(verdict, slot)
    }

    async fn intercept_streaming(
        &self,
        mut req: StreamRequest,
        inbound: PayloadStream,
        next: NextStream<'_>,
    ) -> Result<StreamResponse, ConnectError> {
        let call = Call::from_context(&req.ctx);
        let slot: Slot<StreamResponse> = Arc::default();
        let out = slot.clone();
        let verdict = run_chain(
            &self.chain,
            call,
            Box::new(move |call| {
                Box::pin(async move {
                    call.apply(&mut req.ctx);
                    let r = next.run(req, inbound).await;
                    let status = r.as_ref().map(|_| ()).map_err(RpcError::from);
                    *out.lock().unwrap_or_else(PoisonError::into_inner) = Some(r);
                    status
                })
            }),
        )
        .await;
        settle(verdict, slot)
    }
}
