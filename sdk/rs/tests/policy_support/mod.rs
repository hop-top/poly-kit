//! Helpers shared by the `policy*.rs` tests: Go's `busAdapter`, the
//! in-memory repository and a minimal entity service that publishes the
//! pre-events the way `domain.Service` does (the Rust `domain` module
//! ports the state machine only).

#![allow(dead_code)]

use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::{Context, DynBus, Event};
use hop_top_kit::domain::{EventPublisher, Payload, PublishError};
use hop_top_kit::policy::{
    parse_config, Config, Engine, EngineBuilder, Op, Phase, PreEntityPayload, Principal,
    TOPIC_ENTITY_PRE_PERSISTED, TOPIC_ENTITY_PRE_VALIDATED,
};
use serde_json::{json, Value};

/// `go/runtime/policy`, the canonical rule files and ruleset.
pub fn go_policy_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../go/runtime/policy")
}

pub fn load_go(rel: &str) -> Config {
    let path = go_policy_dir().join(rel);
    hop_top_kit::policy::load_config(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display()))
}

pub fn cfg(yaml: &str) -> Config {
    parse_config(yaml.as_bytes()).unwrap()
}

/// Go's `staticPrincipal`.
pub fn with_static_principal(b: EngineBuilder, p: Principal) -> EngineBuilder {
    b.principal_resolver(move |_| p.clone())
}

pub fn principal(id: &str, role: &str, source: &str) -> Principal {
    Principal {
        id: id.into(),
        role: role.into(),
        source: source.into(),
    }
}

/// A CEL engine over `cfg` whose principal is fixed.
pub fn engine_as(cfg: &Config, p: Principal) -> Arc<Engine> {
    Arc::new(
        with_static_principal(Engine::builder().cel(), p)
            .build(cfg)
            .unwrap(),
    )
}

/// An empty activation of the shape the subscriber builds.
pub fn bare_activation() -> serde_json::Map<String, Value> {
    json!({
        "principal": {"id": "", "role": "", "source": "none"},
        "resource": {},
        "context": {"note": "", "request_attrs": {}},
        "payload": {},
    })
    .as_object()
    .unwrap()
    .clone()
}

/// Go's `busAdapter`: a [`DynBus`] as the state machine's publisher.
pub struct BusAdapter(pub DynBus);

impl EventPublisher for BusAdapter {
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        payload: &Payload,
    ) -> Result<(), PublishError> {
        let ev = Event::new(topic, source, serde_json::to_value(payload)?);
        Ok(self.0.publish_ctx(ctx, &ev)?)
    }
}

/// Go's `memTaskRepo`.
#[derive(Default)]
pub struct MemRepo {
    pub created: Mutex<Vec<Value>>,
    pub updated: Mutex<Vec<Value>>,
    pub deleted: Mutex<Vec<String>>,
}

/// The slice of `domain.Service` the policy tests use: pre-validated,
/// then pre-persisted, then the repository; a veto aborts before it.
pub struct Service {
    pub bus: DynBus,
    pub repo: Arc<MemRepo>,
}

impl Service {
    pub fn new(bus: &DynBus) -> Self {
        Service {
            bus: bus.clone(),
            repo: Arc::new(MemRepo::default()),
        }
    }

    fn pre(
        &self,
        ctx: &Context,
        op: Op,
        phase: Phase,
        id: &str,
        entity: Option<Value>,
    ) -> Result<(), hop_top_kit::bus::BusError> {
        let topic = match phase {
            Phase::PreValidated => TOPIC_ENTITY_PRE_VALIDATED,
            Phase::PrePersisted => TOPIC_ENTITY_PRE_PERSISTED,
        };
        let payload = PreEntityPayload {
            op,
            phase,
            entity_id: id.into(),
            entity,
        };
        self.bus.publish_ctx(
            ctx,
            &Event::new(topic, "domain.service", payload.to_value()),
        )
    }

    pub fn create(&self, ctx: &Context, entity: Value) -> Result<(), hop_top_kit::bus::BusError> {
        let id = entity["ID"].as_str().unwrap_or_default().to_string();
        self.pre(
            ctx,
            Op::Create,
            Phase::PreValidated,
            &id,
            Some(entity.clone()),
        )?;
        self.pre(
            ctx,
            Op::Create,
            Phase::PrePersisted,
            &id,
            Some(entity.clone()),
        )?;
        self.repo.created.lock().unwrap().push(entity);
        Ok(())
    }

    pub fn update(&self, ctx: &Context, entity: Value) -> Result<(), hop_top_kit::bus::BusError> {
        let id = entity["ID"].as_str().unwrap_or_default().to_string();
        self.pre(
            ctx,
            Op::Update,
            Phase::PreValidated,
            &id,
            Some(entity.clone()),
        )?;
        self.pre(
            ctx,
            Op::Update,
            Phase::PrePersisted,
            &id,
            Some(entity.clone()),
        )?;
        self.repo.updated.lock().unwrap().push(entity);
        Ok(())
    }

    pub fn delete(&self, ctx: &Context, id: &str) -> Result<(), hop_top_kit::bus::BusError> {
        self.pre(ctx, Op::Delete, Phase::PreValidated, id, None)?;
        self.pre(ctx, Op::Delete, Phase::PrePersisted, id, None)?;
        self.repo.deleted.lock().unwrap().push(id.into());
        Ok(())
    }
}
