//! Port of go/runtime/policy's clierror_test.go: a denial renders as
//! the CONFLICT envelope (exit 4, permanent) naming the rule.
#![cfg(all(feature = "policy", feature = "output"))]

mod policy_support;

use hop_top_kit::output::{render_error, CliError, CODE_CONFLICT, TRANSIENCE_PERMANENT};
use hop_top_kit::policy::{Engine, PolicyDeniedError};
use policy_support::cfg;
use serde_json::Map;

fn denial() -> PolicyDeniedError {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: admin-only-cancel
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "only admins may cancel"
"#))
    .unwrap();
    eng.decide("kit.runtime.entity.pre_validated", Map::new())
        .unwrap_err()
}

#[test]
fn policy_denial_renders_conflict_envelope() {
    let env = denial().to_cli_error();
    assert_eq!(env.code, CODE_CONFLICT, "CONFLICT, not the generic bucket");
    assert_eq!(
        env.exit_code, 4,
        "exit 4; 1 would mean the denial was not recognised"
    );
    assert_eq!(env.transience, TRANSIENCE_PERMANENT);
    assert!(env.message.contains("only admins may cancel"));
    assert!(
        env.cause.contains("admin-only-cancel"),
        "names the rule: {}",
        env.cause
    );
    assert!(env.cause.contains("kit.runtime.entity.pre_validated"));
    assert!(env.suggested_fix.contains("admin-only-cancel"));

    let via_from: CliError = (&denial()).into();
    assert_eq!(via_from, env);

    let mut out = Vec::new();
    render_error(&mut out, "json", &env).unwrap();
    let got: CliError = serde_json::from_slice(&out).unwrap();
    assert_eq!(got, env);
}
