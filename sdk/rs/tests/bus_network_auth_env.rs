//! `auth_from_env`: Go's `TestGap_BusAuthFromEnv_Missing`. Alone in its
//! binary: it mutates the process environment.

#![cfg(feature = "bus-network")]

use hop_top_kit::bus::network::{auth_from_env, Authenticator, StaticTokenAuth};

const NAMES: [&str; 2] = ["DPKMS_BUS_TOKEN", "BUS_TOKEN"];

#[test]
fn gap_bus_auth_from_env_missing() {
    // first var found
    std::env::set_var("DPKMS_BUS_TOKEN", "tok-1");
    std::env::set_var("BUS_TOKEN", "tok-fallback");
    let auth = auth_from_env(&NAMES).expect("first var found");
    assert_eq!(auth.token().unwrap(), "tok-1");

    // fallback to second
    std::env::set_var("DPKMS_BUS_TOKEN", "");
    let auth = auth_from_env(&NAMES).expect("fallback to second");
    assert_eq!(auth.token().unwrap(), "tok-fallback");

    // all empty returns none
    std::env::set_var("BUS_TOKEN", "");
    assert!(auth_from_env(&NAMES).is_none(), "all empty returns none");
    std::env::remove_var("BUS_TOKEN");
    std::env::remove_var("DPKMS_BUS_TOKEN");
    assert!(auth_from_env(&NAMES).is_none(), "all unset returns none");

    // pin: the shape the helper returns.
    let _ = StaticTokenAuth::new("_");
}
