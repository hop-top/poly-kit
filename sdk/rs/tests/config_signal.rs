//! Ports signal_reload_test.go from go/core/config. Signals are
//! process-wide, so these tests live in their own binary and use
//! distinct signals.
#![cfg(all(feature = "config-signal", unix))]

mod common_config;

use std::sync::Arc;
use std::time::{Duration, Instant};

use common_config::{initial, wait_for, write_cfg, Recorder, ReloadCfg};
use hop_top_kit::config::{load, Options, ReloadOptions, Reloadable};
use tokio::signal::unix::SignalKind;

fn kill(sig: &str) {
    let ok = std::process::Command::new("kill")
        .args([sig, &std::process::id().to_string()])
        .status()
        .unwrap()
        .success();
    assert!(ok);
}

fn setup(rec: Arc<Recorder>) -> (tempfile::TempDir, Arc<Reloadable<ReloadCfg>>) {
    let dir = tempfile::tempdir().unwrap();
    let mut c = initial();
    c.endpoint = "https://before".into();
    let opts = Options {
        project_config_path: write_cfg(dir.path(), &c),
        ..Options::default()
    };
    let mut loaded = ReloadCfg::default();
    load(&mut loaded, &opts).unwrap();
    let r = Reloadable::new(loaded, opts, ReloadOptions::default().publisher(rec));
    (dir, Arc::new(r))
}

async fn wait_endpoint(r: &Reloadable<ReloadCfg>, want: &str) {
    let deadline = Instant::now() + Duration::from_secs(2);
    while r.snapshot().endpoint != want && Instant::now() < deadline {
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    assert_eq!(r.snapshot().endpoint, want);
}

#[tokio::test]
async fn watch_signal_rejects_empty_args() {
    let (_d, r) = setup(Arc::new(Recorder::default()));
    let err = r.watch_signal(async {}, &[]).await.unwrap_err();
    assert_eq!(
        err.to_string(),
        "config.WatchSignal: at least one signal required"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn watch_signal_e2e_reload_on_signal() {
    let rec = Arc::new(Recorder::default());
    let (dir, r) = setup(rec.clone());
    let (stop_tx, stop_rx) = tokio::sync::oneshot::channel::<()>();
    let w = r.clone();
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel::<()>();
    let task = tokio::spawn(async move {
        w.watch_signal_ready(
            async {
                let _ = stop_rx.await;
            },
            &[SignalKind::user_defined1()],
            move || {
                let _ = ready_tx.send(());
            },
        )
        .await
    });
    ready_rx.await.expect("handlers installed");
    let mut up = initial();
    up.endpoint = "https://after".into();
    write_cfg(dir.path(), &up);
    kill("-USR1");
    wait_endpoint(&r, "https://after").await;
    wait_for(&rec, "kit.config.snapshot.reloaded", 1);
    stop_tx.send(()).unwrap();
    let res = tokio::time::timeout(Duration::from_secs(2), task)
        .await
        .expect("watcher exits after shutdown")
        .unwrap();
    assert!(res.is_ok());
}

#[tokio::test(flavor = "multi_thread")]
async fn watch_signal_veto_does_not_kill_watcher() {
    let rec = Arc::new(Recorder::default());
    let (dir, r) = setup(rec.clone());
    let (stop_tx, stop_rx) = tokio::sync::oneshot::channel::<()>();
    let w = r.clone();
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel::<()>();
    let task = tokio::spawn(async move {
        w.watch_signal_ready(
            async {
                let _ = stop_rx.await;
            },
            &[SignalKind::user_defined2()],
            move || {
                let _ = ready_tx.send(());
            },
        )
        .await
    });
    ready_rx.await.expect("handlers installed");
    let mut bad = initial();
    bad.listen_addr = ":9090".into();
    write_cfg(dir.path(), &bad);
    kill("-USR2");
    assert_eq!(
        wait_for(&rec, "kit.config.snapshot.reload_failed", 1).len(),
        1
    );
    let mut good = initial();
    good.endpoint = "https://after".into();
    write_cfg(dir.path(), &good);
    kill("-USR2");
    wait_endpoint(&r, "https://after").await;
    stop_tx.send(()).unwrap();
    task.await.unwrap().unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn watch_signal_publishes_a_panicked_reload() {
    use std::sync::atomic::{AtomicBool, Ordering};
    let rec = Arc::new(Recorder::default());
    let dir = tempfile::tempdir().unwrap();
    let mut opts = Options {
        project_config_path: write_cfg(dir.path(), &initial()),
        ..Options::default()
    };
    let mut loaded = ReloadCfg::default();
    load(&mut loaded, &opts).unwrap();
    // The first reload's env hook panics inside `reload`.
    let tripped = Arc::new(AtomicBool::new(false));
    let t = tripped.clone();
    opts.env_override = Some(Arc::new(move |_: &mut dyn std::any::Any| {
        if !t.swap(true, Ordering::SeqCst) {
            panic!("boom");
        }
    }));
    let r = Arc::new(Reloadable::new(
        loaded,
        opts,
        ReloadOptions::default().publisher(rec.clone()),
    ));
    let (stop_tx, stop_rx) = tokio::sync::oneshot::channel::<()>();
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel::<()>();
    let w = r.clone();
    let task = tokio::spawn(async move {
        w.watch_signal_ready(
            async {
                let _ = stop_rx.await;
            },
            &[SignalKind::window_change()],
            move || {
                let _ = ready_tx.send(());
            },
        )
        .await
    });
    ready_rx.await.expect("handlers installed");
    kill("-WINCH");
    let failed = wait_for(&rec, "kit.config.snapshot.reload_failed", 1);
    let err = failed[0].payload["error"].as_str().unwrap().to_string();
    assert!(
        err.starts_with("config reload: task ") && err.contains("boom"),
        "{err}"
    );
    assert_eq!(failed[0].payload["reason"], "load_error");
    // The watch survives: the next signal reloads normally.
    kill("-WINCH");
    wait_for(&rec, "kit.config.snapshot.reloaded", 1);
    stop_tx.send(()).unwrap();
    task.await.unwrap().unwrap();
}
