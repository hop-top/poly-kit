//! Human formatter: the `human` key in the default registry.
//!
//! Mirrors go `console/output/human.go`. Go dispatches to a
//! `HumanRenderer` hook when the payload's type ships one and falls back
//! to the table view otherwise. A payload here is a `serde_json::Value`,
//! which carries no per-type hook, so every payload takes the fallback:
//! the table rendering, so `--format human` always produces a usable view
//! rather than an error. A command with a bespoke human layout registers
//! its own formatter under `human` with `Registry::override_with`.
//!
//! No extensions (never picked by `--output` inference) and no options.

use std::io::Write;

use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, Options};

use super::table::TableFormatter;

pub struct HumanFormatter;

impl Formatter for HumanFormatter {
    fn key(&self) -> &'static str {
        "human"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[]
    }

    fn options(&self) -> &'static [OptionSpec] {
        &[]
    }

    fn render(
        &self,
        out: &mut dyn Write,
        data: &Value,
        _opts: &Options,
        cols: &[String],
    ) -> std::io::Result<()> {
        // Table's own options are not human's: it declares none, so the
        // caller's (validated, empty) options are not forwarded.
        TableFormatter.render(out, data, &Options::new(), cols)
    }
}
