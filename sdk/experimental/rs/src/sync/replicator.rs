//! The replicator: one repository, N named remotes. Go's replicator.go.
//!
//! Local changes are captured from the bus (or [`Replicator::enqueue`]d)
//! as [`Diff`]s in a bounded queue; each remote has a cursor into it, and
//! a round pings the remote, pushes what its cursor has not passed
//! (through its filter), pulls what the remote has stamped since the last
//! pull, applies it to the repository and trims what every remote has.
//!
//! # Where this differs from Go
//!
//! - Rounds run on one OS thread per remote ([`Replicator::start`]),
//!   joined by [`Replicator::stop`]; on wasm32-unknown-unknown there are
//!   no threads and the caller drives a round with
//!   [`Replicator::sync_once`] from its own timer.
//! - Cursors are absolute positions in the queue, not indexes into it.
//!   Go's indexes shift when the queue drops its oldest entry on
//!   overflow or when another remote's round trims it, so a remote
//!   could skip a diff it had not seen; here neither moves a cursor.
//! - [`Replicator::remove_remote`] also ends the remote's loop; Go's
//!   keeps running it.
//! - Go reads `time.Now()` for a remote's last sync; here it is the
//!   builder's [`WallClock`].
//! - A [`MergeFunc`] given to the builder is kept but, as in Go, never
//!   called by the replicator: a pulled diff overwrites the local
//!   entity. Call [`resolve_diff`](super::resolve_diff) yourself.
//! - Go's `charm/log` logger is a [`Logger`]; its default writes to
//!   stderr.
//!
//! Kept from Go: a `PullOnly` remote never advances its cursor, so it
//! holds the queue untrimmed (up to [`MAX_QUEUE_SIZE`]) and reports the
//! whole queue as pending.

use std::collections::{HashMap, VecDeque};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime};

use serde::de::DeserializeOwned;
use serde::Serialize;
use serde_json::{json, Value};

use crate::bus::{Context, DynBus, Event, SubscriptionId};
use crate::domain::{Entity, Repository};

use super::clock::{Clock, Timestamp, WallClock};
use super::diff::{Diff, Operation};
use super::merge::MergeFunc;
use super::remote::{Remote, RemoteSet, SyncError, SyncMode};
use super::transport::TransportError;

/// The subscription prefix when none is set: the baseline topic prefix
/// of the domain service, so a vanilla wiring needs no topic
/// configuration. Go's `DefaultSubscriptionPrefix`.
pub const DEFAULT_SUBSCRIPTION_PREFIX: &str = "kit.runtime.entity";

/// The most diffs the outbound queue keeps; at the limit the oldest is
/// dropped. Go's `maxQueueSize`.
pub const MAX_QUEUE_SIZE: usize = 10_000;

const DEFAULT_INTERVAL: Duration = Duration::from_secs(5);

/// Where the replicator reports apply-path errors. Go's `charm/log`
/// logger; fields are key/value pairs.
pub trait Logger: Send + Sync {
    /// Logs an error with its fields.
    fn error(&self, msg: &str, fields: &[(&str, &str)]);
}

impl<F> Logger for F
where
    F: Fn(&str, &[(&str, &str)]) + Send + Sync,
{
    fn error(&self, msg: &str, fields: &[(&str, &str)]) {
        self(msg, fields)
    }
}

/// The default [`Logger`]: one line per error on stderr, `ERROR msg
/// key=value ...`.
#[derive(Clone, Copy, Debug, Default)]
pub struct StderrLogger;

impl Logger for StderrLogger {
    fn error(&self, msg: &str, fields: &[(&str, &str)]) {
        let mut line = format!("ERROR {msg}");
        for (k, v) in fields {
            line.push_str(&format!(" {k}={v:?}"));
        }
        eprintln!("{line}");
    }
}

#[derive(Default)]
pub(crate) struct RemoteState {
    pub(crate) connected: bool,
    pub(crate) last_sync: Option<SystemTime>,
    pub(crate) last_error: Option<TransportError>,
}

#[derive(Default)]
pub(crate) struct State {
    pub(crate) queue: VecDeque<Diff>,
    /// Absolute position of `queue[0]`: how many diffs have left the
    /// front, trimmed or dropped.
    pub(crate) base: u64,
    /// Per remote, the absolute position of the next diff to push.
    pub(crate) cursors: HashMap<String, u64>,
    pub(crate) statuses: HashMap<String, RemoteState>,
    /// Per remote, the newest timestamp pulled.
    pub(crate) pulls: HashMap<String, Timestamp>,
    pub(crate) ctx: Option<Context>,
    pub(crate) sub: Option<SubscriptionId>,
}

pub(crate) struct Inner<T: Entity + 'static> {
    pub(crate) repo: Arc<dyn Repository<T>>,
    pub(crate) bus: Option<DynBus>,
    pub(crate) remotes: RemoteSet,
    pub(crate) clock: Arc<Clock>,
    pub(crate) wall: Arc<dyn WallClock>,
    pub(crate) merge: Option<MergeFunc<T>>,
    // Read only by the timer threads, which wasm32 does not have.
    #[cfg_attr(all(target_arch = "wasm32", target_os = "unknown"), allow(dead_code))]
    pub(crate) interval: Duration,
    pub(crate) prefix: String,
    pub(crate) logger: Arc<dyn Logger>,
    pub(crate) state: Mutex<State>,
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    pub(crate) runner: super::runner::Runner,
}

/// Orchestrates multi-remote replication of one repository. Go's
/// `Replicator[T]`. Build one with [`Replicator::builder`].
pub struct Replicator<T: Entity + 'static> {
    pub(crate) inner: Arc<Inner<T>>,
}

/// Configures a [`Replicator`]; Go's `ReplicatorOption`s.
pub struct ReplicatorBuilder<T: Entity + 'static> {
    repo: Arc<dyn Repository<T>>,
    remotes: Vec<Remote>,
    interval: Duration,
    merge: Option<MergeFunc<T>>,
    bus: Option<DynBus>,
    clock: Option<Arc<Clock>>,
    wall: Option<Arc<dyn WallClock>>,
    logger: Option<Arc<dyn Logger>>,
    prefix: String,
}

impl<T> Replicator<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    /// Starts configuring a replicator for `repo`. Go's `NewReplicator`.
    pub fn builder(repo: Arc<dyn Repository<T>>) -> ReplicatorBuilder<T> {
        ReplicatorBuilder {
            repo,
            remotes: Vec::new(),
            interval: DEFAULT_INTERVAL,
            merge: None,
            bus: None,
            clock: None,
            wall: None,
            logger: None,
            prefix: DEFAULT_SUBSCRIPTION_PREFIX.to_string(),
        }
    }
}

impl<T> ReplicatorBuilder<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    /// Adds a remote; one whose name is already taken is ignored, as in
    /// Go. Go's `WithRemote`.
    pub fn remote(mut self, r: Remote) -> Self {
        self.remotes.push(r);
        self
    }

    /// The time between rounds, five seconds by default. Go's
    /// `WithInterval`.
    ///
    /// # Panics
    ///
    /// On a zero interval, as Go's ticker does.
    pub fn interval(mut self, d: Duration) -> Self {
        assert!(!d.is_zero(), "sync: non-positive interval");
        self.interval = d;
        self
    }

    /// Sets the conflict resolution function. Kept, not called: see the
    /// module docs. Go's `WithMergeFunc`.
    pub fn merge_func(mut self, f: MergeFunc<T>) -> Self {
        self.merge = Some(f);
        self
    }

    /// The bus to capture entity changes from and to publish `sync.*`
    /// events on. Go's `WithBus`.
    pub fn bus(mut self, b: DynBus) -> Self {
        self.bus = Some(b);
        self
    }

    /// The hybrid logical clock; by default one for node `local` on the
    /// builder's wall clock. Go's `WithClock`.
    pub fn clock(mut self, c: Arc<Clock>) -> Self {
        self.clock = Some(c);
        self
    }

    /// The wall clock for remote status times and, unless
    /// [`clock`](Self::clock) is given, the default HLC. The system
    /// clock by default, required on wasm32-unknown-unknown.
    pub fn wall_clock(mut self, w: Arc<dyn WallClock>) -> Self {
        self.wall = Some(w);
        self
    }

    /// Where apply-path errors go; stderr by default. Go's `WithLogger`.
    pub fn logger(mut self, l: Arc<dyn Logger>) -> Self {
        self.logger = Some(l);
        self
    }

    /// The 3-segment topic prefix to subscribe under. The replicator
    /// subscribes to `<prefix>.*` and dispatches on the last segment
    /// (`created`, `updated`, `deleted`), so the vocabulary is the same
    /// under any prefix. Pass the prefix the domain service publishes
    /// under. Defaults to [`DEFAULT_SUBSCRIPTION_PREFIX`]. Go's
    /// `WithSubscriptionPrefix`.
    ///
    /// # Panics
    ///
    /// If `prefix` is not exactly three non-empty dot-separated
    /// segments: caught at boot rather than as events silently missed.
    pub fn subscription_prefix(mut self, prefix: &str) -> Self {
        if let Err(e) = validate_prefix(prefix) {
            panic!("sync.WithSubscriptionPrefix({prefix:?}): {e}");
        }
        self.prefix = prefix.to_string();
        self
    }

    /// Builds the replicator. Nothing runs until
    /// [`start`](Replicator::start).
    ///
    /// # Panics
    ///
    /// On wasm32-unknown-unknown, if no [`wall_clock`](Self::wall_clock)
    /// was given: that target has no system clock.
    pub fn build(self) -> Replicator<T> {
        let wall = self.wall.unwrap_or_else(default_wall_clock);
        let clock = self
            .clock
            .unwrap_or_else(|| Arc::new(Clock::with_wall_clock("local", wall.clone())));
        let remotes = RemoteSet::new();
        for r in self.remotes {
            let _ = remotes.add(r);
        }
        Replicator {
            inner: Arc::new(Inner {
                repo: self.repo,
                bus: self.bus,
                remotes,
                clock,
                wall,
                merge: self.merge,
                interval: self.interval,
                prefix: self.prefix,
                logger: self.logger.unwrap_or_else(|| Arc::new(StderrLogger)),
                state: Mutex::default(),
                #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
                runner: Default::default(),
            }),
        }
    }
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn default_wall_clock() -> Arc<dyn WallClock> {
    Arc::new(super::clock::SystemWallClock)
}

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn default_wall_clock() -> Arc<dyn WallClock> {
    panic!("sync: no system clock on wasm32-unknown-unknown; set one with ReplicatorBuilder::wall_clock")
}

/// The 3-segment shape check of `bus::prefix_topics`, without composing a
/// full topic to validate it.
fn validate_prefix(prefix: &str) -> Result<(), String> {
    if prefix.is_empty() {
        return Err("prefix is empty".into());
    }
    if prefix.ends_with('.') {
        return Err(format!("prefix {prefix:?} must not end with '.'"));
    }
    let parts: Vec<&str> = prefix.split('.').collect();
    if parts.len() != 3 {
        return Err(format!(
            "prefix {prefix:?} has {} segments; expected 3 (source.category.object)",
            parts.len()
        ));
    }
    match parts.iter().position(|s| s.is_empty()) {
        Some(i) => Err(format!(
            "prefix {prefix:?} has empty segment at position {i}"
        )),
        None => Ok(()),
    }
}

impl<T> Replicator<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    /// Subscribes to bus events for change capture and starts a loop per
    /// registered remote (none on wasm32-unknown-unknown). The loops end
    /// at [`stop`](Self::stop) or when `ctx` is done. Call it once.
    pub fn start(&self, ctx: &Context) -> Result<(), SyncError> {
        let inner = &self.inner;
        inner.state().ctx = Some(ctx.clone());

        if let Some(bus) = &inner.bus {
            let handler = Arc::clone(inner);
            let id = bus.subscribe_ctx(format!("{}.*", inner.prefix), move |_, e| {
                handler.handle_event(e);
                Ok(())
            });
            inner.state().sub = Some(id);
        }

        for rem in inner.remotes.list() {
            inner
                .state()
                .statuses
                .insert(rem.name.clone(), RemoteState::default());
            inner.start_loop(ctx, &rem.name);
        }
        Ok(())
    }

    /// Ends every loop and waits for them; afterwards the replicator
    /// writes nothing more to the repository. Idempotent. Go's `Stop`.
    pub fn stop(&self) {
        #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
        self.inner.runner.stop();
        let sub = self.inner.state().sub.take();
        if let (Some(bus), Some(id)) = (&self.inner.bus, sub) {
            bus.unsubscribe(id);
        }
    }

    /// Registers a remote and starts its loop;
    /// [`SyncError::NotStarted`] before [`start`](Self::start).
    pub fn add_remote(&self, rem: Remote) -> Result<(), SyncError> {
        let inner = &self.inner;
        let ctx = inner.state().ctx.clone().ok_or(SyncError::NotStarted)?;
        let name = rem.name.clone();
        inner.remotes.add(rem)?;
        inner
            .state()
            .statuses
            .insert(name.clone(), RemoteState::default());
        inner.start_loop(&ctx, &name);
        Ok(())
    }

    /// Unregisters a remote by name and ends its loop.
    pub fn remove_remote(&self, name: &str) -> Result<(), SyncError> {
        self.inner.remotes.remove(name)
    }

    /// Adds a diff to the outbound queue, dropping the oldest at
    /// [`MAX_QUEUE_SIZE`]. For manual pushes and tests; the bus does it
    /// otherwise.
    pub fn enqueue(&self, d: Diff) {
        self.inner.enqueue(d);
    }

    /// A copy of the outbound queue, oldest first.
    pub fn queued(&self) -> Vec<Diff> {
        self.inner.state().queue.iter().cloned().collect()
    }

    /// How many diffs the outbound queue holds.
    pub fn queue_len(&self) -> usize {
        self.inner.state().queue.len()
    }

    /// The hybrid logical clock stamping local changes.
    pub fn clock(&self) -> &Arc<Clock> {
        &self.inner.clock
    }

    /// The configured merge function, if any (see the module docs).
    pub fn merge_func(&self) -> Option<&MergeFunc<T>> {
        self.inner.merge.as_ref()
    }

    /// Runs one round against the named remote now, as its loop does:
    /// ping, push, pull, trim. A transport failure is recorded in
    /// [`status`](Self::status), not returned; the error is for an
    /// unknown remote.
    pub fn sync_once(&self, ctx: &Context, name: &str) -> Result<(), SyncError> {
        let rem = self
            .inner
            .remotes
            .handle(name)
            .ok_or_else(|| SyncError::RemoteNotFound(name.to_string()))?;
        self.inner.sync_remote(ctx, &rem);
        Ok(())
    }

    /// Applies one pulled diff to the repository: a delete when it has no
    /// `after`, otherwise an update, falling back to a create when the
    /// update fails for any reason. Failures are logged, published as
    /// `sync.apply.error` and otherwise dropped.
    pub fn apply_diff(&self, ctx: &Context, d: &Diff) {
        self.inner.apply_diff(ctx, d);
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

impl<T> Inner<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    pub(crate) fn state(&self) -> MutexGuard<'_, State> {
        lock(&self.state)
    }

    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    fn start_loop(self: &Arc<Self>, ctx: &Context, name: &str) {
        if let Some(rem) = self.remotes.handle(name) {
            self.spawn_loop(ctx.clone(), rem);
        }
    }

    #[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
    fn start_loop(self: &Arc<Self>, _: &Context, _: &str) {}

    fn enqueue(&self, d: Diff) {
        let mut st = self.state();
        if st.queue.len() >= MAX_QUEUE_SIZE {
            st.queue.pop_front();
            st.base += 1;
        }
        st.queue.push_back(d);
    }

    /// One round: ping, push, pull, trim.
    pub(crate) fn sync_remote(&self, ctx: &Context, rem: &Remote) {
        if let Err(e) = rem.transport.ping(ctx) {
            self.set_status(&rem.name, false, Some(e));
            self.publish_event(ctx, "sync.remote.error", &rem.name);
            return;
        }
        self.set_status(&rem.name, true, None);
        self.publish_event(ctx, "sync.remote.connected", &rem.name);

        if rem.mode != SyncMode::PullOnly {
            self.push(ctx, rem);
        }
        if rem.mode != SyncMode::PushOnly {
            self.pull(ctx, rem);
        }
        self.trim_queue();

        let now = self.wall.wall_time();
        if let Some(s) = self.state().statuses.get_mut(&rem.name) {
            s.last_sync = Some(now);
        }
        self.publish_event(ctx, "sync.remote.synced", &rem.name);
    }

    fn push(&self, ctx: &Context, rem: &Remote) {
        let (from, pending) = {
            let st = self.state();
            let from = st.cursors.get(&rem.name).copied().unwrap_or(0).max(st.base);
            let skip = usize::try_from(from - st.base).unwrap_or(usize::MAX);
            (
                from,
                st.queue.iter().skip(skip).cloned().collect::<Vec<_>>(),
            )
        };
        if pending.is_empty() {
            return;
        }

        let filtered: Vec<Diff> = match &rem.filter {
            Some(f) => pending.iter().filter(|d| f(d)).cloned().collect(),
            None => pending.clone(),
        };
        if !filtered.is_empty() {
            if let Err(e) = rem.transport.push(ctx, &filtered) {
                self.set_status(&rem.name, true, Some(e));
                return;
            }
        }
        self.state()
            .cursors
            .insert(rem.name.clone(), from + pending.len() as u64);
    }

    fn pull(&self, ctx: &Context, rem: &Remote) {
        let since = self
            .state()
            .pulls
            .get(&rem.name)
            .cloned()
            .unwrap_or_default();
        let diffs = match rem.transport.pull(ctx, &since) {
            Ok(d) => d,
            Err(e) => {
                self.set_status(&rem.name, true, Some(e));
                return;
            }
        };

        let mut newest = Timestamp::default();
        for d in &diffs {
            self.clock.update(&d.timestamp);
            self.apply_diff(ctx, d);
            if newest.before(&d.timestamp) {
                newest = d.timestamp.clone();
            }
        }
        if !diffs.is_empty() {
            self.state().pulls.insert(rem.name.clone(), newest);
        }
    }

    /// Drops what every remote has passed. A remote with no cursor yet
    /// counts as at the start.
    fn trim_queue(&self) {
        let remotes = self.remotes.list();
        let mut st = self.state();
        let mut min = st.base + st.queue.len() as u64;
        for rem in &remotes {
            min = min.min(st.cursors.get(&rem.name).copied().unwrap_or(0));
        }
        if min > st.base {
            let n = usize::try_from(min - st.base).unwrap_or(usize::MAX);
            st.queue.drain(..n);
            st.base = min;
        }
    }

    pub(crate) fn apply_diff(&self, ctx: &Context, d: &Diff) {
        let Some(after) = &d.after else {
            if let Err(e) = self.repo.delete(ctx, &d.entity_id) {
                self.apply_failed(
                    ctx,
                    "sync: apply delete failed",
                    &d.entity_id,
                    &e.to_string(),
                );
            }
            return;
        };
        let entity: T = match serde_json::from_slice(after) {
            Ok(e) => e,
            Err(e) => {
                self.apply_failed(
                    ctx,
                    "sync: unmarshal diff failed",
                    &d.entity_id,
                    &e.to_string(),
                );
                return;
            }
        };
        if self.repo.update(ctx, &entity).is_err() {
            if let Err(e) = self.repo.create(ctx, &entity) {
                self.apply_failed(
                    ctx,
                    "sync: apply create failed",
                    &d.entity_id,
                    &e.to_string(),
                );
            }
        }
    }

    fn apply_failed(&self, ctx: &Context, msg: &str, entity_id: &str, err: &str) {
        self.logger
            .error(msg, &[("entity_id", entity_id), ("err", err)]);
        self.publish_event(ctx, "sync.apply.error", entity_id);
    }

    /// Turns a `<prefix>.created|updated|deleted` bus event into a
    /// queued diff, snapshotting the entity as it is now.
    pub(crate) fn handle_event(&self, e: &Event) {
        let Some(id) = e
            .payload
            .get("id")
            .and_then(Value::as_str)
            .filter(|s| !s.is_empty())
        else {
            return;
        };
        let topic = e.topic.as_str();
        let Some((_, action)) = topic.rsplit_once('.') else {
            return;
        };
        let operation = match action {
            "created" => Operation::Create,
            "updated" => Operation::Update,
            "deleted" => Operation::Delete,
            _ => return,
        };

        let ts = self.clock.now();
        let mut d = Diff {
            entity_id: id.to_string(),
            operation,
            node_id: ts.node_id.clone(),
            timestamp: ts,
            ..Diff::default()
        };
        if operation != Operation::Delete {
            let ctx = self.state().ctx.clone().unwrap_or_default();
            if let Ok(entity) = self.repo.get(&ctx, id) {
                d.after = serde_json::to_vec(&entity).ok();
            }
        }
        self.enqueue(d);
    }

    fn set_status(&self, name: &str, connected: bool, err: Option<TransportError>) {
        let mut st = self.state();
        let s = st.statuses.entry(name.to_string()).or_default();
        s.connected = connected;
        s.last_error = err;
    }

    fn publish_event(&self, ctx: &Context, topic: &str, remote: &str) {
        let Some(bus) = &self.bus else { return };
        let event = Event::new_at(
            topic,
            format!("sync.replicator.{remote}"),
            json!({ "remote": remote }),
            self.wall.wall_time(),
        );
        let _ = bus.publish_ctx(ctx, &event);
    }
}
