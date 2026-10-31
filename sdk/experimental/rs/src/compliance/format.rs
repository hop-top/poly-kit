//! Report rendering (Go `format.go`).

use std::fmt::Write;

use super::{Report, Status};

/// Renders `r` as `text` (the default) or `json`.
pub fn format_report(r: &Report, format: &str) -> String {
    if format.eq_ignore_ascii_case("json") {
        return match serde_json::to_string_pretty(r) {
            Ok(s) => s + "\n",
            Err(e) => format!("{{\"error\": {:?}}}", e.to_string()),
        };
    }
    text(r)
}

fn icon(s: Status) -> &'static str {
    match s {
        Status::Pass => "PASS",
        Status::Fail => "FAIL",
        Status::Warn => "WARN",
        Status::Skip => "SKIP",
    }
}

fn text(r: &Report) -> String {
    let mut b = String::new();
    b.push_str("\n  12-Factor AI CLI Compliance Report\n");
    b.push_str("  ══════════════════════════════════\n");
    if !r.binary.is_empty() {
        let _ = writeln!(b, "  Binary   : {}", r.binary);
    }
    if !r.toolspec.is_empty() {
        let _ = writeln!(b, "  Toolspec : {}", r.toolspec);
    }
    b.push('\n');
    for cr in &r.results {
        let _ = writeln!(
            b,
            "  {}  F{:<2} {:<22} {}",
            icon(cr.status),
            cr.factor.number(),
            cr.name,
            cr.details
        );
        if !cr.suggestion.is_empty() {
            let _ = writeln!(b, "       └─ {}", cr.suggestion);
        }
    }
    b.push('\n');
    let _ = writeln!(b, "  Score: {}/{} factors passing", r.score, r.total);
    b.push('\n');
    b
}
