//! Ports of the Go `console/cli` tests the shared corpus cannot say:
//! `-C/--chdir` (`chdir_test.go`), `-c/--config` layering
//! (`configargs_test.go`), flag validators, the validation failure
//! modes and the alias commands. The corpus in `contracts/cli-root-v1`
//! pins what they print; these pin the seams around it.

#![cfg(all(feature = "cli", feature = "config"))]
#![allow(clippy::result_large_err)]

use std::sync::{Arc, Mutex};

use hop_top_kit::cli::{
    alias_command, AliasStore, ChdirResolver, CommandSpec, Config, Disable, FlagSpec, Root,
    ValidationFailureMode,
};
use hop_top_kit::output::CliError;

/// The process directory is shared: tests that change it, or read it,
/// run one at a time.
static CWD: Mutex<()> = Mutex::new(());

fn cfg() -> Config {
    let mut c = Config::new("t", "0.0.1", "t");
    c.disable_validate = true;
    c
}

fn here() -> CommandSpec {
    CommandSpec::new("here")
        .about("cwd")
        .side_effect("read")
        .handler(|_, out, _| {
            write!(out, "{}", std::env::current_dir().unwrap().display()).unwrap();
            Ok(())
        })
}

fn run(root: &Root, args: &[&str]) -> (i32, String, String) {
    let (mut out, mut err) = (Vec::new(), Vec::new());
    let argv: Vec<String> = std::iter::once("t")
        .chain(args.iter().copied())
        .map(Into::into)
        .collect();
    let code = match root.execute(&argv, &mut out, &mut err) {
        Ok(()) => 0,
        Err(e) => e.exit_code,
    };
    (
        code,
        String::from_utf8(out).unwrap(),
        String::from_utf8(err).unwrap(),
    )
}

fn real(p: &str) -> std::path::PathBuf {
    std::fs::canonicalize(p).unwrap()
}

// --- chdir_test.go ---

#[test]
fn chdir_target_is_dir_and_execute_restores_the_directory() {
    let _g = CWD.lock().unwrap_or_else(|e| e.into_inner());
    let dir = tempfile::tempdir().unwrap();
    let before = std::env::current_dir().unwrap();
    let mut root = Root::with_config(cfg());
    root.add_command(here());
    let (code, out, _) = run(&root, &["--chdir", dir.path().to_str().unwrap(), "here"]);
    assert_eq!(code, 0);
    assert_eq!(real(&out), real(dir.path().to_str().unwrap()));
    // Re-entrant: the run leaves the process where it found it.
    assert_eq!(std::env::current_dir().unwrap(), before);
}

#[test]
fn chdir_resolver_resolves_a_missing_target() {
    let _g = CWD.lock().unwrap_or_else(|e| e.into_inner());
    let resolved = tempfile::tempdir().unwrap();
    let want = resolved.path().to_path_buf();
    let mut c = cfg();
    c.chdir_resolver = Some(ChdirResolver::new(move |t| {
        assert_eq!(t, "shortname");
        Ok(want.clone())
    }));
    let mut root = Root::with_config(c);
    root.add_command(here());
    let (code, out, _) = run(&root, &["-C", "shortname", "here"]);
    assert_eq!(code, 0);
    assert_eq!(real(&out), real(resolved.path().to_str().unwrap()));
}

#[test]
fn chdir_resolver_error_is_the_error() {
    let _g = CWD.lock().unwrap_or_else(|e| e.into_inner());
    let mut c = cfg();
    c.chdir_resolver = Some(ChdirResolver::new(|_| Err("not found in registry".into())));
    let mut root = Root::with_config(c);
    root.add_command(here());
    let (code, _, err) = run(&root, &["-C", "bogus", "here"]);
    assert_eq!(code, 1);
    assert!(err.contains("not found in registry"), "{err}");
}

#[test]
fn chdir_missing_without_resolver_names_the_target() {
    let _g = CWD.lock().unwrap_or_else(|e| e.into_inner());
    let mut root = Root::with_config(cfg());
    root.add_command(here());
    let (code, _, err) = run(&root, &["-C", "/nope/does-not-exist-xyz", "here"]);
    assert_eq!(code, 1);
    assert!(
        err.contains("\"/nope/does-not-exist-xyz\"") && err.contains("not a directory"),
        "{err}"
    );
}

#[test]
fn chdir_empty_target_is_a_no_op() {
    let _g = CWD.lock().unwrap_or_else(|e| e.into_inner());
    let mut root = Root::with_config(cfg());
    root.add_command(here());
    let (code, out, _) = run(&root, &["-C", "", "here"]);
    assert_eq!(code, 0);
    assert_eq!(real(&out), std::env::current_dir().unwrap());
}

#[test]
fn chdir_disabled_omits_the_flag() {
    let mut c = cfg();
    c.disable = Disable {
        chdir: true,
        ..Disable::default()
    };
    let mut root = Root::with_config(c);
    root.add_command(here());
    let model = root.model();
    assert!(model.flag("chdir").is_none());
    let (code, _, err) = run(&root, &["-C", "x", "here"]);
    assert_eq!(code, 2, "{err}");
}

// --- configargs_test.go ---

type Seen = Arc<Mutex<Option<Result<String, String>>>>;

fn config_tool(marker: &str) -> (Root, Seen) {
    let seen: Seen = Arc::default();
    let mut c = cfg();
    c.project_marker = marker.into();
    let mut root = Root::with_config(c);
    let s = seen.clone();
    root.add_command(
        CommandSpec::new("show")
            .about("show")
            .side_effect("read")
            .handler(move |inv, _, _| {
                *s.lock().unwrap() = Some(match inv.config_args() {
                    Ok((paths, ov)) => Ok(format!("{paths:?} {:?}", ov.map(|m| m.len()))),
                    Err(e) => Err(e.to_string()),
                });
                Ok(())
            }),
    );
    (root, seen)
}

#[test]
fn config_args_surface_a_parse_error() {
    let (root, seen) = config_tool("");
    run(&root, &["-c", "no-equals-and-no-such-file", "show"]);
    let got = seen.lock().unwrap().take().unwrap();
    assert!(got.is_err(), "{got:?}");
}

#[test]
fn config_args_project_marker_resolves_a_directory() {
    let dir = tempfile::tempdir().unwrap();
    let marker = ".rlz/config.yaml";
    std::fs::create_dir_all(dir.path().join(".rlz")).unwrap();
    std::fs::write(dir.path().join(marker), "model: x\n").unwrap();
    let (root, seen) = config_tool(marker);
    run(&root, &["-c", dir.path().to_str().unwrap(), "show"]);
    let got = seen.lock().unwrap().take().unwrap().unwrap();
    assert!(got.contains(".rlz/config.yaml"), "{got}");
}

#[test]
fn config_args_missing_marker_names_the_resolved_path() {
    let dir = tempfile::tempdir().unwrap();
    let (root, seen) = config_tool(".rlz/config.yaml");
    run(&root, &["-c", dir.path().to_str().unwrap(), "show"]);
    let err = seen.lock().unwrap().take().unwrap().unwrap_err();
    assert!(err.contains(".rlz/config.yaml"), "{err}");
}

#[test]
fn layer_config_adds_files_after_and_overrides_last() {
    let dir = tempfile::tempdir().unwrap();
    let extra = dir.path().join("extra.yaml");
    std::fs::write(&extra, "a: 1\n").unwrap();
    let layered: Arc<Mutex<Option<hop_top_kit::config::Options>>> = Arc::default();
    let mut root = Root::with_config(cfg());
    let l = layered.clone();
    root.add_command(
        CommandSpec::new("show")
            .about("show")
            .side_effect("read")
            .handler(move |inv, _, _| {
                let base = hop_top_kit::config::Options {
                    extra_config_paths: vec!["first.yaml".into()],
                    ..Default::default()
                };
                *l.lock().unwrap() = Some(inv.layer_config(base).unwrap());
                Ok(())
            }),
    );
    run(
        &root,
        &["-c", extra.to_str().unwrap(), "-c", "a.b=2", "show"],
    );
    let o = layered.lock().unwrap().take().unwrap();
    assert_eq!(o.extra_config_paths.len(), 2);
    assert_eq!(o.extra_config_paths[0].to_str(), Some("first.yaml"));
    assert!(o.overrides.unwrap().contains_key("a"));
}

// --- flag validators ---

fn validated() -> (Root, Arc<Mutex<u32>>) {
    let ran = Arc::new(Mutex::new(0));
    let mut c = cfg();
    c.globals = vec![hop_top_kit::cli::GlobalFlag {
        name: "api-version".into(),
        usage: "version".into(),
        default: Some("bogus".into()),
        ..Default::default()
    }];
    let mut root = Root::with_config(c);
    let r = ran.clone();
    root.add_command(
        CommandSpec::new("go")
            .about("go")
            .side_effect("read")
            .flag(FlagSpec::string("mode"))
            .handler(move |_, _, _| {
                *r.lock().unwrap() += 1;
                Ok(())
            }),
    );
    (root, ran)
}

fn refuse(msg: &'static str) -> impl Fn(&str) -> Option<CliError> + Send + Sync {
    move |v| {
        (v == "bad").then(|| {
            let mut e = CliError::generic(msg);
            e.code = "INVALID".into();
            e.exit_code = 2;
            e
        })
    }
}

#[test]
fn validator_rejects_before_the_handler_and_renders_the_envelope() {
    let (mut root, ran) = validated();
    root.with_flag_validator("mode", refuse("mode is bad"));
    let (code, _, err) = run(&root, &["--format", "json", "go", "--mode", "bad"]);
    assert_eq!(code, 2);
    assert!(err.contains("\"code\": \"INVALID\""), "{err}");
    assert_eq!(*ran.lock().unwrap(), 0);
    let (code, ..) = run(&root, &["go", "--mode", "ok"]);
    assert_eq!(code, 0);
    assert_eq!(*ran.lock().unwrap(), 1);
}

#[test]
fn validator_ignores_defaults_and_unknown_names_and_sees_persistent_flags() {
    let (mut root, ran) = validated();
    // The default is "bogus" and rejected by this validator, but unset.
    root.with_flag_validator("api-version", |v| {
        (v == "bogus").then(|| CliError::usage("api-version must be semver"))
    });
    root.with_flag_validator("nosuch", refuse("never"));
    assert_eq!(run(&root, &["go"]).0, 0);
    // Set on the command line, the persistent flag is validated.
    let (code, _, err) = run(&root, &["go", "--api-version", "bogus"]);
    assert_eq!(code, 2, "{err}");
    assert_eq!(*ran.lock().unwrap(), 1);
}

#[test]
fn validator_registered_twice_last_wins() {
    let (mut root, _) = validated();
    root.with_flag_validator("mode", refuse("first"));
    root.with_flag_validator("mode", |_| None);
    assert_eq!(run(&root, &["go", "--mode", "bad"]).0, 0);
}

// --- validation failure modes ---

fn untagged() -> Root {
    let mut c = Config::new("t", "1", "t");
    c.validation_failure = ValidationFailureMode::Exit;
    let mut root = Root::with_config(c);
    root.add_command(CommandSpec::new("x").about("x").handler(|_, _, _| Ok(())));
    root
}

#[test]
fn validation_failure_modes() {
    // Exit: rendered as a USAGE envelope, exit 2.
    let root = untagged();
    let (code, _, err) = run(&root, &["x"]);
    assert_eq!(code, 2);
    assert!(err.starts_with("USAGE: cli validation failed"), "{err}");
    // Error: returned, not rendered.
    let mut c = Config::new("t", "1", "t");
    c.validation_failure = ValidationFailureMode::Error;
    let mut root = Root::with_config(c);
    root.add_command(CommandSpec::new("x").about("x").handler(|_, _, _| Ok(())));
    let (code, _, err) = run(&root, &["x"]);
    assert_eq!((code, err.as_str()), (2, ""));
    // Silent: warned, and the command runs.
    let mut c = Config::new("t", "1", "t");
    c.validation_failure = ValidationFailureMode::Silent;
    let mut root = Root::with_config(c);
    root.add_command(CommandSpec::new("x").about("x").handler(|_, _, _| Ok(())));
    let (code, _, err) = run(&root, &["x"]);
    assert_eq!(code, 0);
    assert!(
        err.contains("[warn] cli validation failure (silent mode)"),
        "{err}"
    );
    // A bare wrapped tree (no kit config) is never validated.
    let mut bare = Root::new(CommandSpec::new("t"));
    bare.add_command(CommandSpec::new("x").handler(|_, _, _| Ok(())));
    assert_eq!(run(&bare, &["x"]).0, 0);
    // An explicit validate() still reports.
    assert!(untagged().validate().is_err());
}

// --- alias commands ---

fn store_root(dir: &std::path::Path) -> Root {
    let mut store = AliasStore::new(dir.join("aliases.yaml"));
    store.load().unwrap();
    let mut root = Root::with_config(cfg());
    root.add_command(
        CommandSpec::new("ls")
            .about("ls")
            .side_effect("read")
            .handler(|_, out, _| {
                write!(out, "listed").unwrap();
                Ok(())
            }),
    );
    root.add_command(alias_command(Arc::new(Mutex::new(store))));
    root
}

#[test]
fn alias_add_persists_and_dry_run_plans_without_writing() {
    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().join("aliases.yaml");
    let root = store_root(dir.path());
    let (code, out, err) = run(
        &root,
        &[
            "--format",
            "json",
            "--dry-run",
            "alias",
            "add",
            "gg",
            "ls -x",
        ],
    );
    assert_eq!(code, 0, "{err}");
    let plan: serde_json::Value = serde_json::from_str(&out).unwrap();
    assert_eq!(plan["effects"][0]["kind"], "create");
    assert_eq!(plan["effects"][0]["detail"], "dispatch to ls -x");
    assert!(!file.exists(), "a preview must not write");
    let (code, ..) = run(&root, &["alias", "add", "gg", "ls -x"]);
    assert_eq!(code, 0);
    assert_eq!(std::fs::read_to_string(&file).unwrap(), "gg: ls -x\n");
    // Updating the same name previews as an update; deleting previews
    // the old target; both rm and remove reach delete.
    let (_, out, _) = run(
        &root,
        &["--format", "json", "--dry-run", "alias", "rm", "gg"],
    );
    let plan: serde_json::Value = serde_json::from_str(&out).unwrap();
    assert_eq!(plan["effects"][0]["kind"], "delete");
    assert_eq!(plan["effects"][0]["detail"], "was ls -x");
    let (code, ..) = run(&root, &["alias", "remove", "gg"]);
    assert_eq!(code, 0);
    assert_eq!(std::fs::read_to_string(&file).unwrap(), "{}\n");
}

#[test]
fn alias_delete_of_a_missing_alias_dry_run_warns() {
    let dir = tempfile::tempdir().unwrap();
    let root = store_root(dir.path());
    let (code, out, _) = run(
        &root,
        &["--format", "json", "--dry-run", "alias", "delete", "zz"],
    );
    assert_eq!(code, 0);
    let plan: serde_json::Value = serde_json::from_str(&out).unwrap();
    assert_eq!(plan["warnings"][0], "no alias zz: delete is a no-op");
    assert_eq!(plan["effects"].as_array().unwrap().len(), 0);
}
