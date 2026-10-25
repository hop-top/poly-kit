//! Signal-driven reload. Ports Go `signal_reload.go`. Feature
//! `config-signal`, Unix only.

use std::future::Future;

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
    /// published and the next signal is awaited. Errors only for no
    /// signals (`config.WatchSignal: at least one signal required`) or a
    /// signal handler that cannot be installed.
    pub async fn watch_signal(
        &self,
        shutdown: impl Future<Output = ()>,
        signals: &[SignalKind],
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
        tokio::pin!(shutdown);
        let out = loop {
            tokio::select! {
                () = &mut shutdown => break Ok(()),
                got = rx.recv() => match got {
                    Some(()) => {
                        let _ = self.reload(self.options());
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
