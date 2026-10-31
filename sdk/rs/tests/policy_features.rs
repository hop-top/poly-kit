//! Port of go/runtime/policy's deps_test.go for the Rust feature graph:
//! the library half must not pull the terminal-UI stack. `policy` has to
//! stay off `output`/`cli` (clap, comfy-table); the CONFLICT envelope is
//! compiled only when the consumer enables `output` itself.

#[test]
fn policy_feature_does_not_enable_the_terminal_ui_stack() {
    let manifest =
        std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/Cargo.toml")).unwrap();
    let line = manifest
        .lines()
        .find(|l| l.starts_with("policy = ["))
        .expect("policy feature declared on one line");
    for forbidden in ["output", "cli", "clap", "comfy-table", "tui"] {
        assert!(
            !line.contains(&format!("\"{forbidden}\""))
                && !line.contains(&format!("dep:{forbidden}")),
            "policy must not enable {forbidden}: {line}"
        );
    }
}
