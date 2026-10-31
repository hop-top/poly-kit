//! The served-call gate: what a transport asks before it runs a command
//! for a remote caller. Ports the permission slot Go's
//! `console/cli/service.servePermission` composes (`serve_auth.go`), in
//! Go's order:
//!
//! 1. the policy engine's verdict for the caller the transport
//!    established (the first matching `callers:` rule, then the policy's
//!    own `allow`), and, for a caller rule with `max_ops`, a read-only
//!    check that the window still has a call left;
//! 2. the policy's `permissions:` rules ([`PermissionGate`]);
//! 3. the adopter's check ([`ServedGate::with_permission`]);
//! 4. the charge: one call taken from the caller's budget, only now
//!    that every decider above admitted the call.
//!
//! The first refusal stands and later deciders are not asked, so a call
//! the rules or the adopter refuse spends nothing, and a probe never
//! spends. Only a write or destructive command counts against a budget.
//! A budget belongs to one policy rule and one principal in one tenant,
//! over fixed windows; callers sharing a gate share it, whichever
//! surface they call on.
//!
//! The caller is mapped as [`CallerSource`] says: `Verified` is matched
//! by its principal, tenant and scopes; `Transport` (the owner-only
//! socket) holds the owner's authority and matches no principal rule;
//! `None` is nobody, which no caller rule matches and no budget counts.
//!
//! The counts are in memory ([`UsageLedger`]): they do not survive a
//! restart and are not shared between processes, where Go keeps them in
//! a `kv` store.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::permission::{CallerSource, PermissionGate, PermissionRequest};
use super::policy::{Caller, CommandRef, Engine, Policy};

/// The window a `callers:` budget counts over when its rule names none.
pub const DEFAULT_BUDGET_WINDOW: Duration = Duration::from_secs(3600);

/// Seconds between the zero time Go aligns windows to (year 1) and the
/// Unix epoch.
const GO_ZERO_TO_UNIX_SECS: u128 = 62_135_596_800;

/// What a call has used in one window of a [`UsageLedger`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Usage {
    /// Calls counted.
    pub ops: i64,
    /// When the window ends and the count starts over.
    pub reset: SystemTime,
}

type Clock = Arc<dyn Fn() -> SystemTime + Send + Sync>;

/// Counts calls per key over fixed windows, in memory. Windows are
/// aligned as Go's `time.Truncate` aligns them (a one-hour window runs
/// from the top of one hour to the next, a day from midnight UTC); a
/// count belongs to the window it was made in and is never carried into
/// the next. Check and count are atomic.
#[derive(Clone)]
pub struct UsageLedger {
    now: Clock,
    counts: Arc<Mutex<HashMap<String, (u128, i64)>>>,
}

impl Default for UsageLedger {
    fn default() -> Self {
        UsageLedger::new()
    }
}

impl std::fmt::Debug for UsageLedger {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("UsageLedger")
    }
}

impl UsageLedger {
    /// A ledger on the system clock.
    pub fn new() -> UsageLedger {
        UsageLedger::with_clock(SystemTime::now)
    }

    /// A ledger on `now`; for tests that cross a window boundary.
    pub fn with_clock<F>(now: F) -> UsageLedger
    where
        F: Fn() -> SystemTime + Send + Sync + 'static,
    {
        UsageLedger {
            now: Arc::new(now),
            counts: Arc::default(),
        }
    }

    /// The window of length `w_ns` holding now: its start, as
    /// nanoseconds since Go's zero time.
    fn window(&self, w: Duration) -> Result<u128, String> {
        if w < Duration::from_secs(1) {
            return Err(format!("window {} is under a second", go_duration(w)));
        }
        let since_unix = (self.now)()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "clock is before 1970".to_string())?
            .as_nanos();
        let t = since_unix + GO_ZERO_TO_UNIX_SECS * 1_000_000_000;
        let w_ns = w.as_nanos();
        Ok(t - t % w_ns)
    }

    fn reset_of(start: u128, w: Duration) -> SystemTime {
        let end = start + w.as_nanos();
        let unix_ns = end - GO_ZERO_TO_UNIX_SECS * 1_000_000_000;
        UNIX_EPOCH
            + Duration::new(
                (unix_ns / 1_000_000_000) as u64,
                (unix_ns % 1_000_000_000) as u32,
            )
    }

    /// What `key` has used in the current window of length `w`.
    pub fn usage(&self, key: &str, w: Duration) -> Result<Usage, String> {
        let start = self.window(w)?;
        let counts = self.counts.lock().unwrap_or_else(PoisonError::into_inner);
        let ops = match counts.get(key) {
            Some(&(s, ops)) if s == start => ops,
            _ => 0,
        };
        Ok(Usage {
            ops,
            reset: Self::reset_of(start, w),
        })
    }

    /// Counts one call against `key` in the current window when the
    /// window has counted fewer than `max_ops`; reports whether it did,
    /// with the window's usage after.
    pub fn take(&self, key: &str, w: Duration, max_ops: i64) -> Result<(Usage, bool), String> {
        let start = self.window(w)?;
        let mut counts = self.counts.lock().unwrap_or_else(PoisonError::into_inner);
        let entry = counts.entry(key.to_string()).or_insert((start, 0));
        if entry.0 != start {
            *entry = (start, 0);
        }
        let took = entry.1 < max_ops;
        if took {
            entry.1 += 1;
        }
        Ok((
            Usage {
                ops: entry.1,
                reset: Self::reset_of(start, w),
            },
            took,
        ))
    }
}

/// One served call as the gate sees it: the request the rules read, the
/// command's `kit/permissions` annotation and whether it is only a
/// probe (a discovery check that must spend nothing).
#[derive(Clone, Debug)]
pub struct ServedCall<'a> {
    /// The invocation: caller, surface, command and payload.
    pub request: &'a PermissionRequest,
    /// The command's `kit/permissions` value, when declared.
    pub permissions: Option<&'a str>,
    /// A probe asks without spending.
    pub probe: bool,
}

type Adopter = dyn Fn(&PermissionRequest) -> Result<(), String> + Send + Sync;

/// The served-call gate of one policy. See the module docs.
pub struct ServedGate {
    engine: Engine,
    rules: Option<PermissionGate>,
    adopter: Option<Arc<Adopter>>,
    ledger: UsageLedger,
}

impl std::fmt::Debug for ServedGate {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("ServedGate")
            .field("policy", &self.engine.policy().name)
            .field("rules", &self.rules)
            .finish()
    }
}

impl ServedGate {
    /// Validates `policy` and compiles its `permissions:` rules. A
    /// policy that does not validate, or a rule that does not compile,
    /// fails here naming it rather than denying at the first call.
    pub fn new(policy: Policy) -> Result<ServedGate, String> {
        policy.validate()?;
        let rules = if policy.permissions.is_empty() {
            None
        } else {
            Some(PermissionGate::new(&policy.permissions)?)
        };
        Ok(ServedGate {
            engine: Engine::new(policy, 0),
            rules,
            adopter: None,
            ledger: UsageLedger::new(),
        })
    }

    /// Counts budgets in `ledger`; share one between gates so a
    /// principal's budget is one budget.
    #[must_use]
    pub fn with_ledger(mut self, ledger: UsageLedger) -> ServedGate {
        self.ledger = ledger;
        self
    }

    /// Installs the adopter's check, asked after the policy and its
    /// rules and before the charge: `Err` is the refusal's reason. It
    /// can only narrow what they admitted.
    #[must_use]
    pub fn with_permission<F>(mut self, f: F) -> ServedGate
    where
        F: Fn(&PermissionRequest) -> Result<(), String> + Send + Sync + 'static,
    {
        self.adopter = Some(Arc::new(f));
        self
    }

    /// The ledger the budgets are counted in.
    pub fn ledger(&self) -> &UsageLedger {
        &self.ledger
    }

    /// The policy in force.
    pub fn policy(&self) -> &Policy {
        self.engine.policy()
    }

    /// Whether `call` may run: `Err` is the refusal's reason, nothing
    /// spent. An admitted call that counts against a budget has spent
    /// one.
    pub fn decide(&self, call: &ServedCall<'_>) -> Result<(), String> {
        let req = call.request;
        let verb = req.path.join(" ");
        let cmd = CommandRef {
            verb: &verb,
            side_effect: Some(req.tier.as_str()).filter(|t| !t.is_empty()),
            permissions: call.permissions,
        };
        let caller = policy_caller(req);
        let verdict = self.engine.authorize_for(&cmd, caller.as_ref());
        if !verdict.allowed {
            return Err(verdict.reason);
        }
        let budget = caller
            .as_ref()
            .filter(|_| self.engine.mutating(&cmd))
            .and_then(|c| self.budget_for(c));
        if let Some(b) = &budget {
            let u = self.ledger.usage(&b.key, b.window).map_err(unavailable)?;
            if u.ops >= b.max_ops {
                return Err(spent(b, u.reset));
            }
        }
        if let Some(rules) = &self.rules {
            let d = rules.decide(req);
            if !d.allowed {
                return Err(d.reason);
            }
        }
        if let Some(adopter) = &self.adopter {
            adopter(req)?;
        }
        if let (Some(b), false) = (&budget, call.probe) {
            let (u, took) = self
                .ledger
                .take(&b.key, b.window, b.max_ops)
                .map_err(unavailable)?;
            if !took {
                return Err(spent(b, u.reset));
            }
        }
        Ok(())
    }

    /// The budget the first caller rule matching `c` gives it; `None`
    /// when no rule matches or the matching rule sets no `max_ops`.
    fn budget_for(&self, c: &Caller) -> Option<Budget> {
        let p = self.engine.policy();
        let (i, rule) = p.callers.iter().enumerate().find(|(_, r)| r.matches(c))?;
        if rule.max_ops <= 0 {
            return None;
        }
        Some(Budget {
            key: format!(
                "policy/{}/{i}/{}/{}",
                path_escape(&p.name),
                path_escape(&c.principal),
                path_escape(&c.tenant)
            ),
            max_ops: rule.max_ops,
            window: if rule.window.is_zero() {
                DEFAULT_BUDGET_WINDOW
            } else {
                rule.window
            },
        })
    }
}

struct Budget {
    key: String,
    max_ops: i64,
    window: Duration,
}

fn unavailable(e: String) -> String {
    format!("policy: max_ops budget unavailable: {e}")
}

fn spent(b: &Budget, reset: SystemTime) -> String {
    format!(
        "policy: max_ops budget of {} per {} spent; resets at {}",
        b.max_ops,
        go_duration(b.window),
        rfc3339(reset)
    )
}

/// The policy's view of the caller a request describes: Go's
/// `cli.PolicyCaller`. A caller the transport vouched for holds the
/// owner's authority and is named by nothing it claims; a verified one
/// holds its credential's scopes only; nobody is `None`.
pub fn policy_caller(req: &PermissionRequest) -> Option<Caller> {
    match req.source {
        CallerSource::Transport => Some(Caller {
            owner: true,
            ..Caller::default()
        }),
        CallerSource::Verified => Some(Caller {
            principal: req.caller.clone(),
            tenant: req.tenant.clone(),
            scopes: req.scopes.clone(),
            owner: false,
        }),
        CallerSource::None => None,
    }
}

/// Go's `url.PathEscape`.
fn path_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' => out.push(b as char),
            b'-' | b'_' | b'.' | b'~' | b'$' | b'&' | b'+' | b'=' | b':' | b'@' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// Go's `time.Duration.String`.
fn go_duration(d: Duration) -> String {
    let ns = d.as_nanos();
    if ns == 0 {
        return "0s".into();
    }
    let frac = |v: u128, unit: u128| -> String {
        let (whole, rem) = (v / unit, v % unit);
        if rem == 0 {
            return whole.to_string();
        }
        let digits = unit.ilog10() as usize;
        let f = format!("{rem:0digits$}");
        format!("{whole}.{}", f.trim_end_matches('0'))
    };
    if ns < 1_000 {
        return format!("{ns}ns");
    }
    if ns < 1_000_000 {
        return format!("{}µs", frac(ns, 1_000));
    }
    if ns < 1_000_000_000 {
        return format!("{}ms", frac(ns, 1_000_000));
    }
    let secs_ns = ns % 60_000_000_000;
    let total_min = ns / 60_000_000_000;
    let (h, m) = (total_min / 60, total_min % 60);
    let s = format!("{}s", frac(secs_ns, 1_000_000_000));
    match (h, m) {
        (0, 0) => s,
        (0, m) => format!("{m}m{s}"),
        (h, m) => format!("{h}h{m}m{s}"),
    }
}

/// `t` as RFC 3339 in UTC, to the second.
fn rfc3339(t: SystemTime) -> String {
    let secs = t.duration_since(UNIX_EPOCH).map_or(0, |d| d.as_secs());
    let (days, rem) = (secs / 86_400, secs % 86_400);
    // Civil date from a day count (Hinnant's algorithm).
    let z = days as i64 + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

#[cfg(test)]
mod tests;
