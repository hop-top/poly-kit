//! Bus wiring and the activation built from an event. Go's
//! subscriber.go.

use std::sync::Arc;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value as Json};

use crate::bus::{BusError, Context, DynBus, Event, Handler, SharedBus, SubscriptionId};

use super::config::{
    TOPIC_ENTITY_PRE_PERSISTED, TOPIC_ENTITY_PRE_VALIDATED, TOPIC_STATE_PRE_TRANSITIONED,
};
use super::engine::{ContextAttrs, Engine};
use super::error::PolicyDeniedError;
use super::eval::Activation;

/// The operation on a [`PreEntityPayload`]. Go's `domain.Op`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Op {
    /// A new entity.
    Create,
    /// A changed entity.
    Update,
    /// A removed entity.
    Delete,
}

/// Which pre-event seam fired. Go's `domain.Phase`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Phase {
    /// Raw input, before validation.
    PreValidated,
    /// The validated entity, before the repository write.
    PrePersisted,
}

/// The payload of the entity pre-events, as Go's `domain.PreEntityPayload`
/// marshals it (the Go field names, no json tags). The Rust `domain`
/// module ports the state machine only; the entity service that
/// publishes these is not ported, so whatever publishes the entity
/// topics builds this and puts [`to_value`](Self::to_value) in
/// [`Event::payload`].
///
/// `entity` is the raw input at [`Phase::PreValidated`] and the
/// validated entity at [`Phase::PrePersisted`]; absent for a delete,
/// which carries only `entity_id`.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct PreEntityPayload {
    /// The operation.
    pub op: Op,
    /// The seam.
    pub phase: Phase,
    /// The target id.
    #[serde(rename = "EntityID")]
    pub entity_id: String,
    /// The entity, as JSON.
    #[serde(default)]
    pub entity: Option<Json>,
}

impl PreEntityPayload {
    /// The JSON an [`Event`] carries.
    pub fn to_value(&self) -> Json {
        // Strings, enums and a Value: serialization cannot fail.
        serde_json::to_value(self).unwrap_or(Json::Null)
    }
}

/// A bus the engine can be subscribed to: [`SharedBus`] and [`DynBus`].
pub trait PolicyBus: Clone + Send + Sync + 'static {
    /// Subscribes `handler` to `pattern`.
    fn subscribe_policy(&self, pattern: &str, handler: Handler) -> SubscriptionId;
    /// Removes a subscription.
    fn unsubscribe_policy(&self, id: SubscriptionId) -> bool;
}

impl PolicyBus for SharedBus {
    fn subscribe_policy(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.subscribe_ctx(pattern, handler)
    }
    fn unsubscribe_policy(&self, id: SubscriptionId) -> bool {
        self.unsubscribe(id)
    }
}

impl PolicyBus for DynBus {
    fn subscribe_policy(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.subscribe_ctx(pattern, handler)
    }
    fn unsubscribe_policy(&self, id: SubscriptionId) -> bool {
        self.unsubscribe(id)
    }
}

/// Subscribes `engine` to the three veto-able topics on `bus`. A
/// denial vetoes the publish: the publisher gets a
/// [`BusError::Handler`] carrying the denial's text. Go's `Wire`.
///
/// The bus error holds text, not the typed [`PolicyDeniedError`] Go's
/// `errors.As` reaches through the bus; call [`Engine::decide`] where
/// the type matters.
pub fn wire<B: PolicyBus>(bus: &B, engine: Arc<Engine>) -> Unwire<B> {
    let subscribe = |topic: &str| {
        let engine = Arc::clone(&engine);
        bus.subscribe_policy(
            topic,
            Box::new(move |ctx, ev| {
                engine
                    .on_event(ctx, ev)
                    .map_err(|d| BusError::handler(d.to_string()))
            }),
        )
    };
    Unwire {
        bus: bus.clone(),
        ids: vec![
            subscribe(TOPIC_STATE_PRE_TRANSITIONED),
            subscribe(TOPIC_ENTITY_PRE_VALIDATED),
            subscribe(TOPIC_ENTITY_PRE_PERSISTED),
        ],
    }
}

/// Removes the subscriptions [`wire`] made. Go returns a func; dropping
/// this does not unsubscribe, call [`unwire`](Self::unwire).
pub struct Unwire<B: PolicyBus> {
    bus: B,
    ids: Vec<SubscriptionId>,
}

impl<B: PolicyBus> Unwire<B> {
    /// Unsubscribes all three handlers.
    pub fn unwire(self) {
        for id in self.ids {
            self.bus.unsubscribe_policy(id);
        }
    }
}

impl Engine {
    /// The handler for [`TOPIC_STATE_PRE_TRANSITIONED`]. Go's
    /// `OnTransition`.
    pub fn on_transition(&self, ctx: &Context, ev: &Event) -> Result<(), PolicyDeniedError> {
        self.on_event(ctx, ev)
    }

    /// The handler for the entity pre-events. Go's `OnEntityChange`.
    pub fn on_entity_change(&self, ctx: &Context, ev: &Event) -> Result<(), PolicyDeniedError> {
        self.on_event(ctx, ev)
    }

    /// Builds the activation from `ctx` and `ev`, then decides on the
    /// event's topic.
    pub fn on_event(&self, ctx: &Context, ev: &Event) -> Result<(), PolicyDeniedError> {
        self.decide(ev.topic.as_str(), self.activation(ctx, ev))
    }

    /// The activation Go's subscriber builds: `principal`, `resource`,
    /// `entity`, `context` and `payload`.
    pub fn activation(&self, ctx: &Context, ev: &Event) -> Activation {
        let princ = (self.princ)(ctx);
        let pload = payload_as_map(&ev.payload);
        let entity_payload = pre_entity(&ev.payload);
        let resource = resource_from_payload(entity_payload.as_ref(), &pload);
        let entity = entity_from_payload(entity_payload.as_ref(), &pload);

        let mut act = Activation::new();
        act.insert(
            "principal".into(),
            serde_json::json!({"id": princ.id, "role": princ.role, "source": princ.source}),
        );
        act.insert("resource".into(), Json::Object(resource));
        act.insert("entity".into(), Json::Object(entity));
        act.insert("context".into(), Json::Object(context_attrs(ctx)));
        act.insert("payload".into(), Json::Object(pload));
        act
    }
}

/// Go's `payloadAsMap`: an object as is, anything else (including
/// null) as an empty map.
fn payload_as_map(p: &Json) -> Map<String, Json> {
    match p {
        Json::Object(m) => m.clone(),
        _ => Map::new(),
    }
}

/// A payload shaped like [`PreEntityPayload`]: Go branches on the
/// struct type, which a JSON payload has lost, so the shape stands in
/// for it. `Op` and `Phase` are matched as strings so an unknown value
/// still takes the entity branch, as a Go struct would.
struct PreEntity {
    op: String,
    id: String,
    entity: Option<Json>,
}

fn pre_entity(p: &Json) -> Option<PreEntity> {
    let m = p.as_object()?;
    let op = m.get("Op")?.as_str()?.to_string();
    m.get("Phase")?.as_str()?;
    let id = m.get("EntityID")?.as_str()?.to_string();
    let entity = match m.get("Entity") {
        None | Some(Json::Null) => None,
        Some(e) => Some(e.clone()),
    };
    Some(PreEntity { op, id, entity })
}

fn str_of(m: &Map<String, Json>, key: &str) -> Option<String> {
    m.get(key).and_then(Json::as_str).map(str::to_string)
}

/// First string found under `keys`, `""` when none.
fn first_str(m: &Map<String, Json>, keys: &[&str]) -> String {
    keys.iter().find_map(|k| str_of(m, k)).unwrap_or_default()
}

fn entity_from_payload(pe: Option<&PreEntity>, m: &Map<String, Json>) -> Map<String, Json> {
    let mut out = Map::new();
    let (op, kind, track_type);
    if let Some(pe) = pe {
        op = pe.op.clone();
        let fields = pe.entity.as_ref().map(payload_as_map);
        kind = fields
            .as_ref()
            .map(|f| first_str(f, &["Kind", "kind"]))
            .unwrap_or_default();
        track_type = fields
            .as_ref()
            .map(|f| first_str(f, &["TrackType", "track_type", "Type"]))
            .unwrap_or_default();
    } else {
        op = first_str(m, &["op", "Op"]);
        kind = first_str(m, &["kind", "Kind"]);
        track_type = str_of(m, "track_type").unwrap_or_default();
    }
    out.insert("kind".into(), Json::String(kind));
    out.insert("op".into(), Json::String(op));
    out.insert("track_type".into(), Json::String(track_type));
    out
}

fn resource_from_payload(pe: Option<&PreEntity>, m: &Map<String, Json>) -> Map<String, Json> {
    let (id, kind, fields) = if let Some(pe) = pe {
        let fields = pe.entity.as_ref().map(payload_as_map).unwrap_or_default();
        (pe.id.clone(), first_str(&fields, &["Kind", "kind"]), fields)
    } else {
        (
            first_str(m, &["EntityID", "id"]),
            first_str(m, &["Kind", "kind"]),
            m.clone(),
        )
    };
    let mut out = Map::new();
    out.insert("id".into(), Json::String(id));
    out.insert("kind".into(), Json::String(kind));
    out.insert("fields".into(), Json::Object(fields));
    out
}

fn context_attrs(ctx: &Context) -> Map<String, Json> {
    let attrs = ctx.value::<ContextAttrs>().cloned().unwrap_or_default();
    let mut out = Map::new();
    out.insert("note".into(), Json::String(attrs.note));
    out.insert("request_attrs".into(), Json::Object(attrs.request_attrs));
    out
}
