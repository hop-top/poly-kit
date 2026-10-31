//! Feature leaks into consumers.
//!
//! Cargo unifies features across a build, so a feature this crate turns
//! on for one of its dependencies is turned on for every crate in the
//! consumer's tree. `serde_json/preserve_order` swaps `Map` from a
//! `BTreeMap` to an `IndexMap`, which changes key order everywhere; only
//! the `output` feature needs it.

#[cfg(all(feature = "bus", not(feature = "output")))]
#[test]
fn bus_does_not_enable_preserve_order() {
    let v: serde_json::Value = serde_json::from_str(r#"{"b":1,"a":2}"#).unwrap();
    let keys: Vec<&str> = v.as_object().unwrap().keys().map(String::as_str).collect();
    assert_eq!(keys, ["a", "b"], "serde_json/preserve_order is enabled");
}

#[cfg(feature = "output")]
#[test]
fn output_enables_preserve_order() {
    let v: serde_json::Value = serde_json::from_str(r#"{"b":1,"a":2}"#).unwrap();
    let keys: Vec<&str> = v.as_object().unwrap().keys().map(String::as_str).collect();
    assert_eq!(keys, ["b", "a"], "serde_json/preserve_order is disabled");
}
