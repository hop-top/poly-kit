//! The persisted alias table and the commands over it. Ports Go
//! `console/alias.Store` and `Root.AliasCmd` / `AliasesCmd` /
//! `LoadAliasStore`.

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use serde_json::{json, Value};

use crate::output::CliError;

use super::alias::AliasError;
use super::plan::{Effect, Plan};
use super::root::Root;
use super::spec::{ArgSpec, CommandSpec};

/// Command aliases backed by a YAML file (`name: target path`).
#[derive(Clone, Debug)]
pub struct AliasStore {
    path: PathBuf,
    aliases: BTreeMap<String, String>,
}

impl AliasStore {
    /// A store reading and writing `path`; the file need not exist.
    pub fn new(path: impl Into<PathBuf>) -> AliasStore {
        AliasStore {
            path: path.into(),
            aliases: BTreeMap::new(),
        }
    }

    /// Reads the file. A missing file leaves the store empty.
    pub fn load(&mut self) -> Result<(), String> {
        let shown = self.path.display();
        let data = match std::fs::read_to_string(&self.path) {
            Ok(d) => d,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(format!("alias: read {shown}: {e}")),
        };
        self.aliases = if data.trim().is_empty() {
            BTreeMap::new()
        } else {
            serde_norway::from_str(&data).map_err(|e| format!("alias: parse {shown}: {e}"))?
        };
        Ok(())
    }

    /// Writes the table, creating parent directories.
    pub fn save(&self) -> Result<(), String> {
        if let Some(dir) = self.path.parent().filter(|d| !d.as_os_str().is_empty()) {
            std::fs::create_dir_all(dir)
                .map_err(|e| format!("alias: mkdir {}: {e}", dir.display()))?;
        }
        let data =
            serde_norway::to_string(&self.aliases).map_err(|e| format!("alias: marshal: {e}"))?;
        std::fs::write(&self.path, data)
            .map_err(|e| format!("alias: write {}: {e}", self.path.display()))
    }

    /// Adds or updates an alias: the name non-empty without
    /// whitespace, the target non-empty.
    pub fn set(&mut self, name: &str, target: &str) -> Result<(), String> {
        if name.is_empty() || name.contains([' ', '\t', '\n']) {
            return Err(format!(
                "alias: name {name:?} must be non-empty without whitespace"
            ));
        }
        if target.is_empty() {
            return Err("alias: target must be non-empty".into());
        }
        self.aliases.insert(name.into(), target.into());
        Ok(())
    }

    /// Deletes an alias; an error when it does not exist.
    pub fn remove(&mut self, name: &str) -> Result<(), String> {
        self.aliases
            .remove(name)
            .map(|_| ())
            .ok_or_else(|| format!("alias: {name:?} not found"))
    }

    /// The target of `name`.
    pub fn get(&self, name: &str) -> Option<&str> {
        self.aliases.get(name).map(String::as_str)
    }

    /// A copy of the table.
    pub fn all(&self) -> BTreeMap<String, String> {
        self.aliases.clone()
    }

    /// Replaces `args[0]` with its target (split on spaces) when it
    /// names an alias.
    pub fn expand(&self, args: &[String]) -> Vec<String> {
        match args.first().and_then(|a| self.aliases.get(a)) {
            Some(t) => t
                .split_whitespace()
                .map(String::from)
                .chain(args[1..].iter().cloned())
                .collect(),
            None => args.to_vec(),
        }
    }

    /// The file the store reads and writes.
    pub fn path(&self) -> &Path {
        &self.path
    }
}

/// A store shared between the alias commands and their owner.
pub type SharedAliasStore = Arc<Mutex<AliasStore>>;

fn entries(all: &BTreeMap<String, String>) -> Value {
    Value::Array(
        all.iter()
            .map(|(a, t)| json!({"alias": a, "target": t}))
            .collect(),
    )
}

fn lock(s: &SharedAliasStore) -> std::sync::MutexGuard<'_, AliasStore> {
    s.lock().unwrap_or_else(|e| e.into_inner())
}

fn list_all(store: &SharedAliasStore, runtime: &BTreeMap<String, String>) -> Value {
    let mut all = lock(store).all();
    for (k, v) in runtime {
        all.entry(k.clone()).or_insert_with(|| v.clone());
    }
    entries(&all)
}

/// The hidden `aliases` command: lists the root's active aliases.
pub fn aliases_command() -> CommandSpec {
    CommandSpec::new("aliases")
        .about("List active command aliases")
        .hidden()
        .side_effect("read")
        .annotate("kit/idempotent", "yes")
        .handler(|inv, out, _| inv.render(out, &entries(&inv.aliases)))
}

/// The `alias` group over `store`: `list` (also the bare command),
/// `add <name> <target...>` and `delete <name>` (`remove`, `rm`).
pub fn alias_command(store: SharedAliasStore) -> CommandSpec {
    let list = {
        let s = store.clone();
        move |inv: &super::run::Invocation,
              out: &mut dyn std::io::Write,
              _: &mut dyn std::io::Write| { inv.render(out, &list_all(&s, &inv.aliases)) }
    };
    let add_store = store.clone();
    let del_store = store.clone();
    CommandSpec::new("alias")
        .about("Manage command aliases")
        .long_about(
            "Manage user-defined command aliases stored in a YAML file. Aliases are also \
             registered as runtime shims so the aliased name dispatches to its target \
             subcommand. Invoking `<tool> alias` with no subcommand lists the active \
             aliases, equivalent to `<tool> alias list`.",
        )
        .side_effect("read")
        .annotate("kit/idempotent", "yes")
        .annotate("kit/top-level-verb", "true")
        .annotate("kit/reserved", "true")
        .handler(list.clone())
        .subcommand(
            CommandSpec::new("list")
                .about("List aliases")
                .long_about(
                    "Print the active alias table, both YAML-backed entries and \
                     runtime-registered shims, in the active --format. Read-only: no state \
                     mutation.",
                )
                .side_effect("read")
                .handler(list),
        )
        .subcommand(
            CommandSpec::new("add")
                .about("Add or update an alias")
                .long_about(
                    "Persist a new alias (or replace an existing one) in the YAML store and \
                     register a runtime shim so subsequent invocations dispatch to <target>. \
                     <target> may include flags, captured as a single shell-quoted string.",
                )
                .side_effect("write-local")
                .arg(ArgSpec::new("name").required())
                .arg(ArgSpec::new("target").required().variadic())
                .handler(move |inv, out, _| {
                    let (Some(name), target) = (
                        inv.args.first(),
                        inv.args[1.min(inv.args.len())..].join(" "),
                    ) else {
                        return Err(CliError::usage("alias add needs a name and a target"));
                    };
                    if target.is_empty() {
                        return Err(CliError::usage("alias add needs a name and a target"));
                    }
                    if inv.dry_run {
                        let kind = if lock(&add_store).get(name).is_some() {
                            "update"
                        } else {
                            "create"
                        };
                        return inv.render_plan(
                            "alias add",
                            Plan {
                                args: BTreeMap::from([
                                    ("name".into(), name.clone()),
                                    ("target".into(), target.clone()),
                                ]),
                                effects: vec![Effect {
                                    kind: kind.into(),
                                    target: format!("alias:{name}"),
                                    reversible: true,
                                    detail: format!("dispatch to {target}"),
                                }],
                                ..Plan::default()
                            },
                            out,
                        );
                    }
                    {
                        let mut s = lock(&add_store);
                        s.set(name, &target).map_err(CliError::generic)?;
                        s.save().map_err(CliError::generic)?;
                    }
                    inv.render(out, &json!({"alias": name, "target": target}))
                }),
        )
        .subcommand(
            CommandSpec::new("delete")
                .alias("remove")
                .alias("rm")
                .about("Delete an alias")
                .long_about(
                    "Remove an alias from the YAML store and tear down its runtime shim. The \
                     legacy `remove`/`rm` aliases keep working through the deprecation window.",
                )
                .side_effect("write-local")
                .arg(ArgSpec::new("name").required())
                .handler(move |inv, out, _| {
                    let name = inv.args.first().cloned().unwrap_or_default();
                    let target = lock(&del_store).get(&name).map(String::from);
                    if inv.dry_run {
                        let mut plan = Plan {
                            args: BTreeMap::from([("name".into(), name.clone())]),
                            ..Plan::default()
                        };
                        match &target {
                            Some(t) => plan.effects.push(Effect {
                                kind: "delete".into(),
                                target: format!("alias:{name}"),
                                reversible: true,
                                detail: format!("was {t}"),
                            }),
                            None => plan
                                .warnings
                                .push(format!("no alias {name}: delete is a no-op")),
                        }
                        return inv.render_plan("alias delete", plan, out);
                    }
                    {
                        let mut s = lock(&del_store);
                        s.remove(&name).map_err(CliError::generic)?;
                        s.save().map_err(CliError::generic)?;
                    }
                    inv.render(
                        out,
                        &json!({"alias": name, "target": target.unwrap_or_default()}),
                    )
                }),
        )
}

impl Root {
    /// Registers every alias of `store` as a root alias, in name order.
    /// Unknown targets and collisions are collected into one error.
    pub fn load_alias_store(&mut self, store: &AliasStore) -> Result<(), AliasError> {
        let mut errs = Vec::new();
        for (name, target) in store.all() {
            if let Err(e) = self.alias(&name, &target) {
                errs.push(format!("{name} -> {target}: {e}"));
            }
        }
        if errs.is_empty() {
            Ok(())
        } else {
            Err(AliasError::new(format!(
                "alias errors:\n  {}",
                errs.join("\n  ")
            )))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn set_validates_name_and_target() {
        let mut s = AliasStore::new("/nonexistent/aliases.yaml");
        assert_eq!(
            s.set("a b", "x").unwrap_err(),
            "alias: name \"a b\" must be non-empty without whitespace"
        );
        assert_eq!(
            s.set("", "x").unwrap_err(),
            "alias: name \"\" must be non-empty without whitespace"
        );
        assert_eq!(
            s.set("a", "").unwrap_err(),
            "alias: target must be non-empty"
        );
        s.set("a", "x y").unwrap();
        assert_eq!(s.get("a"), Some("x y"));
        assert_eq!(s.remove("zz").unwrap_err(), "alias: \"zz\" not found");
    }

    #[test]
    fn load_save_round_trip_and_missing_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("nested/aliases.yaml");
        let mut s = AliasStore::new(&path);
        s.load().unwrap();
        assert!(s.all().is_empty());
        s.set("ll", "ls").unwrap();
        s.set("d", "deploy --force").unwrap();
        s.save().unwrap();
        assert_eq!(
            std::fs::read_to_string(&path).unwrap(),
            "d: deploy --force\nll: ls\n"
        );
        let mut again = AliasStore::new(&path);
        again.load().unwrap();
        assert_eq!(again.all(), s.all());
        std::fs::write(&path, ": [\n").unwrap();
        assert!(again.load().unwrap_err().starts_with("alias: parse "));
    }

    #[test]
    fn expand_replaces_the_first_word() {
        let mut s = AliasStore::new("x");
        s.set("d", "deploy --force").unwrap();
        let a = |v: &[&str]| v.iter().map(|s| s.to_string()).collect::<Vec<_>>();
        assert_eq!(
            s.expand(&a(&["d", "prod"])),
            a(&["deploy", "--force", "prod"])
        );
        assert_eq!(s.expand(&a(&["x"])), a(&["x"]));
        assert!(s.expand(&[]).is_empty());
    }
}
