//! [`GrantsBuilder`]: the options of a [`Grants`] session.

use std::sync::{Arc, Mutex};
use std::time::SystemTime;

use super::audit::{Auditor, Delivery};
use super::grants::{Clock, Grants, Session};
use super::store::GrantStore;
use super::telemetry::{os_env, Env};

impl Grants {
    /// A session over `store`, auditing nothing until
    /// [`GrantsBuilder::auditor`] is set.
    pub fn builder(store: Arc<dyn GrantStore>) -> GrantsBuilder {
        GrantsBuilder {
            store,
            audit: None,
            clock: None,
            env: os_env(),
            app_prefix: String::new(),
            prompt_version: 1,
            delivery: None,
            #[cfg(feature = "consent-file")]
            relay: None,
            #[cfg(feature = "consent-file")]
            file_auditor: None,
        }
    }
}

/// Configures [`Grants`].
pub struct GrantsBuilder {
    store: Arc<dyn GrantStore>,
    audit: Option<Arc<dyn Auditor>>,
    clock: Option<Clock>,
    env: Env,
    app_prefix: String,
    prompt_version: i64,
    delivery: Option<Arc<dyn Delivery>>,
    #[cfg(feature = "consent-file")]
    pub(crate) relay: Option<Arc<super::relay::Relay>>,
    #[cfg(feature = "consent-file")]
    pub(crate) file_auditor: Option<Arc<super::file_audit::FileAuditor>>,
}

impl GrantsBuilder {
    /// Records every audit event. Without one nothing is audited.
    pub fn auditor(mut self, a: Arc<dyn Auditor>) -> Self {
        self.audit = Some(a);
        self
    }

    /// The clock. Required on wasm32-unknown-unknown, which has none;
    /// elsewhere the system clock.
    pub fn clock(mut self, now: impl Fn() -> SystemTime + Send + Sync + 'static) -> Self {
        self.clock = Some(Arc::new(now));
        self
    }

    /// The env lookup for the telemetry precedence (`""` when unset).
    pub fn env(mut self, env: impl Fn(&str) -> String + Send + Sync + 'static) -> Self {
        self.env = Arc::new(env);
        self
    }

    /// The `<APP>_TELEMETRY_MODE` prefix for telemetry checks.
    pub fn app_prefix(mut self, p: impl Into<String>) -> Self {
        self.app_prefix = p.into();
        self
    }

    /// Stamps grants decided through `request`. Default 1.
    pub fn prompt_version(mut self, v: i64) -> Self {
        self.prompt_version = v;
        self
    }

    /// Notified after every audit record; sends the audit-failed
    /// refusals no auditor could record.
    pub fn delivery(mut self, d: Arc<dyn Delivery>) -> Self {
        self.delivery = Some(d);
        self
    }

    /// Builds the session.
    ///
    /// # Panics
    ///
    /// On wasm32-unknown-unknown without [`GrantsBuilder::clock`]: a
    /// wiring bug, like a missing store.
    pub fn build(self) -> Grants {
        Grants {
            store: self.store,
            audit: self.audit,
            clock: self.clock.unwrap_or_else(default_clock),
            env: self.env,
            app_prefix: self.app_prefix,
            prompt_version: self.prompt_version,
            delivery: self.delivery,
            #[cfg(feature = "consent-file")]
            relay: self.relay,
            session: Mutex::new(Session::default()),
        }
    }
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[allow(clippy::disallowed_methods)]
fn default_clock() -> Clock {
    Arc::new(SystemTime::now)
}

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn default_clock() -> Clock {
    panic!("consent: GrantsBuilder::clock is required on wasm32-unknown-unknown")
}
