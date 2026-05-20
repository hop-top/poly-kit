//! Built-in formatter registration. Phase-1 ships json + yaml; table /
//! csv / text land in a follow-up once renderers are in place.

use std::sync::Arc;

use super::registry::Registry;

mod json;
mod yaml;

pub use json::JsonFormatter;
pub use yaml::YamlFormatter;

pub fn register_all(r: &Registry) {
    let _ = r.register(Arc::new(JsonFormatter));
    let _ = r.register(Arc::new(YamlFormatter));
}
