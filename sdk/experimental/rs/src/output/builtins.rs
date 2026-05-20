//! Built-in formatter registration. Phase-1 ships table + json + yaml;
//! csv / text land in a follow-up once renderers are in place.
//!
//! 'table' is the default --format and intentionally minimal: pipe-friendly
//! ASCII via comfy-table's `NOTHING` preset, no borders, no color.
//! Adopters wanting richer tables (borders, UTF-8 box-drawing, theming)
//! register their own Formatter under the 'table' key.

use std::sync::Arc;

use super::registry::Registry;

mod json;
mod table;
mod yaml;

pub use json::JsonFormatter;
pub use table::TableFormatter;
pub use yaml::YamlFormatter;

pub fn register_all(r: &Registry) {
    let _ = r.register(Arc::new(TableFormatter));
    let _ = r.register(Arc::new(JsonFormatter));
    let _ = r.register(Arc::new(YamlFormatter));
}
