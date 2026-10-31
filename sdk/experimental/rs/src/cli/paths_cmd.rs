//! `config path` and `config paths`: where a kit CLI's configuration
//! comes from. Ports Go `console/cli/config`. The host supplies the
//! resolver that walks the precedence chain; the commands render it.

use std::path::Path;
use std::sync::Arc;

use serde::Serialize;

use crate::output::CliError;

use super::run::Invocation;
use super::spec::{CommandSpec, FlagSpec};

/// One rung of the config precedence chain.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct ResolvedPath {
    /// The file path.
    pub path: String,
    /// Where the rung comes from (`cwd`, `project`, `workspace`,
    /// `user`, `system`, `default`).
    pub source: String,
    /// The rung's scope.
    pub scope: String,
    /// Whether the file exists on disk.
    pub exists: bool,
}

/// Walks the precedence chain for a directory, highest precedence
/// first. An empty chain means no source was discovered.
pub type ConfigResolver = Arc<dyn Fn(&Path) -> Vec<ResolvedPath> + Send + Sync>;

const NO_CONFIG: &str = "no config file found in resolution chain";

/// The `config` parent with `path` and `paths`, and a persistent
/// `--format text|json|yaml` of its own (default text).
pub fn config_command(tool: &str, resolver: ConfigResolver) -> CommandSpec {
    CommandSpec::new("config")
        .about(format!("Inspect {tool} configuration"))
        .flag(
            FlagSpec::string("format")
                .help("Output format: text|json|yaml")
                .default_value("text")
                .persistent(),
        )
        .subcommand(path_command(tool, resolver.clone()))
        .subcommand(paths_command(tool, resolver))
}

fn format_of(inv: &Invocation) -> Result<String, CliError> {
    let f = inv
        .given
        .get("format")
        .and_then(|v| v.last())
        .cloned()
        .unwrap_or_else(|| "text".into());
    match f.as_str() {
        "text" | "json" | "yaml" => Ok(f),
        other => Err(CliError::generic(format!(
            "unknown --format {other:?} (want text|json|yaml)"
        ))),
    }
}

fn cwd_of(inv: &Invocation) -> Result<std::path::PathBuf, CliError> {
    match inv.flag("from").map(str::trim).filter(|s| !s.is_empty()) {
        Some(from) => Ok(from.into()),
        None => std::env::current_dir().map_err(|e| CliError::generic(format!("resolve cwd: {e}"))),
    }
}

fn from_flag() -> FlagSpec {
    FlagSpec::string("from").help("Resolve from this directory instead of the working directory")
}

fn path_command(tool: &str, resolver: ConfigResolver) -> CommandSpec {
    CommandSpec::new("path")
        .about(format!(
            "Print the highest-precedence existing {tool} config file"
        ))
        .long_about(format!(
            "Print the single config file that {tool} would load.\n\n\
             The resolution chain is walked highest-precedence first (cwd, project,\n\
             workspace, user, system, default). The first rung whose file exists is\n\
             printed. If no rung exists, the command prints \"{NO_CONFIG}\"\n\
             to stderr and exits 1."
        ))
        .side_effect("read")
        .annotate("kit/idempotent", "yes")
        .flag(from_flag())
        .handler(move |inv, out, _| {
            let format = format_of(inv)?;
            let chain = resolver(&cwd_of(inv)?);
            let top = chain
                .iter()
                .find(|p| p.exists)
                .ok_or_else(|| CliError::generic(NO_CONFIG))?;
            let io = |e: std::io::Error| CliError::generic(e.to_string());
            match format.as_str() {
                "text" => writeln!(out, "{}", top.path).map_err(io),
                "json" => writeln!(out, "{}", pretty(top)?).map_err(io),
                _ => out.write_all(yaml(top)?.as_bytes()).map_err(io),
            }
        })
}

fn paths_command(tool: &str, resolver: ConfigResolver) -> CommandSpec {
    CommandSpec::new("paths")
        .about(format!("Print the full {tool} config resolution chain"))
        .long_about(format!(
            "Print every rung of the {tool} config resolution\n\
             chain, highest-precedence first. Each entry includes the source label\n\
             (cwd|project|workspace|user|system|default), scope, file path, and\n\
             whether the file exists on disk.\n\n\
             In text format (default), one path is printed per line. JSON and YAML\n\
             emit the full ResolvedPath shape (path, source, scope, exists) so\n\
             callers can drive scripts off precedence metadata."
        ))
        .side_effect("read")
        .annotate("kit/idempotent", "yes")
        .flag(from_flag())
        .handler(move |inv, out, _| {
            let format = format_of(inv)?;
            let chain = resolver(&cwd_of(inv)?);
            let io = |e: std::io::Error| CliError::generic(e.to_string());
            match format.as_str() {
                "text" => {
                    for p in &chain {
                        writeln!(out, "{}", p.path).map_err(io)?;
                    }
                    Ok(())
                }
                "json" => writeln!(out, "{}", pretty(&chain)?).map_err(io),
                _ => out.write_all(yaml(&chain)?.as_bytes()).map_err(io),
            }
        })
}

fn pretty<T: Serialize + ?Sized>(v: &T) -> Result<String, CliError> {
    serde_json::to_string_pretty(v).map_err(|e| CliError::generic(e.to_string()))
}

fn yaml<T: Serialize + ?Sized>(v: &T) -> Result<String, CliError> {
    serde_norway::to_string(v).map_err(|e| CliError::generic(e.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn yaml_and_json_shapes() {
        let chain = vec![ResolvedPath {
            path: "/a.yaml".into(),
            source: "cwd".into(),
            scope: "local".into(),
            exists: false,
        }];
        assert_eq!(
            yaml(&chain).unwrap(),
            "- path: /a.yaml\n  source: cwd\n  scope: local\n  exists: false\n"
        );
        assert_eq!(
            pretty(&Vec::<ResolvedPath>::new()).unwrap(),
            "[]",
            "an empty chain stays an array"
        );
    }
}
