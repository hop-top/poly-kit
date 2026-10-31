//! `default_principal_resolver` env cases. Own file: it mutates the
//! process environment, so one test owns both variables.
#![cfg(all(feature = "policy", not(target_arch = "wasm32")))]

use hop_top_kit::bus::Context;
use hop_top_kit::policy::default_principal_resolver;

#[test]
fn default_principal_resolver_env() {
    // SAFETY: this file holds the only test, so nothing reads the
    // environment concurrently.
    unsafe {
        std::env::set_var("KIT_POLICY_ROLE", "lead");
        std::env::set_var("USER", "alice");
    }
    let got = default_principal_resolver(&Context::background());
    assert_eq!(got.id, "alice");
    assert_eq!(got.role, "lead");
    assert_eq!(got.source, "env");

    unsafe {
        std::env::set_var("KIT_POLICY_ROLE", "");
        std::env::set_var("USER", "");
    }
    let got = default_principal_resolver(&Context::background());
    assert_eq!(got.role, "");
    assert_eq!(got.source, "none");
}
