//! Ports nearest_test.go, paths_test.go, paths_markers_test.go,
//! gaps_test.go (OptionsForTool) and the config-home cases of
//! go/core/xdg/xdg_test.go.
//!
//! These tests set HOME and XDG_* and change directory, so each takes
//! one process-wide lock; this binary holds nothing else.
#![cfg(feature = "config")]

use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard};

use hop_top_kit::config::{
    nearest, nearest_with_depth, options_for_tool, options_for_tool_with_markers_from, paths,
    paths_for_tool_with_markers, ErrorKind, ResolvedPath,
};
use hop_top_kit::xdg;

static ENV: Mutex<()> = Mutex::new(());

struct Fixture {
    _lock: MutexGuard<'static, ()>,
    _root: tempfile::TempDir,
    home: PathBuf,
    saved: Vec<(&'static str, Option<String>)>,
    cwd: PathBuf,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::env::set_current_dir(&self.cwd);
        for (k, v) in &self.saved {
            match v {
                Some(v) => std::env::set_var(k, v),
                None => std::env::remove_var(k),
            }
        }
    }
}

/// HOME and XDG_CONFIG_HOME inside a temp dir.
fn fixture() -> Fixture {
    let lock = ENV.lock().unwrap_or_else(|e| e.into_inner());
    let root = tempfile::tempdir().unwrap();
    let home = root.path().join("home");
    std::fs::create_dir_all(&home).unwrap();
    let saved = ["HOME", "XDG_CONFIG_HOME", "XDG_CONFIG_DIRS"]
        .into_iter()
        .map(|k| (k, std::env::var(k).ok()))
        .collect();
    std::env::set_var("HOME", &home);
    std::env::set_var("XDG_CONFIG_HOME", home.join(".config"));
    std::env::set_var("XDG_CONFIG_DIRS", "");
    Fixture {
        _lock: lock,
        _root: root,
        home,
        saved,
        cwd: std::env::current_dir().unwrap(),
    }
}

fn touch(p: &Path) {
    std::fs::create_dir_all(p.parent().unwrap()).unwrap();
    std::fs::write(p, "x: 1\n").unwrap();
}

fn by_source<'a>(chain: &'a [ResolvedPath], source: &str) -> Vec<&'a ResolvedPath> {
    chain.iter().filter(|p| p.source == source).collect()
}

#[test]
fn nearest_finds_marker_in_start_dir() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    let want = root.path().join(".rlz").join("config.yaml");
    touch(&want);
    assert_eq!(nearest(root.path(), ".rlz/config.yaml").unwrap(), want);
}

#[test]
fn nearest_walks_up() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    let deep = root.path().join("a").join("b").join("c");
    std::fs::create_dir_all(&deep).unwrap();
    let want = root.path().join(".rlz").join("config.yaml");
    touch(&want);
    assert_eq!(nearest(&deep, ".rlz/config.yaml").unwrap(), want);
}

#[test]
fn nearest_not_found() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    let err = nearest(root.path(), ".does-not-exist/c.yaml").unwrap_err();
    assert!(err.is(ErrorKind::MarkerNotFound));
    assert_eq!(err.to_string(), "config: project marker not found");
}

#[test]
fn nearest_respects_max_depth() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    let deep = root.path().join("a/b/c/d/e");
    std::fs::create_dir_all(&deep).unwrap();
    touch(&root.path().join(".rlz/config.yaml"));
    let err = nearest_with_depth(&deep, ".rlz/config.yaml", 2).unwrap_err();
    assert!(err.is(ErrorKind::MarkerNotFound));
}

#[test]
fn nearest_directory_marker() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    std::fs::create_dir_all(root.path().join("sub/.rlz")).unwrap();
    assert_eq!(
        nearest(root.path().join("sub"), ".rlz").unwrap(),
        root.path().join("sub").join(".rlz")
    );
}

#[test]
fn nearest_empty_marker() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    let err = nearest(root.path(), "").unwrap_err();
    assert_eq!(err.to_string(), "config: marker is empty");
}

#[test]
fn nearest_empty_start_dir_uses_cwd() {
    let _f = fixture();
    let root = tempfile::tempdir().unwrap();
    touch(&root.path().join(".rlz/config.yaml"));
    std::env::set_current_dir(root.path()).unwrap();
    let got = nearest("", ".rlz/config.yaml").unwrap();
    assert!(got.ends_with(".rlz/config.yaml"), "{got:?}");
}

#[test]
fn paths_cwd_inside_project_with_config() {
    let f = fixture();
    let project = f.home.join("work/myproj");
    let cfg = project.join(".kit/config.yaml");
    touch(&cfg);
    let chain = paths(&project);
    let hit = by_source(&chain, "cwd")
        .into_iter()
        .find(|e| e.path == cfg)
        .expect("cwd .kit/config.yaml");
    assert!(hit.exists);
}

#[test]
fn paths_project_with_no_project_config() {
    let f = fixture();
    let user_cfg = f.home.join(".config/kit/config.yaml");
    touch(&user_cfg);
    let project = f.home.join("work/empty");
    std::fs::create_dir_all(&project).unwrap();
    let chain = paths(&project);
    assert!(by_source(&chain, "cwd").iter().all(|e| !e.exists));
    let user = by_source(&chain, "user");
    assert_eq!(user.len(), 1);
    assert_eq!(user[0].path, user_cfg);
    assert!(user[0].exists);
    let def = by_source(&chain, "default");
    assert_eq!(def.len(), 1);
    assert!(def[0].exists);
}

#[test]
fn paths_random_cwd_no_project_no_user() {
    let _f = fixture();
    let random = tempfile::tempdir().unwrap();
    let chain = paths(random.path());
    let user = by_source(&chain, "user");
    assert_eq!(user.len(), 1);
    assert!(!user[0].exists);
    let sys = by_source(&chain, "system");
    assert_eq!(sys.len(), 1);
    assert_eq!(sys[0].path, Path::new("/etc/kit/config.yaml"));
    assert_eq!(by_source(&chain, "default").len(), 1);
}

#[test]
fn paths_user_exists_system_does_not() {
    let f = fixture();
    touch(&f.home.join(".config/kit/config.yaml"));
    let chain = paths(f.home.join("anywhere"));
    assert!(by_source(&chain, "user")[0].exists);
    assert!(!by_source(&chain, "system")[0].exists);
}

#[test]
fn paths_relative_cwd_resolves_to_abs() {
    let _f = fixture();
    let base = tempfile::tempdir().unwrap();
    std::env::set_current_dir(base.path()).unwrap();
    let chain = paths(".");
    assert!(!chain.is_empty());
    for e in chain.iter().filter(|e| e.source != "default") {
        assert!(e.path.is_absolute(), "{e:?}");
    }
}

#[test]
fn paths_deep_walk_up() {
    let f = fixture();
    let root = f.home.join("work/proj");
    let deep = root.join("a/b/c/d");
    std::fs::create_dir_all(&deep).unwrap();
    let cfg = root.join(".kit/config.yaml");
    touch(&cfg);
    let chain = paths(&deep);
    let hit = chain.iter().find(|e| e.path == cfg).expect("deep walk");
    assert_eq!(hit.source, "project");
    assert!(hit.exists);
}

#[test]
fn paths_does_not_walk_above_home() {
    let f = fixture();
    let stray = f.home.parent().unwrap().join(".kit/config.yaml");
    touch(&stray);
    let deep = f.home.join("x/y/z");
    std::fs::create_dir_all(&deep).unwrap();
    assert!(paths(&deep).iter().all(|e| e.path != stray));
}

#[test]
fn paths_default_entry_is_last_and_deterministic() {
    let _f = fixture();
    let cwd = tempfile::tempdir().unwrap();
    let chain = paths(cwd.path());
    let last = chain.last().unwrap();
    assert_eq!(last.source, "default");
    assert_eq!(last.path, Path::new("<defaults>"));
    assert!(last.exists);
    assert_eq!(chain, paths(cwd.path()));
}

#[test]
fn paths_with_markers_custom_marker_found() {
    let f = fixture();
    let project = f.home.join("work/myproj");
    let cfg = project.join(".ctxt/config.yaml");
    touch(&cfg);
    let deep = project.join("internal/events");
    std::fs::create_dir_all(&deep).unwrap();
    let chain = paths_for_tool_with_markers(
        &deep,
        "ctxt",
        &[".ctxt/config.yaml", ".ctxt.yaml", "ctxt.yaml"],
    );
    let hit = chain
        .iter()
        .find(|p| (p.source == "cwd" || p.source == "project") && p.path == cfg)
        .expect("custom marker in chain");
    assert!(hit.exists);
}

#[test]
fn paths_with_markers_empty_falls_back_to_default() {
    let f = fixture();
    let project = f.home.join("work/myproj");
    let kit = project.join(".kit/config.yaml");
    touch(&kit);
    let chain = paths_for_tool_with_markers(&project, "ctxt", &[]);
    assert!(chain.iter().any(|p| p.path == kit));
}

#[test]
fn paths_with_markers_rewrites_user_and_system() {
    let f = fixture();
    let chain = paths_for_tool_with_markers(&f.home, "ctxt", &[".ctxt.yaml"]);
    assert!(by_source(&chain, "user")[0]
        .path
        .ends_with(".config/ctxt/config.yaml"));
    assert_eq!(
        by_source(&chain, "system")[0].path,
        Path::new("/etc/ctxt/config.yaml")
    );
}

#[test]
fn options_with_markers_picks_custom_project_file() {
    let f = fixture();
    let project = f.home.join("work/myproj");
    let cfg = project.join(".ctxt.yaml");
    touch(&cfg);
    let opts = options_for_tool_with_markers_from(&project, "ctxt", &[".ctxt.yaml"]);
    assert_eq!(opts.project_config_path, cfg);
    assert!(opts.user_config_path.ends_with(".config/ctxt/config.yaml"));
    assert_eq!(opts.system_config_path, Path::new("/etc/ctxt/config.yaml"));
}

#[test]
fn options_with_markers_no_custom_file_project_empty() {
    let f = fixture();
    let cwd = f.home.join("work/myproj");
    std::fs::create_dir_all(&cwd).unwrap();
    let opts = options_for_tool_with_markers_from(&cwd, "ctxt", &[".ctxt.yaml"]);
    assert_eq!(opts.project_config_path, PathBuf::new());
}

#[test]
fn options_for_tool_parameterizes_user_and_system() {
    let _f = fixture();
    let opts = options_for_tool("c12n");
    let user = opts.user_config_path.to_string_lossy().to_string();
    assert!(user.contains("c12n"));
    assert!(!user.contains("/kit/"));
    assert_eq!(opts.system_config_path, Path::new("/etc/c12n/config.yaml"));
}

#[test]
fn xdg_config_home_override_and_fallback() {
    let f = fixture();
    assert_eq!(xdg::raw_config_dir("tool"), f.home.join(".config/tool"));
    std::env::set_var("XDG_CONFIG_HOME", "relative/ignored");
    let fallback = xdg::raw_config_dir("tool");
    assert!(fallback.ends_with("tool"));
    assert!(fallback.starts_with(&f.home), "{fallback:?}");
    std::env::set_var("XDG_CONFIG_HOME", "~/cfg");
    assert_eq!(xdg::raw_config_dir("tool"), f.home.join("cfg/tool"));
    assert_eq!(xdg::home(), f.home);
}
