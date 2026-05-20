//! Integration tests for the `output` and `cli` features.

#![cfg(feature = "cli")]

use std::sync::Arc;

use clap::Command;
use hop_top_kit::output::{
    default_registry, dispatch, register_output_flags, ColumnSpec, DispatchOptions, Formatter,
    OptionSpec, OptionType, Options, Registry, RegisterOutputFlagsOptions,
};
use serde_json::{json, Value};

// --- Registry -----------------------------------------------------------

struct DummyJson;
impl Formatter for DummyJson {
    fn key(&self) -> &'static str {
        "json"
    }
    fn extensions(&self) -> &'static [&'static str] {
        &[".json"]
    }
    fn options(&self) -> &'static [OptionSpec] {
        &[]
    }
    fn render(
        &self,
        _out: &mut dyn std::io::Write,
        _data: &Value,
        _opts: &Options,
        _cols: &[String],
    ) -> std::io::Result<()> {
        Ok(())
    }
}

#[test]
fn registry_register_lookup_duplicate_override() {
    let r = Registry::new();
    let a: Arc<dyn Formatter> = Arc::new(DummyJson);
    r.register(a.clone()).unwrap();
    assert!(r.lookup("json").is_some());
    assert!(r.lookup("missing").is_none());

    let b: Arc<dyn Formatter> = Arc::new(DummyJson);
    let err = r.register(b.clone()).unwrap_err();
    assert!(format!("{err}").contains("'json' already registered"));

    r.override_with(b).unwrap();
    assert!(r.lookup("json").is_some());
}

#[test]
fn registry_keys_sorted_and_extension_map() {
    let r = default_registry();
    let keys = r.keys();
    assert_eq!(keys, vec!["json", "table", "yaml"]);

    let exts = r.extension_map();
    assert_eq!(exts.get("json").copied(), Some("json"));
    assert_eq!(exts.get("yaml").copied(), Some("yaml"));
    assert_eq!(exts.get("yml").copied(), Some("yaml"));
    // table intentionally has no extensions — never picks up ext-infer.
    assert!(exts.values().all(|v| *v != "table"));
}

// --- Built-in renders --------------------------------------------------

#[test]
fn json_formatter_list_payload() {
    let r = default_registry();
    let f = r.lookup("json").unwrap();
    let data = json!([
        {"name": "alpha", "count": 1},
        {"name": "beta",  "count": 2},
    ]);
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &[]).unwrap();
    let parsed: Value = serde_json::from_slice(&buf).unwrap();
    assert_eq!(parsed, data);
}

#[test]
fn json_formatter_cols_projection() {
    let r = default_registry();
    let f = r.lookup("json").unwrap();
    let data = json!([{"name": "alpha", "count": 1, "status": "ok"}]);
    let cols = vec!["name".to_string(), "status".to_string()];
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &cols).unwrap();
    let parsed: Value = serde_json::from_slice(&buf).unwrap();
    assert_eq!(parsed, json!([{"name": "alpha", "status": "ok"}]));
}

#[test]
fn yaml_formatter_list_payload() {
    let r = default_registry();
    let f = r.lookup("yaml").unwrap();
    let data = json!([{"name": "alpha"}, {"name": "beta"}]);
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &[]).unwrap();
    let yaml = std::str::from_utf8(&buf).unwrap();
    assert!(yaml.contains("name: alpha"));
    assert!(yaml.contains("name: beta"));
}

// --- Dispatch end-to-end -----------------------------------------------

fn build_cmd() -> Command {
    let (cmd, _ctx) = register_output_flags(
        Command::new("demo").no_binary_name(true),
        RegisterOutputFlagsOptions::default(),
    );
    cmd
}

#[test]
fn dispatch_explicit_json_to_writer() {
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format", "json"]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!([{"a": 1}]),
        DispatchOptions::default(),
    )
    .unwrap();
    let parsed: Value = serde_json::from_slice(&buf).unwrap();
    assert_eq!(parsed, json!([{"a": 1}]));
}

/// Regression guard for the table/default-format mismatch class of bug:
/// invoking dispatch with no --format and no --output extension must
/// resolve to a formatter that actually exists in the registry.
/// Before TableFormatter shipped, this errored with UnknownFormat('table').
#[test]
fn dispatch_default_format_path_succeeds_without_flags_or_extension() {
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from::<_, &str>([]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!([{"name": "alpha", "count": 1}]),
        DispatchOptions::default(),
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    // Table renders header + 1 row.
    assert!(out.contains("name"), "expected 'name' header in default-format output, got: {out}");
    assert!(out.contains("alpha"));
    assert!(out.contains('1'));
}

#[test]
fn table_formatter_renders_header_and_rows() {
    let r = default_registry();
    let f = r.lookup("table").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([
            {"name": "alpha", "count": 1},
            {"name": "beta",  "count": 22},
        ]),
        &Options::new(),
        &[],
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert!(out.contains("name"));
    assert!(out.contains("count"));
    assert!(out.contains("alpha"));
    assert!(out.contains("beta"));
    assert!(out.contains("22"));
}

#[test]
fn table_formatter_cols_projection() {
    let r = default_registry();
    let f = r.lookup("table").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1, "status": "ok"}]),
        &Options::new(),
        &["status".to_string(), "name".to_string()],
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    // 'count' must not appear when projected away.
    assert!(!out.contains("count"));
    assert!(out.contains("status"));
    assert!(out.contains("alpha"));
}

#[test]
fn table_formatter_header_false_suppresses_header() {
    use hop_top_kit::output::OptionValue;
    let r = default_registry();
    let f = r.lookup("table").unwrap();
    let mut opts = Options::new();
    opts.insert("header".to_string(), OptionValue::Bool(false));
    let mut buf = Vec::new();
    f.render(&mut buf, &json!([{"name": "alpha"}]), &opts, &[])
        .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    // Without header, the literal "name" string shouldn't appear (only "alpha" does).
    assert!(!out.contains("name"));
    assert!(out.contains("alpha"));
}

#[test]
fn dispatch_infers_format_from_output_ext() {
    let tmp = tempfile::NamedTempFile::with_suffix(".yaml").unwrap();
    let path = tmp.path().to_string_lossy().into_owned();
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["-o", &path]).unwrap();
    let mut sink = Vec::new();
    dispatch(
        &matches,
        &mut sink,
        &json!({"name": "alpha"}),
        DispatchOptions::default(),
    )
    .unwrap();
    let content = std::fs::read_to_string(&path).unwrap();
    assert!(content.contains("name: alpha"));
}

#[test]
fn dispatch_explicit_format_conflicts_with_ext() {
    let tmp = tempfile::NamedTempFile::with_suffix(".yaml").unwrap();
    let path = tmp.path().to_string_lossy().into_owned();
    let cmd = build_cmd();
    let matches = cmd
        .try_get_matches_from(["--format", "json", "-o", &path])
        .unwrap();
    let mut buf = Vec::new();
    let err = dispatch(&matches, &mut buf, &json!({}), DispatchOptions::default()).unwrap_err();
    assert!(format!("{err}").contains("conflicts with --output extension"));
}

#[test]
fn dispatch_format_help_lists_all() {
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format-help"]).unwrap();
    let mut buf = Vec::new();
    dispatch(&matches, &mut buf, &json!({}), DispatchOptions::default()).unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert!(out.contains("json"));
    assert!(out.contains("yaml"));
}

#[test]
fn dispatch_unknown_format_rejected() {
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format", "bogus"]).unwrap();
    let mut buf = Vec::new();
    let err = dispatch(&matches, &mut buf, &json!({}), DispatchOptions::default()).unwrap_err();
    assert!(format!("{err}").contains("unknown output format 'bogus'"));
}

#[test]
fn dispatch_template_mutually_exclusive_with_cols() {
    let cmd = build_cmd();
    let matches = cmd
        .try_get_matches_from([
            "--format", "json", "--template", "{a}", "--cols", "a",
        ])
        .unwrap();
    let mut buf = Vec::new();
    let err = dispatch(
        &matches,
        &mut buf,
        &json!([{"a": 1}]),
        DispatchOptions::default(),
    )
    .unwrap_err();
    assert!(format!("{err}").contains("mutually exclusive"));
}

#[test]
fn dispatch_template_renders() {
    let cmd = build_cmd();
    let matches = cmd
        .try_get_matches_from(["--template", "{name}:{count}"])
        .unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!([{"name": "alpha", "count": 1}]),
        DispatchOptions::default(),
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(out, "alpha:1\n");
}

#[test]
fn dispatch_cols_validated_against_schema() {
    let schema = [
        ColumnSpec::new("name", "name", 9),
        ColumnSpec::new("count", "count", 7),
    ];
    let cmd = build_cmd();
    let matches = cmd
        .try_get_matches_from(["--format", "json", "--cols", "bogus"])
        .unwrap();
    let mut buf = Vec::new();
    let err = dispatch(
        &matches,
        &mut buf,
        &json!([{"name": "x"}]),
        DispatchOptions {
            columns: Some(&schema),
            ..Default::default()
        },
    )
    .unwrap_err();
    assert!(format!("{err}").contains("unknown column 'bogus'"));
}

#[test]
fn dispatch_format_opt_forwards_to_formatter() {
    let cmd = build_cmd();
    let matches = cmd
        .try_get_matches_from(["--format", "json", "--format-opt", "indent=0"])
        .unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!({"a": 1}),
        DispatchOptions::default(),
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "{\"a\":1}\n");
}
