//! Signal-driven reload. Ports Go `signal_reload.go`. Feature
//! `config-signal`, Unix only.

use std::future::Future;
use std::sync::Arc;

use serde::de::DeserializeOwned;
use serde::Serialize;
use tokio::signal::unix::{signal, SignalKind};

use super::partition::ReloadFields;
use super::reload::Reloadable;
use super::Error;

impl<T> Reloadable<T>
where
    T: Serialize + DeserializeOwned + ReloadFields + Default + Send + Sync + 'static,
{
    /// Reloads with the held options on every receipt of any signal in
    /// `signals` (SIGHUP by convention; tests use SIGUSR1/2) until
    /// `shutdown` completes, then returns `Ok(())`.
    ///
    /// Reload errors do not end the watch: the failure event is
    /// published and the next signal is awaited. A reload that panics is
    /// published as `reload_failed` (`load_error`, `config reload: task
    /// ... panicked ...`) and the watch goes on. Errors only for no
    /// signals (`config.WatchSignal: at least one signal required`) or a
    /// signal handler that cannot be installed.
    ///
    /// Each reload (file reads) runs on tokio's blocking pool, so it
    /// never stalls the executor. Called on an `Arc<Reloadable<T>>`.
    pub async fn watch_signal(
        self: &Arc<Self>,
        shutdown: impl Future<Output = ()>,
        signals: &[SignalKind],
    ) -> Result<(), Error> {
        self.watch_signal_ready(shutdown, signals, || {}).await
    }

    /// [`watch_signal`](Self::watch_signal), calling `ready` once every
    /// handler is installed: a signal sent after that is not lost (and
    /// cannot take the default action, which for most signals ends the
    /// process).
    pub async fn watch_signal_ready(
        self: &Arc<Self>,
        shutdown: impl Future<Output = ()>,
        signals: &[SignalKind],
        ready: impl FnOnce(),
    ) -> Result<(), Error> {
        if signals.is_empty() {
            return Err(Error::invalid(
                "config.WatchSignal: at least one signal required",
            ));
        }
        let (tx, mut rx) = tokio::sync::mpsc::channel::<()>(1);
        let mut tasks = Vec::new();
        for kind in signals {
            let mut stream = signal(*kind)
                .map_err(|e| Error::invalid(format!("config.WatchSignal: install handler: {e}")))?;
            let tx = tx.clone();
            tasks.push(tokio::spawn(async move {
                while stream.recv().await.is_some() {
                    // A full channel already has a reload pending.
                    let _ = tx.try_send(());
                }
            }));
        }
        drop(tx);
        ready();
        tokio::pin!(shutdown);
        let out = loop {
            tokio::select! {
                () = &mut shutdown => break Ok(()),
                got = rx.recv() => match got {
                    Some(()) => {
                        let me = Arc::clone(self);
                        let opts = self.options();
                        let held = opts.clone();
                        // A reload error was already published; a reload
                        // that never returned (it panicked) is published here.
                        if let Err(e) = tokio::task::spawn_blocking(move || me.reload(opts)).await {
                            self.reload_aborted(&held, &e.to_string());
                        }
                    }
                    None => break Ok(()),
                },
            }
        };
        for t in tasks {
            t.abort();
        }
        out
    }
}
