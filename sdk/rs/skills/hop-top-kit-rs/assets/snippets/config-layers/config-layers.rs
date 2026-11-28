//! Layered config into one typed struct. Precedence, lowest to highest:
//! struct defaults, system file, user file, project file, extra `-c`
//! files, `-c key=value` overrides. A missing system, user or project
//! file is skipped. Needs feature `config` and `serde` with `derive`.

use std::path::Path;

use hop_top_kit::config::{load, parse_overrides, Error, Options};
use serde::{Deserialize, Serialize};

// `#[serde(default)]` lets a partial file decode: absent keys keep the
// struct's own defaults.
#[derive(Serialize, Deserialize)]
#[serde(default)]
pub struct Cfg {
    pub name: String,
    pub port: i64,
    pub debug: bool,
}

impl Default for Cfg {
    fn default() -> Self {
        Cfg {
            name: "demo".into(),
            port: 8080,
            debug: false,
        }
    }
}

pub fn effective(user: &Path, project: &Path, set: &[&str]) -> Result<Cfg, Error> {
    let opts = Options {
        user_config_path: user.into(),
        project_config_path: project.into(),
        overrides: parse_overrides(set)?,
        ..Options::default()
    };
    let mut cfg = Cfg::default();
    load(&mut cfg, &opts)?;
    Ok(cfg)
}

fn main() {
    // The conventional cascade for a tool: nearest project file, the
    // user's config home, then /etc.
    let mut cfg = Cfg::default();
    let opts = hop_top_kit::config::options_for_tool("demo");
    load(&mut cfg, &opts).expect("load config");
    println!("{} listens on {}", cfg.name, cfg.port);
}
