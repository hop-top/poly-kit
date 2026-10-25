//! [`Reloadable`]: atomic in-process hot reload with an immutability
//! veto and bus events. Ports Go `reloadable.go` and `reload_events.go`.

use std::collections::BTreeMap;
use std::fmt;
use std::sync::{Arc, Mutex, RwLock};

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::partition::{partition, FieldDiff, ReloadFields};
use super::yaml::{de::from_node, ser::to_node};
use super::Value;
use super::{load, Error, Options};
use crate::bus::{prefix_topics, Topic};

/// Where reload events go. The bus's [`SharedBus`](crate::bus::SharedBus)
/// implements it; Go's counterpart is `domain.EventPublisher`.
pub trait ReloadPublisher: Send + Sync {
    /// Publishes one event. Errors are dropped by [`Reloadable`].
    fn publish(&self, topic: &str, source: &str, payload: serde_json::Value) -> Result<(), String>;
}

impl ReloadPublisher for crate::bus::SharedBus {
    fn publish(&self, topic: &str, source: &str, payload: serde_json::Value) -> Result<(), String> {
        crate::bus::SharedBus::publish(self, &crate::bus::Event::new(topic, source, payload))
            .map_err(|e| e.to_string())
    }
}

/// Topics for the two reload outcomes.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ReloadTopics {
    pub reloaded: Topic,
    pub reload_failed: Topic,
}

impl Default for ReloadTopics {
    /// `kit.config.snapshot.reloaded` / `kit.config.snapshot.reload_failed`.
    fn default() -> Self {
        ReloadTopics {
            reloaded: Topic::from("kit.config.snapshot.reloaded"),
            reload_failed: Topic::from("kit.config.snapshot.reload_failed"),
        }
    }
}

const RELOAD_ACTIONS: [&str; 2] = ["reloaded", "reload_failed"];
const DEFAULT_RELOAD_SOURCE: &str = "core.config";

/// Why a reload failed (`reason` on the failure event).
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub enum ReloadFailReason {
    #[serde(rename = "immutable_changed")]
    ImmutableChanged,
    #[serde(rename = "load_error")]
    LoadError,
}

/// Payload of the `reloaded` event. `mutable_diff` may be empty (a
/// no-op reload still fires).
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ReloadedPayload {
    pub mutable_diff: BTreeMap<String, FieldDiff>,
    pub source_paths: Vec<String>,
}

/// Payload of the `reload_failed` event.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct ReloadFailedPayload {
    pub reason: ReloadFailReason,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub offending: Vec<String>,
    pub error: String,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub mutable_diff: BTreeMap<String, FieldDiff>,
    pub source_paths: Vec<String>,
}

/// Construction options.
#[derive(Clone, Default)]
pub struct ReloadOptions {
    publisher: Option<Arc<dyn ReloadPublisher>>,
    topics: ReloadTopics,
    source: Option<String>,
}

impl fmt::Debug for ReloadOptions {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ReloadOptions")
            .field("publisher", &self.publisher.is_some())
            .field("topics", &self.topics)
            .field("source", &self.source)
            .finish()
    }
}

impl ReloadOptions {
    /// Emits reload events through `p` (opt-in; none by default).
    /// Publishing is fire-and-forget on a thread and never fails a
    /// reload.
    pub fn publisher(mut self, p: Arc<dyn ReloadPublisher>) -> Self {
        self.publisher = Some(p);
        self
    }

    /// Replaces topics; an empty one keeps its default.
    pub fn topics(mut self, t: ReloadTopics) -> Self {
        let d = ReloadTopics::default();
        self.topics = ReloadTopics {
            reloaded: if t.reloaded.0.is_empty() {
                d.reloaded
            } else {
                t.reloaded
            },
            reload_failed: if t.reload_failed.0.is_empty() {
                d.reload_failed
            } else {
                t.reload_failed
            },
        };
        self
    }

    /// Both topics from a `source.category.object` prefix. Fails with
    /// the bus validator's message for a bad prefix.
    pub fn try_topic_prefix(mut self, prefix: &str) -> Result<Self, Error> {
        let (tm, err) = prefix_topics(prefix, &RELOAD_ACTIONS);
        if let Some(e) = err {
            return Err(Error::invalid(e));
        }
        self.topics = ReloadTopics {
            reloaded: tm["reloaded"].clone(),
            reload_failed: tm["reload_failed"].clone(),
        };
        Ok(self)
    }

    /// [`try_topic_prefix`](Self::try_topic_prefix) that panics, as Go's
    /// `WithReloadTopicPrefix` does: a bad prefix is a wiring bug.
    pub fn topic_prefix(self, prefix: &str) -> Self {
        match self.try_topic_prefix(prefix) {
            Ok(o) => o,
            Err(e) => panic!(
                "config.WithReloadTopicPrefix({}): {e}",
                super::goquote::quote(prefix)
            ),
        }
    }

    /// The `source` on every event; `core.config` by default.
    pub fn source(mut self, src: impl Into<String>) -> Self {
        self.source = Some(src.into());
        self
    }
}

/// A typed config snapshot with atomic hot reload.
///
/// Readers take [`snapshot`](Self::snapshot) (an `Arc`; treat it as
/// immutable). [`reload`](Self::reload) re-runs [`load`], vetoes any
/// change to a field not marked reloadable ([`ReloadFields`]), and
/// otherwise swaps the snapshot. Reloads are serialized; readers never
/// block on one.
pub struct Reloadable<T> {
    cur: RwLock<Arc<T>>,
    opts: Mutex<Options>,
    publisher: Option<Arc<dyn ReloadPublisher>>,
    topics: ReloadTopics,
    source: String,
}

impl<T> Reloadable<T>
where
    T: Serialize + DeserializeOwned + ReloadFields + Default + Send + Sync + 'static,
{
    /// Wraps `initial` (a freshly loaded value) with the `opts` used for
    /// later reloads.
    pub fn new(initial: T, opts: Options, options: ReloadOptions) -> Self {
        Reloadable {
            cur: RwLock::new(Arc::new(initial)),
            opts: Mutex::new(opts),
            publisher: options.publisher,
            topics: options.topics,
            source: options
                .source
                .unwrap_or_else(|| DEFAULT_RELOAD_SOURCE.to_string()),
        }
    }

    /// The current snapshot.
    pub fn snapshot(&self) -> Arc<T> {
        self.cur.read().unwrap_or_else(|e| e.into_inner()).clone()
    }

    /// A copy of the options the next argument-less reload uses.
    pub fn options(&self) -> Options {
        self.opts.lock().unwrap_or_else(|e| e.into_inner()).clone()
    }

    /// Reloads with `new_opts`: on success swaps the snapshot, keeps
    /// `new_opts` and publishes `reloaded` with the reloadable diff. A
    /// changed immutable field fails with
    /// [`ErrorKind::ImmutableChanged`](super::ErrorKind::ImmutableChanged);
    /// a load failure with `config reload: <cause>`. Either way the
    /// snapshot stays and `reload_failed` is published.
    pub fn reload(&self, new_opts: Options) -> Result<(), Error> {
        let mut held = self.opts.lock().unwrap_or_else(|e| e.into_inner());
        let old = self.snapshot();
        let mut fresh = T::default();
        // As in Go, the event carries the cause; the return wraps it.
        let fail = |e: Error, ctx: &str| {
            self.failed(
                &new_opts,
                ReloadFailReason::LoadError,
                Vec::new(),
                &e,
                BTreeMap::new(),
            );
            e.wrap(ctx)
        };
        if let Err(e) = load(&mut fresh, &new_opts) {
            return Err(fail(e, "config reload"));
        }
        let (mutable, immutable) = partition::<T>();
        let (o, n) = match to_value(old.as_ref()).and_then(|o| Ok((o, to_value(&fresh)?))) {
            Ok(v) => v,
            Err(e) => return Err(fail(e, "config reload")),
        };
        let imm =
            diff_paths(&o, &n, &immutable).map_err(|e| fail(e, "config reload: diff immutable"))?;
        if !imm.is_empty() {
            let offending: Vec<String> = imm.keys().cloned().collect();
            let e = Error::immutable(offending.clone());
            // Go reports the immutable diff here, in mutable_diff.
            self.failed(
                &new_opts,
                ReloadFailReason::ImmutableChanged,
                offending,
                &e,
                imm,
            );
            return Err(e);
        }
        let mt =
            diff_paths(&o, &n, &mutable).map_err(|e| fail(e, "config reload: diff mutable"))?;
        *self.cur.write().unwrap_or_else(|e| e.into_inner()) = Arc::new(fresh);
        let payload = ReloadedPayload {
            mutable_diff: mt,
            source_paths: source_paths(&new_opts),
        };
        *held = new_opts;
        self.publish(&self.topics.reloaded, &payload);
        Ok(())
    }

    fn failed(
        &self,
        opts: &Options,
        reason: ReloadFailReason,
        offending: Vec<String>,
        err: &Error,
        diff: BTreeMap<String, FieldDiff>,
    ) {
        let payload = ReloadFailedPayload {
            reason,
            offending,
            error: err.to_string(),
            mutable_diff: diff,
            source_paths: source_paths(opts),
        };
        self.publish(&self.topics.reload_failed, &payload);
    }

    fn publish<P: Serialize>(&self, topic: &Topic, payload: &P) {
        let Some(p) = self.publisher.clone() else {
            return;
        };
        if topic.0.is_empty() {
            return;
        }
        let Ok(json) = serde_json::to_value(payload) else {
            return;
        };
        let (topic, source) = (topic.0.clone(), self.source.clone());
        std::thread::spawn(move || {
            let _ = p.publish(&topic, &source, json);
        });
    }
}

/// System, user, project, then extra paths; unset slots stay as `""`.
fn source_paths(opts: &Options) -> Vec<String> {
    let mut out: Vec<String> = [
        &opts.system_config_path,
        &opts.user_config_path,
        &opts.project_config_path,
    ]
    .iter()
    .map(|p| p.display().to_string())
    .collect();
    out.extend(
        opts.extra_config_paths
            .iter()
            .map(|p| p.display().to_string()),
    );
    out
}

/// Checks `T::reload_fields()` against `T::default()` as serialized:
/// every listed field exists under that name and every serialized field
/// is listed. Call it from a test of each config type; an `Option`
/// struct that is `None` by default is not looked into.
pub fn check_reload_fields<T: ReloadFields + Serialize + Default>() -> Result<(), Error> {
    let v = to_value(&T::default())?;
    let mut out = Vec::new();
    super::partition::mismatches(&T::reload_fields(), &v, "", &mut out);
    if out.is_empty() {
        Ok(())
    } else {
        Err(Error::invalid(format!(
            "config: ReloadFields drift: {}",
            out.join("; ")
        )))
    }
}

/// Go `lookupPath`: a null on the way (an empty `Option`) resolves to
/// null; a non-mapping on the way does not resolve.
fn lookup(v: &Value, path: &str) -> Option<Value> {
    let mut cur = v;
    for seg in path.split('.') {
        match cur {
            Value::Null => return Some(Value::Null),
            Value::Map(m) => cur = m.get(seg)?,
            _ => return None,
        }
    }
    Some(cur.clone())
}

fn to_value<T: Serialize>(v: &T) -> Result<Value, Error> {
    Ok(from_node(&to_node(v)?)?)
}

/// The paths among `paths` whose values differ between `old` and
/// `fresh`.
fn diff_paths(
    old: &Value,
    fresh: &Value,
    paths: &[String],
) -> Result<BTreeMap<String, FieldDiff>, Error> {
    let mut out = BTreeMap::new();
    for p in paths {
        let (o, n) = (lookup(old, p), lookup(fresh, p));
        let (Some(o), Some(n)) = (o.clone(), n.clone()) else {
            return Err(Error::invalid(format!(
                "config.diffPaths: path {} not resolvable (old={} new={})",
                super::goquote::quote(p),
                o.is_some(),
                n.is_some()
            )));
        };
        if o != n {
            out.insert(p.clone(), FieldDiff { from: o, to: n });
        }
    }
    Ok(out)
}
