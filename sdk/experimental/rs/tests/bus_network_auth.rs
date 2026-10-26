//! The auth handshake: Go's `network_auth_test.go`
//! (`TestNetworkAuth_ValidToken`, `_InvalidToken`, `_MissingClientAuth`,
//! `TestStaticTokenAuth_Verify`), plus the error each refusal returns.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::atomic::Ordering;

use bus_network_support::*;
use hop_top_kit::bus::network::{Authenticator, NetworkOptions, OpenAuth, StaticTokenAuth};
use serde_json::Value;

#[test]
fn network_auth_valid_token() {
    let auth = StaticTokenAuth::new("secret123");
    let a = Hub::start(NetworkOptions::new().origin_id("A").auth(auth.clone()));
    let b = Peer::new(NetworkOptions::new().origin_id("B").auth(auth));
    b.connect(&a.url).expect("connect with valid auth");

    let received = flag_on(&a.bus, "auth.test");
    sleep_ms(50);
    publish(&b.bus, ev("auth.test", "B", Value::Null));
    wait_for(&received, "timed out waiting for event after auth");
}

#[test]
fn network_auth_invalid_token() {
    let a = Hub::start(
        NetworkOptions::new()
            .origin_id("A")
            .auth(StaticTokenAuth::new("correct")),
    );
    let b = Peer::new(
        NetworkOptions::new()
            .origin_id("B")
            .auth(StaticTokenAuth::new("wrong")),
    );
    // Go lets Connect succeed or fail here; this port surfaces the
    // refusal, as Go's current adapter does.
    let err = b.connect(&a.url).expect_err("wrong token must be refused");
    assert!(err.is_auth_failed(), "{err}");
    assert_eq!(err.to_string(), "bus: network auth failed");

    let received = flag_on(&a.bus, "auth.fail");
    sleep_ms(100);
    publish(&b.bus, ev("auth.fail", "B", Value::Null));
    sleep_ms(300);
    assert!(
        !received.load(Ordering::SeqCst),
        "event should not be delivered with invalid auth"
    );
}

#[test]
fn network_auth_missing_client_auth() {
    let a = Hub::start(
        NetworkOptions::new()
            .origin_id("A")
            .auth(StaticTokenAuth::new("secret")),
    );
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    let _ = b.connect(&a.url);

    let received = flag_on(&a.bus, "noauth.test");
    sleep_ms(100);
    publish(&b.bus, ev("noauth.test", "B", Value::Null));
    sleep_ms(300);
    assert!(
        !received.load(Ordering::SeqCst),
        "event should not be delivered without auth"
    );
}

#[test]
fn static_token_auth_verify() {
    let auth = StaticTokenAuth::new("abc");
    assert!(auth.verify("abc").is_ok(), "valid token rejected");
    let err = auth
        .verify("xyz")
        .expect_err("invalid token should be rejected");
    assert_eq!(err.to_string(), "bus: network auth failed");
    assert_eq!(auth.token().unwrap(), "abc");
    assert!(
        !format!("{auth:?}").contains("abc"),
        "Debug must not print the secret"
    );
}

#[test]
fn open_auth_sends_open_and_accepts_any_non_empty_token() {
    assert_eq!(OpenAuth.token().unwrap(), "open");
    assert!(OpenAuth.verify("anything").is_ok());
    assert!(OpenAuth.verify("").is_err());

    let a = Hub::start(NetworkOptions::new().origin_id("A").auth(OpenAuth));
    let b = Peer::new(
        NetworkOptions::new()
            .origin_id("B")
            .auth(StaticTokenAuth::new("whatever")),
    );
    b.connect(&a.url).expect("open auth accepts any token");
}

#[test]
fn a_token_error_is_returned_unchanged() {
    struct Broken;
    impl Authenticator for Broken {
        fn token(&self) -> Result<String, hop_top_kit::bus::network::AuthError> {
            Err("vault sealed".into())
        }
        fn verify(&self, _: &str) -> Result<(), hop_top_kit::bus::network::AuthError> {
            Ok(())
        }
    }
    let a = Hub::start(NetworkOptions::new().origin_id("A").auth(OpenAuth));
    let b = Peer::new(NetworkOptions::new().origin_id("B").auth(Broken));
    let err = b.connect(&a.url).expect_err("token error");
    assert_eq!(err.to_string(), "vault sealed");
    assert!(!err.is_auth_failed());
}
