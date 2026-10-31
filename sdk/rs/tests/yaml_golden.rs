//! Byte-exact golden tests for every YAML emit path in the crate: the
//! `yaml` formatter (direct and through `--format yaml` dispatch) and the
//! structured-error envelope.
//!
//! The YAML backend is an implementation detail; these pin the bytes it
//! emits so a backend swap cannot change them silently.

#![cfg(feature = "output")]

use hop_top_kit::output::{default_registry, render_error, CliError, Options};
use serde_json::{json, Value};

fn yaml(data: &Value, cols: &[&str]) -> String {
    let cols: Vec<String> = cols.iter().map(|c| (*c).to_string()).collect();
    let r = default_registry();
    let f = r.lookup("yaml").expect("yaml formatter");
    let mut buf = Vec::new();
    f.render(&mut buf, data, &Options::new(), &cols)
        .expect("render");
    String::from_utf8(buf).expect("utf8")
}

fn error_yaml(err: &CliError) -> String {
    let mut buf = Vec::new();
    render_error(&mut buf, "yaml", err).expect("render_error");
    String::from_utf8(buf).expect("utf8")
}

// --- yaml formatter -----------------------------------------------------

#[test]
fn golden_scalars() {
    assert_eq!(yaml(&json!("hello"), &[]), "hello\n");
    assert_eq!(yaml(&json!(42), &[]), "42\n");
    assert_eq!(yaml(&json!(-7), &[]), "-7\n");
    assert_eq!(yaml(&json!(1.5), &[]), "1.5\n");
    assert_eq!(yaml(&json!(true), &[]), "true\n");
    assert_eq!(yaml(&json!(false), &[]), "false\n");
    assert_eq!(yaml(&Value::Null, &[]), "null\n");
}

#[test]
fn golden_empty_collections() {
    assert_eq!(yaml(&json!([]), &[]), "[]\n");
    assert_eq!(yaml(&json!({}), &[]), "{}\n");
    assert_eq!(yaml(&json!({"a": [], "b": {}}), &[]), "a: []\nb: {}\n");
}

#[test]
fn golden_nested_document_keeps_insertion_order() {
    let data = json!({
        "zeta": 1,
        "alpha": {"mid": [1, 2], "first": "x"},
        "list": [{"k": "v", "a": null}, "s", 3],
    });
    assert_eq!(
        yaml(&data, &[]),
        "zeta: 1\nalpha:\n  mid:\n  - 1\n  - 2\n  first: x\nlist:\n- k: v\n  a: null\n- s\n- 3\n"
    );
}

#[test]
fn golden_list_of_rows() {
    let data = json!([
        {"name": "alpha", "count": 1, "ok": true},
        {"name": "beta", "count": 2, "ok": false},
    ]);
    assert_eq!(
        yaml(&data, &[]),
        "- name: alpha\n  count: 1\n  ok: true\n- name: beta\n  count: 2\n  ok: false\n"
    );
}

#[test]
fn golden_cols_projection_object_and_list() {
    let row = json!({"name": "alpha", "count": 1, "status": "ok"});
    assert_eq!(yaml(&row, &["status", "name"]), "status: ok\nname: alpha\n");
    let rows = json!([row.clone(), {"name": "beta", "status": "bad"}]);
    assert_eq!(
        yaml(&rows, &["name", "count"]),
        "- name: alpha\n  count: 1\n- name: beta\n"
    );
}

#[test]
fn golden_strings_needing_quotes() {
    let data = json!({
        "empty": "",
        "yes_word": "yes",
        "no_word": "no",
        "null_word": "null",
        "tilde": "~",
        "true_word": "true",
        "int_like": "123",
        "float_like": "1.5",
        "octal_like": "0o17",
        "hex_like": "0x1F",
        "exp_like": "1e3",
        "inf_like": ".inf",
        "colon": "a: b",
        "hash": "a #b",
        "dash": "- item",
        "lead_space": " x",
        "trail_space": "x ",
        "brace": "{x}",
        "bracket": "[x]",
        "star": "*alias",
        "amp": "&anchor",
        "bang": "!tag",
        "pct": "%dir",
        "at": "@x",
        "tick": "`x`",
        "quote": "it's",
        "dquote": "say \"hi\"",
        "date_like": "2026-05-19",
        "time_like": "2026-05-19T12:00:00Z",
    });
    assert_eq!(yaml(&data, &[]), "empty: ''\nyes_word: yes\nno_word: no\nnull_word: 'null'\ntilde: '~'\ntrue_word: 'true'\nint_like: '123'\nfloat_like: '1.5'\noctal_like: '0o17'\nhex_like: '0x1F'\nexp_like: '1e3'\ninf_like: '.inf'\ncolon: 'a: b'\nhash: 'a #b'\ndash: '- item'\nlead_space: ' x'\ntrail_space: 'x '\nbrace: '{x}'\nbracket: '[x]'\nstar: '*alias'\namp: '&anchor'\nbang: '!tag'\npct: '%dir'\nat: '@x'\ntick: '`x`'\nquote: it's\ndquote: say \"hi\"\ndate_like: 2026-05-19\ntime_like: 2026-05-19T12:00:00Z\n");
}

#[test]
fn golden_multiline_and_special_characters() {
    let data = json!({
        "two_lines": "a\nb",
        "trailing_nl": "a\n",
        "blank_between": "a\n\nb",
        "tab": "a\tb",
        "cr": "a\rb",
        "unicode": "caf\u{e9} \u{2713} \u{1F600}",
        "ctrl_char": "a\u{0001}b",
        "backslash": "a\\b",
        "long": "word ".repeat(40).trim_end(),
    });
    assert_eq!(yaml(&data, &[]), "two_lines: |-\n  a\n  b\ntrailing_nl: |\n  a\nblank_between: |-\n  a\n\n  b\ntab: \"a\\tb\"\ncr: \"a\\rb\"\nunicode: café ✓ 😀\nctrl_char: \"a\\x01b\"\nbackslash: a\\b\nlong: word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word word\n");
}

#[test]
fn golden_awkward_keys() {
    let data = json!({
        "": 1,
        "with space": 2,
        "with: colon": 3,
        "123": 4,
        "true": 5,
        "null": 6,
        "-dash": 7,
        "caf\u{e9}": 8,
    });
    assert_eq!(yaml(&data, &[]), "'': 1\nwith space: 2\n'with: colon': 3\n'123': 4\n'true': 5\n'null': 6\n-dash: 7\ncafé: 8\n");
}

#[test]
fn golden_numbers() {
    let data = json!({
        "zero": 0,
        "neg": -1,
        "big_u64": 18446744073709551615u64,
        "min_i64": -9223372036854775808i64,
        "float": 0.1,
        "float_whole": 2.0,
        "float_exp": 1.5e300,
        "float_small": 1.5e-7,
        "float_neg_zero": -0.0,
    });
    assert_eq!(yaml(&data, &[]), "zero: 0\nneg: -1\nbig_u64: 18446744073709551615\nmin_i64: -9223372036854775808\nfloat: 0.1\nfloat_whole: 2.0\nfloat_exp: 1.5e300\nfloat_small: 1.5e-7\nfloat_neg_zero: -0.0\n");
}

// --- structured-error envelope -----------------------------------------

#[test]
fn golden_error_envelope_minimal() {
    assert_eq!(
        error_yaml(&CliError::transient("upstream timeout")),
        "code: TRANSIENT\nmessage: upstream timeout\nexit_code: 6\ntransience: transient\n"
    );
}

#[test]
fn golden_error_envelope_full() {
    let err = CliError {
        code: "NOT_FOUND".to_string(),
        message: "missing: thing".to_string(),
        cause: "root\ncause".to_string(),
        suggested_fix: "try --all".to_string(),
        alternatives: vec!["other".to_string(), "yes".to_string(), "123".to_string()],
        exit_code: 3,
        corrected_from: String::new(),
        transience: "permanent".to_string(),
    };
    assert_eq!(error_yaml(&err), "code: NOT_FOUND\nmessage: 'missing: thing'\ncause: |-\n  root\n  cause\nsuggested_fix: try --all\nalternatives:\n- other\n- yes\n- '123'\nexit_code: 3\ntransience: permanent\n");
}

#[test]
fn golden_error_envelope_unset_transience_is_unknown() {
    let err = CliError {
        code: "X".to_string(),
        message: "m".to_string(),
        exit_code: 9,
        ..CliError::default()
    };
    assert_eq!(
        error_yaml(&err),
        "code: X\nmessage: m\nexit_code: 9\ntransience: unknown\n"
    );
}

// --- --format yaml dispatch ---------------------------------------------

#[cfg(feature = "cli")]
mod dispatch_path {
    use clap::Command;
    use hop_top_kit::output::{
        dispatch, register_output_flags, ColumnSpec, DispatchOptions, RegisterOutputFlagsOptions,
    };
    use serde_json::json;

    fn run(args: &[&str], data: &serde_json::Value, opts: DispatchOptions) -> String {
        let (cmd, _ctx) = register_output_flags(
            Command::new("demo").no_binary_name(true),
            RegisterOutputFlagsOptions::default(),
        );
        let matches = cmd.try_get_matches_from(args).unwrap();
        let mut buf = Vec::new();
        dispatch(&matches, &mut buf, data, opts).unwrap();
        String::from_utf8(buf).unwrap()
    }

    #[test]
    fn golden_dispatch_format_yaml() {
        let data = json!([{"name": "alpha", "count": 1}, {"name": "beta", "count": 2}]);
        assert_eq!(
            run(&["--format", "yaml"], &data, DispatchOptions::default()),
            "- name: alpha\n  count: 1\n- name: beta\n  count: 2\n"
        );
    }

    #[test]
    fn golden_dispatch_format_yaml_with_cols() {
        let data = json!([{"name": "alpha", "count": 1, "status": "ok"}]);
        assert_eq!(
            run(
                &["--format", "yaml", "--cols", "status,name"],
                &data,
                DispatchOptions::default()
            ),
            "- status: ok\n  name: alpha\n"
        );
    }

    #[test]
    fn golden_dispatch_column_spec_order_and_selection() {
        let cols = [
            ColumnSpec::new("status", "status", 0),
            ColumnSpec::new("name", "name", 0),
        ];
        let data = json!([{"name": "alpha", "count": 1, "status": "ok"}]);
        let opts = DispatchOptions {
            columns: Some(&cols),
            ..DispatchOptions::default()
        };
        assert_eq!(
            run(&["--format", "yaml"], &data, opts),
            "- status: ok\n  name: alpha\n"
        );
    }

    #[test]
    fn golden_dispatch_output_extension_yaml() {
        let tmp = tempfile::NamedTempFile::with_suffix(".yml").unwrap();
        let path = tmp.path().to_string_lossy().into_owned();
        let mut sink = Vec::new();
        let (cmd, _ctx) = register_output_flags(
            Command::new("demo").no_binary_name(true),
            RegisterOutputFlagsOptions::default(),
        );
        let matches = cmd.try_get_matches_from(["-o", &path]).unwrap();
        dispatch(
            &matches,
            &mut sink,
            &json!({"name": "alpha", "tags": ["a", "b"]}),
            DispatchOptions::default(),
        )
        .unwrap();
        assert_eq!(
            std::fs::read_to_string(&path).unwrap(),
            "name: alpha\ntags:\n- a\n- b\n"
        );
    }
}
