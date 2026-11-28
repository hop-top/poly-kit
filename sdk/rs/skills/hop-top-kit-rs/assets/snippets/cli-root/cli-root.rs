//! Smallest kit CLI: two commands, the persistent flags (`--format`,
//! `--quiet`, `-V`, `--no-color`), help groups and structured output,
//! with no parser type in sight. Needs features `cli` and `config`, and
//! `serde_json` for the `json!` payload.

#![allow(clippy::result_large_err)]

use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, Root};
use serde_json::json;

pub fn root() -> Root {
    let mut cfg = Config::new(
        "demo",
        env!("CARGO_PKG_VERSION"),
        "A kit CLI in a few lines",
    );
    // A shipped tool leaves validation on and declares side-effect tiers
    // and long help; this sketch skips both.
    cfg.disable_validate = true;
    let mut root = Root::with_config(cfg);

    root.add_command(
        CommandSpec::new("list")
            .about("List the items")
            .handler(|inv, out, _err| {
                // Rendered in the active --format (table by default).
                inv.render(
                    out,
                    &json!([
                        {"name": "alpha", "size": 1},
                        {"name": "beta", "size": 2},
                    ]),
                )
            }),
    );

    root.add_command(
        CommandSpec::new("greet")
            .about("Greet someone")
            .arg(ArgSpec::new("greeting").help("Greeting word"))
            .flag(FlagSpec::string("name").short('n').help("Who to greet"))
            .handler(|inv, out, _err| {
                let name = inv.flag("name").unwrap_or("world");
                let word = inv.args.first().map_or("hello", String::as_str);
                writeln!(out, "{word}, {name}").ok();
                Ok(())
            }),
    );
    root
}

fn main() {
    let (mut out, mut err) = (std::io::stdout(), std::io::stderr());
    std::process::exit(root().run(std::env::args(), &mut out, &mut err));
}
