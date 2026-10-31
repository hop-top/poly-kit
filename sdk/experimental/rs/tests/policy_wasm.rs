//! wasm32-unknown-unknown smoke test for the policy engine, run under
//! Node by wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features policy --test policy_wasm
//! ```
//!
//! Rules parse, CEL compiles and evaluates, and a denial vetoes a
//! transition through the bus, with no filesystem, environment or
//! clock.

#![cfg(all(feature = "policy", target_arch = "wasm32", target_os = "unknown"))]

use std::sync::Arc;
use std::time::SystemTime;

use hop_top_kit::bus::{Context, DynBus, Event};
use hop_top_kit::policy::{parse_config, stage_config, wire, with_principal, Engine, Principal};
use serde_json::json;
use wasm_bindgen_test::wasm_bindgen_test;

const RULES: &str = r#"policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'payload.To == "CANCELED" && principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: only admin may cancel
"#;

fn transition() -> Event {
    Event::new_at(
        "kit.runtime.state.pre_transitioned",
        "domain.statemachine",
        json!({"From": "OPEN", "To": "CANCELED", "Force": false}),
        SystemTime::UNIX_EPOCH,
    )
}

fn as_role(role: &str) -> Context {
    with_principal(
        &Context::background(),
        Principal {
            role: role.into(),
            ..Principal::default()
        },
    )
}

#[wasm_bindgen_test]
fn denial_vetoes_through_the_bus() {
    let engine = Arc::new(Engine::with_cel(&parse_config(RULES.as_bytes()).unwrap()).unwrap());
    let bus = DynBus::builder().build();
    let unwire = wire(&bus, engine);

    let err = bus
        .publish_ctx(&as_role("user"), &transition())
        .unwrap_err();
    assert!(err.to_string().contains("only admin may cancel"));
    bus.publish_ctx(&as_role("admin"), &transition()).unwrap();
    unwire.unwire();
    bus.publish_ctx(&as_role("user"), &transition()).unwrap();
}

#[wasm_bindgen_test]
fn shipped_stage_rules_compile() {
    Engine::with_cel(&stage_config()).unwrap();
}
