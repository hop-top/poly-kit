//! The CLI and MCP examples run as real processes. Cargo sets no
//! `CARGO_BIN_EXE_*` for examples, and the test executable's own location
//! says nothing reliable about where examples land (the build directory
//! and the artifact directory can differ), so each example is built here
//! and its path taken from cargo's own artifact report.

#![cfg(all(feature = "cli", feature = "config", feature = "mcp"))]

use std::io::Write;
use std::path::PathBuf;
use std::process::{Command, Stdio};

fn example(name: &str) -> PathBuf {
    let out = Command::new(env!("CARGO"))
        .args([
            "build",
            "--example",
            name,
            "--features",
            "cli,config,mcp",
            "--message-format=json-render-diagnostics",
        ])
        .stderr(Stdio::inherit())
        .output()
        .expect("cargo");
    assert!(out.status.success(), "build example {name}");
    let stdout = String::from_utf8(out.stdout).unwrap();
    stdout
        .lines()
        .filter(|l| l.contains(r#""reason":"compiler-artifact""#))
        .filter(|l| l.contains(r#""kind":["example"]"#))
        .filter(|l| l.contains(&format!(r#""name":"{name}""#)))
        .find_map(executable)
        .unwrap_or_else(|| panic!("cargo reported no executable for example {name}"))
}

// executable extracts the "executable" string from one cargo JSON message,
// undoing the JSON escapes a path can carry (backslashes on Windows).
fn executable(line: &str) -> Option<PathBuf> {
    let rest = &line[line.find(r#""executable":""#)? + r#""executable":""#.len()..];
    let mut path = String::new();
    let mut chars = rest.chars();
    while let Some(c) = chars.next() {
        match c {
            '"' => return Some(PathBuf::from(path)),
            '\\' => path.push(chars.next()?),
            c => path.push(c),
        }
    }
    None
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
