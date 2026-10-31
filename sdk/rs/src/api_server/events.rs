//! One event pair per request on the bus, a port of Go's
//! `WithEventPublisher`.

use std::sync::Arc;
use std::time::Instant;

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::Response;
use serde_json::{json, Value};

use super::error::ApiServerError;
use crate::bus::{prefix_topics, validate_topic, Event, SharedBus, Topic};

/// The event source for request events.
pub const SOURCE: &str = "api.router";

/// The sink the request events go to. A publish failure is swallowed
/// by the caller: events are observability and never fail a request.
pub trait EventPublisher: Send + Sync {
    /// Publishes one event.
    fn publish(&self, topic: &str, source: &str, payload: Value);
}

impl EventPublisher for SharedBus {
    fn publish(&self, topic: &str, source: &str, payload: Value) {
        let _ = SharedBus::publish(self, &Event::new(topic, source, payload));
    }
}

/// The topics a request publishes to.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Topics {
    /// Published when a request starts (default
    /// `kit.api.request.started`).
    pub request_start: String,
    /// Published when it ends (default `kit.api.request.ended`).
    pub request_end: String,
}

impl Default for Topics {
    fn default() -> Self {
        Self {
            request_start: "kit.api.request.started".into(),
            request_end: "kit.api.request.ended".into(),
        }
    }
}

impl Topics {
    /// `<prefix>.started` and `<prefix>.ended` for a 3-segment prefix
    /// (`source.category.object`).
    pub fn with_prefix(prefix: &str) -> Result<Self, ApiServerError> {
        let (map, err) = prefix_topics(prefix, &["started", "ended"]);
        if let Some(e) = err {
            return Err(ApiServerError(format!("api topic prefix {prefix:?}: {e}")));
        }
        Ok(Self {
            request_start: map["started"].as_str().to_string(),
            request_end: map["ended"].as_str().to_string(),
        })
    }

    /// Explicit topics; each must pass the bus topic grammar.
    pub fn new(start: &str, end: &str) -> Result<Self, ApiServerError> {
        for (what, t) in [("request start", start), ("request end", end)] {
            validate_topic(&Topic::from(t))
                .map_err(|e| ApiServerError(format!("api {what} topic: {e}")))?;
        }
        Ok(Self {
            request_start: start.into(),
            request_end: end.into(),
        })
    }
}

#[derive(Clone)]
pub(crate) struct EventState {
    pub(crate) publisher: Arc<dyn EventPublisher>,
    pub(crate) topics: Topics,
}

pub(crate) async fn events(State(st): State<EventState>, req: Request, next: Next) -> Response {
    let start = Instant::now();
    let method = req.method().to_string();
    let path = req.uri().path().to_string();
    st.publisher.publish(
        &st.topics.request_start,
        SOURCE,
        json!({"method": method, "path": path}),
    );
    let resp = next.run(req).await;
    st.publisher.publish(
        &st.topics.request_end,
        SOURCE,
        json!({
            "method": method,
            "path": path,
            "status": resp.status().as_u16(),
            "duration": format!("{}ms", start.elapsed().as_millis()),
        }),
    );
    resp
}
