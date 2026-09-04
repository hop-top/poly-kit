//! Integration tests for the structured-error envelope.
//! Mirrors go/console/output/error_test.go assertions.

#![cfg(feature = "output")]

use hop_top_kit::output::{
    render_error, transience_for_code, CliError, CODE_CONFLICT, CODE_GENERIC, CODE_NOT_FOUND,
    CODE_PROVENANCE_MISSING, CODE_RATE_LIMITED, CODE_TRANSIENT, CODE_UNAUTHORIZED, CODE_USAGE,
    EXIT_GENERIC, EXIT_PROVENANCE_MISSING, EXIT_RATE_LIMITED, EXIT_TRANSIENT, TRANSIENCE_PERMANENT,
    TRANSIENCE_TRANSIENT, TRANSIENCE_UNKNOWN,
};
use serde_json::Value;

fn render(format: &str, err: &CliError) -> String {
    let mut buf = Vec::new();
    render_error(&mut buf, format, err).expect("render_error");
    String::from_utf8(buf).expect("utf8")
}

// --- Constructors -------------------------------------------------------

#[test]
fn constructors_set_code_exit_transience() {
    let cases = [
        (
            CliError::generic("boom"),
            CODE_GENERIC,
            1,
            TRANSIENCE_PERMANENT,
        ),
        (
            CliError::not_found("nope"),
            CODE_NOT_FOUND,
            3,
            TRANSIENCE_PERMANENT,
        ),
        (
            CliError::conflict("dup"),
            CODE_CONFLICT,
            4,
            TRANSIENCE_PERMANENT,
        ),
        (
            CliError::unauthorized("nope"),
            CODE_UNAUTHORIZED,
            5,
            TRANSIENCE_PERMANENT,
        ),
        (
            CliError::usage("bad flag"),
            CODE_USAGE,
            2,
            TRANSIENCE_PERMANENT,
        ),
        (
            CliError::rate_limited("budget"),
            CODE_RATE_LIMITED,
            64,
            TRANSIENCE_TRANSIENT,
        ),
        (
            CliError::transient("upstream timeout"),
            CODE_TRANSIENT,
            6,
            TRANSIENCE_TRANSIENT,
        ),
        (
            CliError::provenance_missing("/email"),
            CODE_PROVENANCE_MISSING,
            65,
            TRANSIENCE_PERMANENT,
        ),
    ];
    for (got, code, exit, transience) in &cases {
        assert_eq!(got.code, *code);
        assert_eq!(got.exit_code, *exit);
        assert_eq!(got.transience, *transience);
    }

    // Exit-code table is unique; 1 is generic, 6 transient, 65 provenance.
    let mut exits: Vec<i32> = cases.iter().map(|(e, ..)| e.exit_code).collect();
    exits.sort_unstable();
    exits.dedup();
    assert_eq!(exits.len(), cases.len());
    assert_eq!(EXIT_GENERIC, 1);
    assert_eq!(EXIT_TRANSIENT, 6);
    assert_eq!(EXIT_RATE_LIMITED, 64);
    assert_eq!(EXIT_PROVENANCE_MISSING, 65);
}

// --- transience_for_code ------------------------------------------------

#[test]
fn transience_for_code_table() {
    for (code, want) in [
        (CODE_USAGE, TRANSIENCE_PERMANENT),
        (CODE_NOT_FOUND, TRANSIENCE_PERMANENT),
        (CODE_CONFLICT, TRANSIENCE_PERMANENT),
        (CODE_UNAUTHORIZED, TRANSIENCE_PERMANENT),
        (CODE_PROVENANCE_MISSING, TRANSIENCE_PERMANENT),
        (CODE_RATE_LIMITED, TRANSIENCE_TRANSIENT),
        (CODE_TRANSIENT, TRANSIENCE_TRANSIENT),
        (CODE_GENERIC, TRANSIENCE_UNKNOWN),
        ("ADOPTER_SPECIFIC", TRANSIENCE_UNKNOWN),
        ("", TRANSIENCE_UNKNOWN),
    ] {
        assert_eq!(transience_for_code(code), want, "code {code:?}");
    }
}

// --- wrap ---------------------------------------------------------------

#[test]
fn wrap_defaults_transience_from_code() {
    let base = std::io::Error::other("boom");
    assert_eq!(
        CliError::wrap(&base, CODE_CONFLICT, 4).transience,
        TRANSIENCE_PERMANENT
    );
    assert_eq!(
        CliError::wrap(&base, CODE_RATE_LIMITED, 64).transience,
        TRANSIENCE_TRANSIENT
    );
    assert_eq!(
        CliError::wrap(&base, CODE_GENERIC, 1).transience,
        TRANSIENCE_UNKNOWN
    );
    assert_eq!(CliError::wrap(&base, CODE_CONFLICT, 4).message, "boom");
}

// --- with_transience ----------------------------------------------------

#[test]
fn with_transience_sets_and_keeps_other_fields() {
    let orig = CliError {
        code: "SHARED".to_string(),
        message: "m".to_string(),
        exit_code: 9,
        ..CliError::default()
    };
    let got = orig.clone().with_transience(TRANSIENCE_TRANSIENT);
    assert_eq!(got.transience, TRANSIENCE_TRANSIENT);
    // Cloned source stays untouched.
    assert_eq!(orig.transience, "");
    assert_eq!(got.code, orig.code);
    assert_eq!(got.message, orig.message);
    assert_eq!(got.exit_code, orig.exit_code);
}

// --- render_error -------------------------------------------------------

#[test]
fn render_error_structured_always_carries_transience() {
    let bare = CliError {
        code: "ADOPTER_SPECIFIC".to_string(),
        message: "m".to_string(),
        exit_code: 9,
        ..CliError::default()
    };
    let got: Value = serde_json::from_str(&render("json", &bare)).expect("json");
    assert_eq!(got["transience"], TRANSIENCE_UNKNOWN);

    assert!(render("yaml", &bare).contains("transience: unknown"));

    // Input envelope is not mutated.
    assert_eq!(bare.transience, "");

    // An explicit class renders untouched.
    let got: Value =
        serde_json::from_str(&render("json", &CliError::rate_limited("budget"))).expect("json");
    assert_eq!(got["transience"], TRANSIENCE_TRANSIENT);
}

#[test]
fn render_error_json_wire_round_trip() {
    let out = render("json", &CliError::provenance_missing("/email"));
    let got: Value = serde_json::from_str(&out).expect("json");
    assert_eq!(got["code"], CODE_PROVENANCE_MISSING);
    assert_eq!(got["cause"], "/email");
    assert_eq!(got["exit_code"], 65);
    assert_eq!(got["transience"], TRANSIENCE_PERMANENT);

    // Empty optionals stay off the wire (omitempty parity), and the
    // wire form deserializes back into the same envelope.
    let out = render("json", &CliError::transient("upstream timeout"));
    let got: Value = serde_json::from_str(&out).expect("json");
    let mut keys: Vec<&str> = got
        .as_object()
        .expect("object")
        .keys()
        .map(String::as_str)
        .collect();
    keys.sort_unstable();
    assert_eq!(keys, ["code", "exit_code", "message", "transience"]);
    let back: CliError = serde_json::from_str(&out).expect("decode");
    assert_eq!(back, CliError::transient("upstream timeout"));
}

#[test]
fn render_error_yaml_wire_round_trip() {
    let out = render("yaml", &CliError::transient("upstream timeout"));
    let back: CliError = serde_yaml::from_str(&out).expect("decode");
    assert_eq!(back, CliError::transient("upstream timeout"));
}

#[test]
fn render_error_plain_lists_each_field() {
    let err = CliError {
        code: "NOT_FOUND".to_string(),
        message: "missing thing".to_string(),
        cause: "root".to_string(),
        suggested_fix: "try --all".to_string(),
        alternatives: vec!["other".to_string()],
        exit_code: 3,
        ..CliError::default()
    };
    assert_eq!(
        render("table", &err),
        "NOT_FOUND: missing thing\nCause: root\nFix: try --all\nAlternative: other\n"
    );
    // Bare message renders without a code prefix.
    let bare = CliError {
        message: "just text".to_string(),
        exit_code: 1,
        ..CliError::default()
    };
    assert_eq!(render("", &bare), "just text\n");
}

// --- Display / Error ----------------------------------------------------

#[test]
fn display_matches_go_error_string() {
    let e = CliError::not_found("missing thing");
    let s = e.to_string();
    assert!(s.contains("NOT_FOUND"));
    assert!(s.contains("missing thing"));
    // Satisfies std::error::Error.
    let _: &dyn std::error::Error = &e;
}
