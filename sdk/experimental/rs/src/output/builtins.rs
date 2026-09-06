//! Built-in formatter registration: table + json + yaml + csv + text.
//!
//! 'table' is the default --format and intentionally minimal: pipe-friendly
//! ASCII via comfy-table's `NOTHING` preset, no borders, no color.
//! Adopters wanting richer tables (borders, UTF-8 box-drawing, theming)
//! register their own Formatter under the 'table' key.

use std::sync::Arc;

use super::registry::Registry;

mod csv;
mod json;
mod rows;
mod table;
mod text;
mod yaml;

pub use csv::CsvFormatter;
pub use json::JsonFormatter;
pub use table::TableFormatter;
pub use text::TextFormatter;
pub use yaml::YamlFormatter;

pub fn register_all(r: &Registry) {
    let _ = r.register(Arc::new(CsvFormatter));
    let _ = r.register(Arc::new(JsonFormatter));
    let _ = r.register(Arc::new(TableFormatter));
    let _ = r.register(Arc::new(TextFormatter));
    let _ = r.register(Arc::new(YamlFormatter));
}
