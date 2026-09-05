//! `SIGINT`/`SIGTERM` handling and the second-signal escalation.

use super::cancel::CancelToken;

/// The signals the supervisor listens for. `SIGKILL` is not catchable.
pub const SHUTDOWN_SIGNALS: &[&str] = &["SIGINT", "SIGTERM"];

/// A signal handler pair: `shutdown` fires on the first SIGINT/SIGTERM,
/// `escalate` on a second of either kind.
///
/// The first signal begins the graceful drain; a second aborts it, so
/// an operator can escalate without reaching for SIGKILL. `stop` ends
/// the listener task, or it outlives the run.
pub struct SignalController {
    /// Fires on the first SIGINT/SIGTERM. Hand this to
    /// [`super::Supervisor::run`].
    pub shutdown: CancelToken,
    /// Fires on a second signal of either kind. Hand this to
    /// [`super::SupervisorOptions::escalate`].
    pub escalate: CancelToken,
    /// Ends the listener task.
    stop_token: CancelToken,
}

impl SignalController {
    /// Removes the signal listener. Idempotent.
    pub fn stop(&self) {
        self.stop_token.cancel();
    }
}

impl Drop for SignalController {
    fn drop(&mut self) {
        self.stop();
    }
}

/// Installs the SIGINT/SIGTERM listener on the current runtime.
///
/// A port that cannot observe a *second* signal degrades to the single
/// graceful path rather than inventing a different escalation; this
/// port can, so both tokens are live. On a platform with no SIGTERM
/// only SIGINT is installed and the drain still begins on the
/// platform's own notification.
#[cfg(unix)]
pub fn signal_controller() -> std::io::Result<SignalController> {
    use tokio::signal::unix::{signal, SignalKind};

    let shutdown = CancelToken::new();
    let escalate = CancelToken::new();
    let stop_token = CancelToken::new();

    let mut sigint = signal(SignalKind::interrupt())?;
    let mut sigterm = signal(SignalKind::terminate())?;

    let first = shutdown.clone();
    let second = escalate.clone();
    let stop = stop_token.clone();
    tokio::spawn(async move {
        let mut count = 0u32;
        loop {
            tokio::select! {
                () = stop.cancelled() => return,
                _ = sigint.recv() => {}
                _ = sigterm.recv() => {}
            }
            count += 1;
            if count == 1 {
                first.cancel();
            } else {
                second.cancel();
                return;
            }
        }
    });

    Ok(SignalController {
        shutdown,
        escalate,
        stop_token,
    })
}

/// Non-unix fallback: Ctrl-C only.
///
/// Windows has no `SIGTERM` to catch, so the platform's own shutdown
/// notification maps onto the same drain. A second Ctrl-C still
/// escalates.
#[cfg(not(unix))]
pub fn signal_controller() -> std::io::Result<SignalController> {
    let shutdown = CancelToken::new();
    let escalate = CancelToken::new();
    let stop_token = CancelToken::new();

    let first = shutdown.clone();
    let second = escalate.clone();
    let stop = stop_token.clone();
    tokio::spawn(async move {
        let mut count = 0u32;
        loop {
            tokio::select! {
                () = stop.cancelled() => return,
                r = tokio::signal::ctrl_c() => {
                    if r.is_err() {
                        return;
                    }
                }
            }
            count += 1;
            if count == 1 {
                first.cancel();
            } else {
                second.cancel();
                return;
            }
        }
    });

    Ok(SignalController {
        shutdown,
        escalate,
        stop_token,
    })
}
