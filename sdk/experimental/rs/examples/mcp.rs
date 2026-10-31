//! Mounts a command tree as MCP tools and serves it over stdio, the
//! transport a host that spawns the tool speaks: one JSON-RPC message
//! per line, until the host closes stdin.
//!
//! ```text
//! echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
//!   | cargo run --example mcp --features mcp
//! echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"greet","arguments":{"name":"Ada"}}}' \
//!   | cargo run --example mcp --features mcp
//! ```

use hop_top_kit::mcp::{Bridge, CallResult, Leaf, MountOptions, Surface};

fn main() {
    let bridge = Bridge::new()
        .leaf(Leaf::new(&["ping"], "Ping the server", |_| {
            Ok(CallResult::ok("pong\n"))
        }))
        .leaf(Leaf::new(&["greet"], "Greet someone", |args| {
            let name = args.get("name").and_then(|v| v.as_str()).unwrap_or("world");
            Ok(CallResult::ok(format!("hello, {name}\n")))
        }));
    let surface = Surface::mount(bridge, MountOptions::default()).expect("mount");

    // Protocol on stdout, the log on stderr.
    let stdin = std::io::stdin();
    surface
        .serve_stdio(stdin.lock(), std::io::stdout(), std::io::stderr())
        .expect("stdio");
}
