//! Runs the code snippets shipped inside the agent skill under
//! `skills/hop-top-kit-rs/assets/snippets/`, so they cannot rot. Each
//! snippet file is compiled into this test crate as a module (no manifest
//! target, and `skills/` stays outside the published crate) and its output
//! is compared with the `<name>.expected` file beside it.

#![allow(dead_code)] // each snippet's `main` is for the reader, not for us

#[allow(unused_imports)]
use std::fmt::Write as _;

/// `expected` is the snippet's `.expected` file; a mismatch prints both.
fn check(name: &str, actual: &str, expected: &str) {
    assert_eq!(
        actual, expected,
        "snippet {name}: output differs from {name}.expected"
    );
}

#[cfg(all(feature = "cli", feature = "config"))]
#[path = "../skills/hop-top-kit-rs/assets/snippets/cli-root/cli-root.rs"]
mod cli_root;

#[cfg(all(feature = "cli", feature = "config"))]
#[test]
fn snippet_cli_root() {
    let root = cli_root::root();
    let mut got = String::new();
    for args in [
        &["list", "--format", "json"][..],
        &["greet", "--name", "Ada", "hi"],
        &["list", "--bogus"],
    ] {
        let (mut out, mut err) = (Vec::new(), Vec::new());
        let argv = std::iter::once("demo").chain(args.iter().copied());
        let code = root.run(argv, &mut out, &mut err);
        writeln!(got, "$ demo {}", args.join(" ")).unwrap();
        writeln!(got, "exit {code}").unwrap();
        got.push_str(&String::from_utf8(out).unwrap());
    }
    check(
        "cli-root",
        &got,
        include_str!("../skills/hop-top-kit-rs/assets/snippets/cli-root/cli-root.expected"),
    );
}

#[cfg(feature = "config")]
#[path = "../skills/hop-top-kit-rs/assets/snippets/config-layers/config-layers.rs"]
mod config_layers;

#[cfg(feature = "config")]
#[test]
fn snippet_config_layers() {
    let dir = std::env::temp_dir().join(format!("kit-skill-config-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let (user, project) = (dir.join("user.yaml"), dir.join("project.yaml"));
    std::fs::write(&user, "name: from-user\nport: 9000\n").unwrap();
    std::fs::write(&project, "port: 9100\ndebug: true\n").unwrap();
    let missing = dir.join("absent.yaml");

    let mut got = String::new();
    for (label, user, set) in [
        ("user + project", &user, &[][..]),
        ("user + project + override", &user, &["name=cli"][..]),
        ("project only (user file absent)", &missing, &[][..]),
    ] {
        let c = config_layers::effective(user, &project, set).unwrap();
        writeln!(
            got,
            "{label}: name={} port={} debug={}",
            c.name, c.port, c.debug
        )
        .unwrap();
    }
    std::fs::remove_dir_all(&dir).unwrap();
    check(
        "config-layers",
        &got,
        include_str!(
            "../skills/hop-top-kit-rs/assets/snippets/config-layers/config-layers.expected"
        ),
    );
}

#[cfg(feature = "mcp")]
#[path = "../skills/hop-top-kit-rs/assets/snippets/mcp-tools/mcp-tools.rs"]
mod mcp_tools;

#[cfg(feature = "mcp")]
#[test]
fn snippet_mcp_tools() {
    let input = concat!(
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#,
        "\n",
        r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"greet","arguments":{"name":"Ada"}}}"#,
        "\n"
    );
    let (mut out, mut log) = (Vec::new(), Vec::new());
    mcp_tools::surface()
        .serve_stdio(input.as_bytes(), &mut out, &mut log)
        .unwrap();
    // Requests may be answered concurrently: order replies by id.
    let mut replies: Vec<serde_json::Value> = String::from_utf8(out)
        .unwrap()
        .lines()
        .map(|l| serde_json::from_str(l).unwrap())
        .collect();
    replies.sort_by_key(|v| v["id"].as_i64());
    let mut got = String::new();
    let mut names: Vec<&str> = replies[0]["result"]["tools"]
        .as_array()
        .unwrap()
        .iter()
        .map(|t| t["name"].as_str().unwrap())
        .collect();
    names.sort_unstable();
    writeln!(got, "tools: {}", names.join(" ")).unwrap();
    writeln!(
        got,
        "greet: {}",
        replies[1]["result"]["content"][0]["text"]
            .as_str()
            .unwrap()
            .trim_end()
    )
    .unwrap();
    check(
        "mcp-tools",
        &got,
        include_str!("../skills/hop-top-kit-rs/assets/snippets/mcp-tools/mcp-tools.expected"),
    );
}
