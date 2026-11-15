//! A service's `stopped` is its Stop returning, so on a canceled run
//! the events follow reverse start order whichever branch of the
//! supervisor's wait noticed the cancellation and the clean exits.

#![cfg(feature = "serve")]

use std::sync::mpsc;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::serve::{
    CancelToken, EventPayload, Publisher, ReadySignal, ServeFuture, Service, ServiceConfig,
    ServiceConfigs, ServiceRegistry, Supervisor, SupervisorOptions,
};

/// Start returns as soon as the run is canceled.
struct OnCancel(&'static str);

impl Service for OnCancel {
    fn name(&self) -> &str {
        self.0
    }
    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            ready.report();
            cancel.cancelled().await;
            Ok(())
        })
    }
    fn ready(&self) -> bool {
        true
    }
    fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }
}

/// Start returns only once its Stop was called: the listener shape.
struct UntilStop {
    name: &'static str,
    released: CancelToken,
    /// Delay before reporting ready, so earlier services can settle
    /// while this one is still being awaited.
    ready_after: Duration,
}

impl Service for UntilStop {
    fn name(&self) -> &str {
        self.name
    }
    fn start<'a>(
        &'a self,
        _: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            // No timer when there is no delay: the gated publisher
            // parks a worker, which can be the one driving timers.
            if !self.ready_after.is_zero() {
                tokio::time::sleep(self.ready_after).await;
            }
            ready.report();
            self.released.cancelled().await;
            Ok(())
        })
    }
    fn ready(&self) -> bool {
        true
    }
    fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            self.released.cancel();
            Ok(())
        })
    }
}

/// Start returns cleanly by itself shortly after reporting ready.
struct Quitter;

impl Service for Quitter {
    fn name(&self) -> &str {
        "quitter"
    }
    fn start<'a>(
        &'a self,
        _: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            ready.report();
            tokio::time::sleep(Duration::from_millis(10)).await;
            Ok(())
        })
    }
    fn ready(&self) -> bool {
        true
    }
    fn stop<'a>(&'a self, _: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }
}

/// Records every event; may hold the aggregate-ready publish until
/// released, which keeps the supervisor in its start phase while the
/// run is canceled and the Start calls return.
struct Recorder {
    topics: Mutex<Vec<(String, Option<String>)>>,
    gate: Mutex<Option<mpsc::Receiver<()>>>,
}

impl Recorder {
    fn new(gate: Option<mpsc::Receiver<()>>) -> Arc<Self> {
        Arc::new(Recorder {
            topics: Mutex::new(Vec::new()),
            gate: Mutex::new(gate),
        })
    }

    fn count(&self, topic: &str) -> usize {
        self.topics
            .lock()
            .unwrap()
            .iter()
            .filter(|(t, _)| t == topic)
            .count()
    }

    fn stopped(&self) -> Vec<String> {
        self.topics
            .lock()
            .unwrap()
            .iter()
            .filter(|(t, _)| t == "kit.serve.service.stopped")
            .filter_map(|(_, s)| s.clone())
            .collect()
    }
}

impl Publisher for Recorder {
    fn publish(&self, topic: &str, _source: &str, payload: &EventPayload) {
        if topic == "kit.serve.supervisor.ready_reported" {
            if let Some(rx) = self.gate.lock().unwrap().take() {
                let _ = rx.recv();
            }
        }
        self.topics
            .lock()
            .unwrap()
            .push((topic.to_string(), payload.service.clone()));
    }
}

fn configs(names: &[&str]) -> ServiceConfigs {
    names
        .iter()
        .map(|n| ((*n).to_string(), ServiceConfig::enabled()))
        .collect()
}

async fn until(what: &str, mut cond: impl FnMut() -> bool) {
    for _ in 0..2000 {
        if cond() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(2)).await;
    }
    panic!("timed out waiting for {what}");
}

// The supervisor future blocks a worker thread in the gated publish,
// so the service tasks need the other workers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn shutdown_stopped_events_follow_reverse_start_order() {
    let names = ["alpha", "beta", "gamma", "delta"];
    for _ in 0..50 {
        let mut reg = ServiceRegistry::new();
        reg.register(Arc::new(OnCancel("alpha"))).unwrap();
        reg.register(Arc::new(UntilStop {
            name: "beta",
            released: CancelToken::new(),
            ready_after: Duration::ZERO,
        }))
        .unwrap();
        reg.register(Arc::new(OnCancel("gamma"))).unwrap();
        reg.register(Arc::new(UntilStop {
            name: "delta",
            released: CancelToken::new(),
            ready_after: Duration::ZERO,
        }))
        .unwrap();

        let (release, gate) = mpsc::channel();
        let rec = Recorder::new(Some(gate));
        let sup = Arc::new(Supervisor::new(
            reg,
            SupervisorOptions {
                publisher: Some(rec.clone()),
                ..SupervisorOptions::default()
            },
        ));
        let cancel = CancelToken::new();
        let run = {
            let (sup, cancel) = (sup.clone(), cancel.clone());
            let selected: Vec<String> = names.iter().map(|n| n.to_string()).collect();
            let cfg = configs(&names);
            tokio::spawn(async move { sup.run(cancel, &selected, &cfg).await })
        };

        until("every service ready", || {
            rec.count("kit.serve.service.ready_reported") == 4
        })
        .await;
        cancel.cancel();
        tokio::time::sleep(Duration::from_millis(20)).await;
        release.send(()).unwrap();
        let res = tokio::time::timeout(Duration::from_secs(20), run)
            .await
            .expect("run bounded")
            .unwrap();
        assert!(res.error.is_none());

        assert_eq!(rec.stopped(), ["delta", "gamma", "beta", "alpha"]);
    }
}

// A service returning on its own before shutdown still reports
// stopped at that moment, once, and Stop does not repeat it.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn early_exit_still_reports_stopped_once() {
    let mut reg = ServiceRegistry::new();
    reg.register(Arc::new(OnCancel("steady"))).unwrap();
    reg.register(Arc::new(Quitter)).unwrap();
    let rec = Recorder::new(None);
    let sup = Arc::new(Supervisor::new(
        reg,
        SupervisorOptions {
            publisher: Some(rec.clone()),
            ..SupervisorOptions::default()
        },
    ));
    let cancel = CancelToken::new();
    let run = {
        let (sup, cancel) = (sup.clone(), cancel.clone());
        let names = ["steady", "quitter"];
        let selected: Vec<String> = names.iter().map(|n| n.to_string()).collect();
        let cfg = configs(&names);
        tokio::spawn(async move { sup.run(cancel, &selected, &cfg).await })
    };

    until("quitter stopped", || {
        rec.stopped().contains(&"quitter".to_string())
    })
    .await;
    assert_eq!(
        rec.stopped(),
        ["quitter"],
        "reported at exit, before shutdown"
    );
    cancel.cancel();
    tokio::time::timeout(Duration::from_secs(20), run)
        .await
        .expect("run bounded")
        .unwrap();
    assert_eq!(rec.stopped(), ["quitter", "steady"]);
}

// Cancel lands while the last service is still being awaited for
// readiness: the earlier Start calls that return on cancel settle into
// the pending queue, and draining it must not report stopped ahead of
// the ordered stop.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn cancel_during_start_phase_keeps_reverse_stopped_order() {
    let names = ["alpha", "beta", "gamma", "delta"];
    for _ in 0..20 {
        let mut reg = ServiceRegistry::new();
        reg.register(Arc::new(OnCancel("alpha"))).unwrap();
        reg.register(Arc::new(UntilStop {
            name: "beta",
            released: CancelToken::new(),
            ready_after: Duration::ZERO,
        }))
        .unwrap();
        reg.register(Arc::new(OnCancel("gamma"))).unwrap();
        reg.register(Arc::new(UntilStop {
            name: "delta",
            released: CancelToken::new(),
            ready_after: Duration::from_millis(40),
        }))
        .unwrap();

        let rec = Recorder::new(None);
        let sup = Arc::new(Supervisor::new(
            reg,
            SupervisorOptions {
                publisher: Some(rec.clone()),
                ..SupervisorOptions::default()
            },
        ));
        let cancel = CancelToken::new();
        let run = {
            let (sup, cancel) = (sup.clone(), cancel.clone());
            let selected: Vec<String> = names.iter().map(|n| n.to_string()).collect();
            let cfg = configs(&names);
            tokio::spawn(async move { sup.run(cancel, &selected, &cfg).await })
        };

        until("third service ready", || {
            rec.count("kit.serve.service.ready_reported") == 3
        })
        .await;
        cancel.cancel();
        let res = tokio::time::timeout(Duration::from_secs(20), run)
            .await
            .expect("run bounded")
            .unwrap();
        assert!(res.error.is_none());

        assert_eq!(rec.stopped(), ["delta", "gamma", "beta", "alpha"]);
    }
}
