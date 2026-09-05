//! A tool with one service, `echo`, mounted on a plain clap root.
//!
//! `echo` binds a TCP listener on `127.0.0.1:0` and reports ready
//! with the address the kernel picked, so a supervising process can
//! watch a real port come up and go down. Lifecycle events go to
//! stderr twice: as the log trace, and as the bus publications with
//! their topic strings.
//!
//! ```text
//! cargo run --example serve --features serve-cli -- serve --list
//! cargo run --example serve --features serve-cli -- serve
//! ```

use std::net::TcpListener;
use std::sync::{Arc, Mutex};

use hop_top_kit::serve::command::{mount_for, run, ServeCommandOptions};
use hop_top_kit::serve::{
    CancelToken, EventPayload, Publisher, ReadySignal, ServeFuture, Service, ServiceConfig,
    ServiceConfigs, ServiceRegistry,
};

struct Echo {
    bound: Mutex<Option<String>>,
}

impl Service for Echo {
    fn name(&self) -> &str {
        "echo"
    }

    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move {
            let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| e.to_string())?;
            let addr = listener.local_addr().map_err(|e| e.to_string())?;
            *self.bound.lock().unwrap() = Some(addr.to_string());
            ready.report();
            cancel.cancelled().await;
            drop(listener);
            *self.bound.lock().unwrap() = None;
            Ok(())
        })
    }

    fn ready(&self) -> bool {
        self.bound.lock().unwrap().is_some()
    }

    fn stop<'a>(&'a self, _cancel: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }

    fn addr(&self) -> Option<String> {
        self.bound.lock().unwrap().clone()
    }
}

/// Prints every publication with its topic, so the trace shows the
/// exact strings a subscriber would filter on.
struct StderrBus;

impl Publisher for StderrBus {
    fn publish(&self, topic: &str, source: &str, payload: &EventPayload) {
        eprintln!(
            "event topic={topic} source={source} payload={}",
            payload.to_json()
        );
    }
}

#[tokio::main]
async fn main() {
    let mut registry = ServiceRegistry::new();
    registry
        .register(Arc::new(Echo {
            bound: Mutex::new(None),
        }))
        .expect("wiring");

    let mut configs = ServiceConfigs::new();
    configs.insert("echo".to_string(), ServiceConfig::enabled());

    let root = mount_for(clap::Command::new("serve-example"), &registry);
    let matches = root.get_matches();
    let Some(sub) = matches.subcommand_matches("serve") else {
        eprintln!("usage: serve-example serve [SERVICE] [--list]");
        std::process::exit(2);
    };

    let mut opts = ServeCommandOptions::new(registry);
    opts.configs = Some(configs);
    opts.publisher = Some(Arc::new(StderrBus));
    let res = run(sub, opts).await;
    if let Some(err) = &res.error {
        eprintln!("{}: {}", err.code, err.message);
    }
    std::process::exit(res.exit_code);
}
