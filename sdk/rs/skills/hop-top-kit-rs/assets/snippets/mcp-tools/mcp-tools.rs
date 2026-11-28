//! Commands as MCP tools over stdio: one JSON-RPC message per line until
//! the host closes stdin. Needs feature `mcp`.

use hop_top_kit::mcp::{Bridge, CallResult, Leaf, MountOptions, Surface};

pub fn surface() -> Surface {
    let bridge = Bridge::new()
        .leaf(Leaf::new(&["ping"], "Ping the server", |_| {
            Ok(CallResult::ok("pong\n"))
        }))
        .leaf(Leaf::new(&["greet"], "Greet someone", |args| {
            let name = args.get("name").and_then(|v| v.as_str()).unwrap_or("world");
            Ok(CallResult::ok(format!("hello, {name}\n")))
        }));
    Surface::mount(bridge, MountOptions::default()).expect("mount")
}

fn main() {
    // Protocol on stdout, the log on stderr.
    let stdin = std::io::stdin();
    surface()
        .serve_stdio(stdin.lock(), std::io::stdout(), std::io::stderr())
        .expect("stdio");
}
