//! The CLI and MCP examples run as real processes. Cargo sets no
//! `CARGO_BIN_EXE_*` for examples and builds them only for a whole-crate
//! `cargo test`, so a missing binary is built here, then found next to
//! the test executable.

#![cfg(all(feature = "cli", feature = "config", feature = "mcp"))]

use std::io::Write;
use std::path::PathBuf;
use std::process::{Command, Stdio};

fn example(name: &str) -> PathBuf {
    let mut p = std::env::current_exe().unwrap();
    p.pop(); // deps
    p.pop(); // debug
    p.push("examples");
    p.push(name);
    if !p.exists() {
        let st = Command::new(env!("CARGO"))
            .args(["build", "--example", name, "--features", "cli,config,mcp"])
            .status()
            .expect("cargo");
        assert!(st.success(), "build example {name}");
    }
    p
}

fn run(name: &str, args: &[&str], stdin: &str) -> (i32, String, String) {
    let mut child = Command::new(example(name))
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("example built");
    child
        .stdin
        .take()
        .unwrap()
        .write_all(stdin.as_bytes())
        .unwrap();
    let out = child.wait_with_output().unwrap();
    (
        out.status.code().unwrap_or(-1),
        String::from_utf8(out.stdout).unwrap(),
        String::from_utf8(out.stderr).unwrap(),
    )
}

#[test]
fn cli_example_lists_and_greets() {
    let (code, out, _) = run("cli", &["list", "--format", "json"], "");
    assert_eq!(code, 0, "{out}");
    let v: serde_json::Value = serde_json::from_str(&out).unwrap();
    assert_eq!(v[1]["name"], "beta");

    let (code, out, _) = run("cli", &["greet", "--name", "Ada", "hi"], "");
    assert_eq!((code, out.as_str()), (0, "hi, Ada\n"));

    let (code, out, _) = run("cli", &["--help"], "");
    assert_eq!(code, 0);
    assert!(out.contains("COMMANDS:") && out.contains("greet"), "{out}");
}

#[test]
fn cli_example_verbose_logs_to_stderr() {
    let (_, out, err) = run("cli", &["greet", "-V"], "");
    assert_eq!(out, "hello, world\n");
    assert!(err.contains("DEBU greeting name=world"), "{err}");
    let (_, _, err) = run("cli", &["greet"], "");
    assert!(err.is_empty(), "{err}");
}

#[test]
fn cli_example_rejects_an_unknown_flag() {
    let (code, _, err) = run("cli", &["list", "--bogus"], "");
    assert_eq!(code, 2, "{err}");
}

#[test]
fn mcp_example_serves_tools_over_stdio() {
    let input = concat!(
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#,
        "\n",
        r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"greet","arguments":{"name":"Ada"}}}"#,
        "\n"
    );
    let (code, out, err) = run("mcp", &[], input);
    assert_eq!(code, 0, "{err}");
    assert!(
        out.contains("\"ping\"") && out.contains("\"greet\""),
        "{out}"
    );
    assert!(out.contains("hello, Ada"), "{out}");
}
