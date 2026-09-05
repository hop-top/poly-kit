# serve

## What it answers

How does a CLI host long-running services under one `serve` verb with a shared lifecycle: start order,
readiness, failure policy, shutdown, exit codes? One-shot commands belong to `hop_top_kit::cli`; event
delivery to `hop_top_kit::bus`.

## Use it when

- a tool ships one or more services and wants `tool serve` and `tool serve <name>` to behave the same → implement
  `Service`, register it on a `ServiceRegistry`, run a `Supervisor`
- the tool already has a clap root → feature `serve-cli`, then `command::mount` and `command::run`
- a service must start after another → override `Service::depends_on`; stop order is the exact reverse
- an operator must be able to refuse a service → pass a `PolicyGate` in `ResolveRequest`

## Quick start

```rust
use std::sync::Arc;
use hop_top_kit::serve::{
    resolve, CancelToken, ReadySignal, ResolveRequest, ServeFuture, Service, ServiceConfig,
    ServiceConfigs, ServiceRegistry, Supervisor, SupervisorOptions,
};

struct Api;

impl Service for Api {
    fn name(&self) -> &str { "api" }
    fn start<'a>(&'a self, cancel: CancelToken, ready: ReadySignal)
        -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async move { ready.report(); cancel.cancelled().await; Ok(()) })
    }
    fn ready(&self) -> bool { true }
    fn stop<'a>(&'a self, _cancel: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        Box::pin(async { Ok(()) })
    }
}

let mut registry = ServiceRegistry::new();
registry.register(Arc::new(Api)).expect("wiring");
let mut configs = ServiceConfigs::new();
configs.insert("api".to_string(), ServiceConfig::enabled());

let outcome = resolve(&registry, &ResolveRequest { args: vec![], configs: Some(&configs), policy: None });
let shutdown = CancelToken::new();
let trigger = shutdown.clone();
tokio::spawn(async move { trigger.cancel() });
let sup = Supervisor::new(registry, SupervisorOptions::default());
let res = sup.run(shutdown, &outcome.selected, &configs).await;
assert_eq!(res.exit_code, 0);
```

## Contract

- Feature `serve` pulls in `output`, `tokio` and `serde_json`; `serve-cli` adds `cli` (clap).
  Authority: the crate [feature table](../../README.md#features).
- Behaviour is [serve-lifecycle.md](../../../../../docs/contracts/serve-lifecycle.md): the selector form runs a
  disabled service, the supervisor form skips it silently, zero services resolved is a usage error.
- Names match `^[a-z][a-z0-9-]*$`; `all`, `none`, `list` are reserved. Duplicate registration is refused
  at construction; `override_service` is the escape hatch.
- Exit codes come from `LifecycleOutcome::exit_code`: clean stop 0, start failure or crash or shutdown
  timeout 1, usage or config 2, unknown service 3, policy denied 5.
- Defaults: `ready_timeout` 30s, `stop_timeout` 30s, `shutdown_timeout` 60s, `failure_policy` fail-fast.
- Lifecycle events publish under `kit.serve.*` through the narrow `Publisher` trait: `bus::Bus` is `!Send`.
- Parity: [contracts/parity/serve.json](../../../../../contracts/parity/serve.json), row `ports.rs`, pinned by
  `tests/serve_parity.rs`.

## Neighbours

- `hop_top_kit::cli` (src/cli.rs): the clap root and one-shot verbs the `serve` command mounts on
- `hop_top_kit::bus`: topic grammar the six transitions follow
- `hop_top_kit::output`: `CliError` and the exit-code taxonomy the outcomes map onto
- `examples/serve.rs`: a service binding a real listener, driven by `tests/serve_command.rs`

## See also

- [Migrate to served commands](../../../../../docs/adopters/guides/migrate-to-served-commands.md)
- [Crate README, Serve](../../README.md#serve)
