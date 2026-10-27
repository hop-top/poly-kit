//! [`ConfigGetter`](crate::bus::ConfigGetter) for config views, so the
//! bus reads its enforcement mode (`kit.bus.enforce`) from real config:
//! the viper-style [`EnvLookup`] and a loaded [`Value`] tree.

use super::env::cast_string;
use super::{EnvLookup, Value};

impl crate::bus::ConfigGetter for EnvLookup {
    fn get(&self, key: &str) -> Option<String> {
        self.get_value(key).map(|v| cast_string(&v))
    }
}

/// A loaded value tree answers dotted keys (scalars only, cast like
/// viper's `GetString`).
impl crate::bus::ConfigGetter for Value {
    fn get(&self, key: &str) -> Option<String> {
        match self.lookup(key)? {
            Value::Seq(_) | Value::Map(_) => None,
            v => Some(cast_string(v)),
        }
    }
}
