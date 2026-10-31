//! A kit CLI root with two commands: the persistent flags (`--format`,
//! `--quiet`, `-V`, `--no-color`, ...), help groups and structured
//! output, with no parser type in sight.
//!
//! ```text
//! cargo run --example cli --features cli,config -- --help
//! cargo run --example cli --features cli,config -- list
//! cargo run --example cli --features cli,config -- list --format json
//! cargo run --example cli --features cli,config -- greet --name Ada -V
//! ```

use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, Root};
use serde_json::json;

fn root() -> Root {
    let mut cfg = Config::new(
        "demo",
        env!("CARGO_PKG_VERSION"),
        "A kit CLI in a few lines",
    );
    // The example declares no side-effect tiers or long help, which the
    // boot-time validation would ask for in a shipped tool.
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
                // -V raises the log level; --quiet floors it.
                let log = inv.logger();
                let name = inv.flag("name").unwrap_or("world");
                log.debug("greeting", &[("name", &name)]);
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
