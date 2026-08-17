//! Integration tests for the `output` and `cli` features.

#![cfg(feature = "cli")]

use std::sync::Arc;

use clap::Command;
use hop_top_kit::output::{
    default_registry, dispatch, register_output_flags, ColumnSpec, DispatchOptions, Formatter,
    OptionSpec, OptionValue, Options, RegisterOutputFlagsOptions, Registry,
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
    assert_eq!(keys, vec!["csv", "json", "table", "text", "yaml"]);

    let exts = r.extension_map();
    assert_eq!(exts.get("json").copied(), Some("json"));
    assert_eq!(exts.get("yaml").copied(), Some("yaml"));
    assert_eq!(exts.get("yml").copied(), Some("yaml"));
    assert_eq!(exts.get("csv").copied(), Some("csv"));
    assert_eq!(exts.get("txt").copied(), Some("text"));
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

/// `--cols` selects AND reorders: user order wins (contract rule 2).
///
/// Asserts on the SERIALIZED BYTES, not on `Value == Value`. `serde_json`
/// compares objects key-set-wise and ignores key order, so a Value-level
/// assertion here passes under any ordering and pins nothing.
#[test]
fn json_formatter_cols_projection_preserves_user_order() {
    let r = default_registry();
    let f = r.lookup("json").unwrap();
    let data = json!([{"name": "alpha", "count": 1, "status": "ok"}]);
    let cols = vec!["status".to_string(), "name".to_string()];
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &cols).unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(
        out, "[\n  {\n    \"status\": \"ok\",\n    \"name\": \"alpha\"\n  }\n]\n",
        "cols order must survive serialization verbatim, got: {out}"
    );
}

/// With no `--cols` and no ColumnSpec, payload key order is the fallback
/// (contract rule 1) — it must NOT be alphabetized on the way out.
#[test]
fn json_formatter_preserves_payload_key_order() {
    let r = default_registry();
    let f = r.lookup("json").unwrap();
    let data: Value =
        serde_json::from_str(r#"[{"zeta":1,"alpha":{"omega":2,"beta":3},"mid":"x"}]"#).unwrap();
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &[]).unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    let keys: Vec<&str> = ["zeta", "alpha", "omega", "beta", "mid"]
        .into_iter()
        .filter(|k| out.contains(&format!("\"{k}\"")))
        .collect();
    // Ordering must hold at every nesting depth, not just the top level.
    let positions: Vec<usize> = ["\"zeta\"", "\"alpha\"", "\"omega\"", "\"beta\"", "\"mid\""]
        .iter()
        .map(|k| {
            out.find(k)
                .unwrap_or_else(|| panic!("missing {k} in {out}"))
        })
        .collect();
    assert_eq!(keys.len(), 5, "all keys present, got: {out}");
    assert!(
        positions.windows(2).all(|w| w[0] < w[1]),
        "payload key order must survive at every depth, got: {out}"
    );
}

/// YAML shares the projection path; key order must survive there too.
#[test]
fn yaml_formatter_cols_projection_preserves_user_order() {
    let r = default_registry();
    let f = r.lookup("yaml").unwrap();
    let data = json!([{"name": "alpha", "count": 1, "status": "ok"}]);
    let cols = vec!["status".to_string(), "name".to_string()];
    let mut buf = Vec::new();
    f.render(&mut buf, &data, &Options::new(), &cols).unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(
        out, "- status: ok\n  name: alpha\n",
        "yaml must emit cols in user order, got: {out}"
    );
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
    assert!(
        out.contains("name"),
        "expected 'name' header in default-format output, got: {out}"
    );
    assert!(out.contains("alpha"));
    assert!(out.contains('1'));
}

/// Column sequence is asserted positionally, not with `contains` —
/// a `contains` assertion holds under ANY ordering and pins nothing.
fn column_order(out: &str) -> Vec<&str> {
    out.lines()
        .next()
        .unwrap_or("")
        .split_whitespace()
        .collect()
}

#[test]
fn table_formatter_renders_header_and_rows() {
    let r = default_registry();
    let f = r.lookup("table").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        // Deliberately NOT alphabetical: sorted order would be count, name.
        &serde_json::from_str(r#"[{"name":"alpha","count":1},{"name":"beta","count":22}]"#)
            .unwrap(),
        &Options::new(),
        &[],
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(
        column_order(out),
        vec!["name", "count"],
        "payload key order must drive columns, got: {out}"
    );
    assert!(out.contains("alpha"));
    assert!(out.contains("beta"));
    assert!(out.contains("22"));
}

/// Contract rule 2: `--cols` reorders as well as selects.
#[test]
fn table_formatter_cols_projection_reorders() {
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
    assert_eq!(
        column_order(out),
        vec!["status", "name"],
        "user --cols order must win, got: {out}"
    );
    // 'count' must not appear when projected away.
    assert!(!out.contains("count"));
}

/// Contract rule 4: zero rows emits nothing — not even a bare header.
/// Emptiness is decided by ROW count, never by header count.
#[test]
fn table_formatter_zero_rows_emits_nothing() {
    let r = default_registry();
    let f = r.lookup("table").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([]),
        &Options::new(),
        &["name".to_string(), "count".to_string()],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "",
        "zero rows must emit nothing, not a bare header row"
    );
}

/// Contract rule 1: the ColumnSpec list drives default column order and
/// headers when the caller supplies one and the user passes no --cols.
#[test]
fn dispatch_columnspec_drives_default_order() {
    let schema = [
        ColumnSpec::new("name", "name", 9),
        ColumnSpec::new("count", "count", 7),
        ColumnSpec::new("status", "status", 5),
    ];
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from::<_, &str>([]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        // Payload key order is deliberately the reverse of the schema, so
        // a passing assertion can only come from the ColumnSpec list.
        &serde_json::from_str(r#"[{"status":"ok","count":1,"name":"alpha"}]"#).unwrap(),
        DispatchOptions {
            columns: Some(&schema),
            ..Default::default()
        },
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(
        column_order(out),
        vec!["name", "count", "status"],
        "ColumnSpec order must drive default columns, got: {out}"
    );
}

/// Contract rule 2 at the dispatch layer: --cols beats the ColumnSpec order.
#[test]
fn dispatch_user_cols_override_columnspec_order() {
    let schema = [
        ColumnSpec::new("name", "name", 9),
        ColumnSpec::new("count", "count", 7),
        ColumnSpec::new("status", "status", 5),
    ];
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--cols", "status,name"]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!([{"name": "alpha", "count": 1, "status": "ok"}]),
        DispatchOptions {
            columns: Some(&schema),
            ..Default::default()
        },
    )
    .unwrap();
    let out = std::str::from_utf8(&buf).unwrap();
    assert_eq!(
        column_order(out),
        vec!["status", "name"],
        "--cols must reorder as well as select, got: {out}"
    );
}

/// ColumnSpec order must reach the JSON path too, not just table.
#[test]
fn dispatch_columnspec_drives_json_key_order() {
    let schema = [
        ColumnSpec::new("name", "name", 9),
        ColumnSpec::new("count", "count", 7),
    ];
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format", "json"]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &serde_json::from_str(r#"[{"count":1,"name":"alpha"}]"#).unwrap(),
        DispatchOptions {
            columns: Some(&schema),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "[\n  {\n    \"name\": \"alpha\",\n    \"count\": 1\n  }\n]\n",
    );
}

/// Contract rule 3: header == key, enforced at construction so the two
/// cannot drift. Validation and value lookup are the same operation.
#[test]
#[should_panic(expected = "ColumnSpec header/key mismatch")]
fn columnspec_rejects_header_key_drift() {
    let _ = ColumnSpec::new("Name", "name", 5);
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
        .try_get_matches_from(["--format", "json", "--template", "{a}", "--cols", "a"])
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

// --- csv formatter ------------------------------------------------------

/// The adversarial row that pins csv quoting rules byte-for-byte.
///
/// The cross-language ordering harness compares observed COLUMN ORDER and
/// never raw bytes, so quoting parity is enforced here and nowhere else. Any
/// drift in the quoting rule must fail loudly at this assertion.
///
/// The rule: quote a field iff it contains the delimiter, a double quote, LF,
/// CR, or BEGINS with a unicode space. Trailing spaces and tabs are
/// deliberately NOT quoted — php's fputcsv quotes both, which is exactly the
/// divergence this formatter avoids by implementing the rule explicitly
/// rather than delegating to a library.
///
/// A quoted field's bytes are preserved verbatim. CR and LF are separate
/// alternatives in RFC 4180's `escaped` production, so a bare CR between
/// quotes is legal; dropping it would be silent data loss.
fn adversarial_row() -> Value {
    json!([{
        "a": "plain",
        "b": "with,comma",
        "c": "with\"quote",
        "d": "with\nnewline",
        "e": " leading space",
        "f": "trailing ",
        "g": "",
        "h": "with\ttab",
        "i": "with\rcr"
    }])
}

#[test]
fn csv_formatter_quoting_matches_go_byte_for_byte() {
    let r = default_registry();
    let f = r.lookup("csv").expect("csv formatter must be registered");
    let mut buf = Vec::new();
    f.render(&mut buf, &adversarial_row(), &Options::new(), &[])
        .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "a,b,c,d,e,f,g,h,i\n\
         plain,\"with,comma\",\"with\"\"quote\",\"with\nnewline\",\" leading space\",trailing ,,with\ttab,\"with\rcr\"\n",
    );
}

/// CRLF mode changes the RECORD TERMINATOR and nothing else. An embedded LF
/// stays an LF and a lone CR is preserved; rewriting either would mutate the
/// caller's value. Only the bytes between records differ from LF mode.
#[test]
fn csv_formatter_crlf_changes_only_the_record_terminator() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert("crlf".to_string(), OptionValue::Bool(true));
    let mut buf = Vec::new();
    f.render(&mut buf, &adversarial_row(), &opts, &[]).unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "a,b,c,d,e,f,g,h,i\r\n\
         plain,\"with,comma\",\"with\"\"quote\",\"with\nnewline\",\" leading space\",trailing ,,with\ttab,\"with\rcr\"\r\n",
    );
}

/// Quote-all must preserve CR/LF too. The previous implementation dropped a
/// lone CR on this path as well — worse than the go writer it was copied
/// from, which preserved it here while dropping it on the default path.
#[test]
fn csv_formatter_crlf_quote_all_preserves_cr() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert("crlf".to_string(), OptionValue::Bool(true));
    opts.insert("quote-all".to_string(), OptionValue::Bool(true));
    let mut buf = Vec::new();
    f.render(&mut buf, &adversarial_row(), &opts, &[]).unwrap();
    let got = std::str::from_utf8(&buf).unwrap();
    assert!(
        got.contains("\"with\rcr\""),
        "lone CR must survive quote-all, got {got:?}"
    );
    assert!(
        got.contains("\"with\nnewline\""),
        "in-field LF must stay LF, got {got:?}"
    );
    assert!(!got.contains("withcr"), "CR must not be dropped: {got:?}");
}

/// A minimal RFC 4180 reader. The crate ships no csv reader dependency, so
/// round-trip is proved against a decoder written to the grammar directly:
/// `escaped = DQUOTE *(TEXTDATA / COMMA / CR / LF / 2DQUOTE) DQUOTE`.
/// Records are split only on an UNQUOTED line ending.
fn decode_csv(input: &str, delim: char) -> Vec<Vec<String>> {
    let mut records = Vec::new();
    let mut record = Vec::new();
    let mut field = String::new();
    let mut in_quotes = false;
    let mut chars = input.chars().peekable();
    let mut pending = false; // saw at least one char of the current record

    while let Some(c) = chars.next() {
        pending = true;
        if in_quotes {
            if c == '"' {
                if chars.peek() == Some(&'"') {
                    chars.next();
                    field.push('"');
                } else {
                    in_quotes = false;
                }
            } else {
                field.push(c);
            }
            continue;
        }
        match c {
            '"' if field.is_empty() => in_quotes = true,
            c if c == delim => record.push(std::mem::take(&mut field)),
            '\r' | '\n' => {
                if c == '\r' && chars.peek() == Some(&'\n') {
                    chars.next();
                }
                record.push(std::mem::take(&mut field));
                records.push(std::mem::take(&mut record));
                pending = false;
            }
            other => field.push(other),
        }
    }
    if pending || !field.is_empty() {
        record.push(field);
        records.push(record);
    }
    records
}

/// Round-trip is the acceptance criterion, not byte-equality: byte-equality
/// alone would be satisfied by every runtime agreeing on lossy output.
#[test]
fn csv_formatter_round_trips_adversarial_row() {
    let expected: Vec<String> = [
        "plain",
        "with,comma",
        "with\"quote",
        "with\nnewline",
        " leading space",
        "trailing ",
        "",
        "with\ttab",
        "with\rcr",
    ]
    .iter()
    .map(|s| s.to_string())
    .collect();

    for (label, crlf, quote_all) in [
        ("lf", false, false),
        ("crlf", true, false),
        ("lf/quote-all", false, true),
        ("crlf/quote-all", true, true),
    ] {
        let r = default_registry();
        let f = r.lookup("csv").unwrap();
        let mut opts = Options::new();
        opts.insert("crlf".to_string(), OptionValue::Bool(crlf));
        opts.insert("quote-all".to_string(), OptionValue::Bool(quote_all));
        opts.insert("no-header".to_string(), OptionValue::Bool(true));
        let mut buf = Vec::new();
        f.render(&mut buf, &adversarial_row(), &opts, &[]).unwrap();
        let text = std::str::from_utf8(&buf).unwrap();

        let recs = decode_csv(text, ',');
        assert_eq!(
            recs.len(),
            1,
            "{label}: one row must decode to one record, got {text:?}"
        );
        assert_eq!(recs[0], expected, "{label}: round-trip must be lossless");
    }
}

/// Go quotes on `unicode::is_whitespace` of the FIRST character, not on a
/// literal ASCII space. `starts_with(' ')` silently left a leading TAB, VT
/// and NBSP unquoted — a divergence from the rule this formatter documents.
#[test]
fn csv_formatter_quotes_any_leading_unicode_space() {
    for (input, want) in [
        ("\tlead", "\"\tlead\"\n"),
        (" lead", "\" lead\"\n"),
        ("\u{a0}lead", "\"\u{a0}lead\"\n"),
        ("\u{b}lead", "\"\u{b}lead\"\n"),
        ("trail ", "trail \n"),
        ("plain", "plain\n"),
    ] {
        let r = default_registry();
        let f = r.lookup("csv").unwrap();
        let mut opts = Options::new();
        opts.insert("no-header".to_string(), OptionValue::Bool(true));
        let mut buf = Vec::new();
        f.render(&mut buf, &json!([{ "v": input }]), &opts, &[])
            .unwrap();
        assert_eq!(std::str::from_utf8(&buf).unwrap(), want, "input {input:?}");
    }
}

/// `\.` alone on a line terminates a PostgreSQL COPY stream; go's writer
/// quotes it defensively and so must this one.
#[test]
fn csv_formatter_quotes_postgres_copy_sentinel() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert("no-header".to_string(), OptionValue::Bool(true));
    let mut buf = Vec::new();
    f.render(&mut buf, &json!([{ "v": "\\." }]), &opts, &[])
        .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "\"\\.\"\n");
}

#[test]
fn csv_formatter_header_and_rows() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        // Deliberately NOT alphabetical: sorted order would be count, name.
        &json!([{"name": "alpha", "count": 1}, {"name": "beta", "count": 22}]),
        &Options::new(),
        &[],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "name,count\nalpha,1\nbeta,22\n"
    );
}

#[test]
fn csv_formatter_no_header_option() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert("no-header".to_string(), OptionValue::Bool(true));
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1}]),
        &opts,
        &[],
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "alpha,1\n");
}

#[test]
fn csv_formatter_quote_all_option() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert("quote-all".to_string(), OptionValue::Bool(true));
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "al\"pha", "count": 1}]),
        &opts,
        &[],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "\"name\",\"count\"\n\"al\"\"pha\",\"1\"\n"
    );
}

#[test]
fn csv_formatter_custom_delimiter() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert(
        "delimiter".to_string(),
        OptionValue::String(";".to_string()),
    );
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        // 'a;b' must be quoted because it contains the ACTIVE delimiter;
        // 'c,d' must NOT be, because a comma is just a character now.
        &json!([{"x": "a;b", "y": "c,d"}]),
        &opts,
        &[],
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "x;y\n\"a;b\";c,d\n");
}

#[test]
fn csv_formatter_rejects_multichar_delimiter() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut opts = Options::new();
    opts.insert(
        "delimiter".to_string(),
        OptionValue::String("||".to_string()),
    );
    let mut buf = Vec::new();
    let err = f
        .render(&mut buf, &json!([{"x": 1}]), &opts, &[])
        .unwrap_err();
    assert!(
        format!("{err}").contains("delimiter must be exactly one character"),
        "got: {err}"
    );
}

/// Contract rule 2: `--cols` reorders as well as selects. The requested
/// order disagrees with BOTH alphabetical (count, name, status) and payload
/// declaration order (name, count, status), so a runtime that merely selects
/// is caught here.
#[test]
fn csv_formatter_cols_reorder_beats_alphabetical_and_declaration() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1, "status": "ok"}]),
        &Options::new(),
        &["status".to_string(), "name".to_string()],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "status,name\nok,alpha\n"
    );
}

/// Contract rule 4: zero rows emits nothing — not even a bare header row,
/// even though `cols` is populated.
#[test]
fn csv_formatter_zero_rows_emits_nothing() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([]),
        &Options::new(),
        &["name".to_string(), "count".to_string()],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "",
        "zero rows must emit nothing, not a bare header row"
    );
}

#[test]
fn csv_formatter_missing_and_null_cells_are_empty() {
    let r = default_registry();
    let f = r.lookup("csv").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"a": null, "b": 1}]),
        &Options::new(),
        &["a".to_string(), "b".to_string(), "missing".to_string()],
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "a,b,missing\n,1,\n");
}

// --- text formatter -----------------------------------------------------

#[test]
fn text_formatter_kv_default_style() {
    let r = default_registry();
    let f = r.lookup("text").expect("text formatter must be registered");
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1}, {"name": "beta", "count": 22}]),
        &Options::new(),
        &[],
    )
    .unwrap();
    // Blank line BETWEEN records, never trailing.
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "name=alpha\ncount=1\n\nname=beta\ncount=22\n"
    );
}

#[test]
fn text_formatter_kv_custom_separator() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut opts = Options::new();
    opts.insert(
        "separator".to_string(),
        OptionValue::String(": ".to_string()),
    );
    let mut buf = Vec::new();
    f.render(&mut buf, &json!([{"name": "alpha"}]), &opts, &[])
        .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "name: alpha\n");
}

#[test]
fn text_formatter_lines_style_is_tab_separated_without_header() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut opts = Options::new();
    opts.insert(
        "style".to_string(),
        OptionValue::String("lines".to_string()),
    );
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1}, {"name": "beta", "count": 22}]),
        &opts,
        &[],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "alpha\t1\nbeta\t22\n",
        "lines style emits no header row"
    );
}

#[test]
fn text_formatter_paragraph_style() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut opts = Options::new();
    opts.insert(
        "style".to_string(),
        OptionValue::String("paragraph".to_string()),
    );
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1}, {"name": "beta", "count": 22}]),
        &opts,
        &[],
    )
    .unwrap();
    // Records are 1-indexed; blank line BETWEEN records, never trailing.
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "Record 1:\n  name: alpha\n  count: 1\n\nRecord 2:\n  name: beta\n  count: 22\n"
    );
}

/// Contract rule 2, on the text path: requested order disagrees with both
/// alphabetical and declaration order.
#[test]
fn text_formatter_cols_reorder_beats_alphabetical_and_declaration() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([{"name": "alpha", "count": 1, "status": "ok"}]),
        &Options::new(),
        &["status".to_string(), "name".to_string()],
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "status=ok\nname=alpha\n"
    );
}

/// Contract rule 4 on the text path.
#[test]
fn text_formatter_zero_rows_emits_nothing() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut buf = Vec::new();
    f.render(
        &mut buf,
        &json!([]),
        &Options::new(),
        &["name".to_string(), "count".to_string()],
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "");
}

#[test]
fn text_formatter_single_object_payload_is_one_record() {
    let r = default_registry();
    let f = r.lookup("text").unwrap();
    let mut buf = Vec::new();
    f.render(&mut buf, &json!({"name": "alpha"}), &Options::new(), &[])
        .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "name=alpha\n");
}

// --- registration / extension wiring ------------------------------------

/// `--format csv` / `--format text` must resolve, and `--output x.csv` /
/// `x.txt` must infer the right formatter.
#[test]
fn csv_and_text_are_registered_with_extensions() {
    let r = default_registry();
    assert!(r.keys().contains(&"csv"), "csv must be registered");
    assert!(r.keys().contains(&"text"), "text must be registered");
    let ext = r.extension_map();
    assert_eq!(ext.get("csv").copied(), Some("csv"));
    assert_eq!(ext.get("txt").copied(), Some("text"));
}

#[test]
fn dispatch_resolves_csv_format_end_to_end() {
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format", "csv"]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        &json!([{"name": "alpha", "count": 1}]),
        DispatchOptions::default(),
    )
    .unwrap();
    assert_eq!(std::str::from_utf8(&buf).unwrap(), "name,count\nalpha,1\n");
}

/// Contract rule 1 through the dispatcher: the ColumnSpec list drives
/// default order on the csv path, beating payload key order.
#[test]
fn dispatch_columnspec_drives_csv_column_order() {
    let schema = [
        ColumnSpec::new("status", "status", 9),
        ColumnSpec::new("name", "name", 7),
    ];
    let cmd = build_cmd();
    let matches = cmd.try_get_matches_from(["--format", "csv"]).unwrap();
    let mut buf = Vec::new();
    dispatch(
        &matches,
        &mut buf,
        // Payload key order is name, status — the OPPOSITE of the spec.
        &json!([{"name": "alpha", "status": "ok"}]),
        DispatchOptions {
            columns: Some(&schema),
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(
        std::str::from_utf8(&buf).unwrap(),
        "status,name\nok,alpha\n"
    );
}
