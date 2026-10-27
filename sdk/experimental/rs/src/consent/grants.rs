//! `Grants`: check, request, use, decide, revoke, list, end_session.

use std::collections::{BTreeMap, HashSet};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::SystemTime;

use super::audit::{AuditEvent, AuditKind, Auditor, Delivery, RefusalCause};
use super::error::{Error, Reason, RefusedError};
use super::grant::{
    sort_grants, validate_key, DecisionSource, Duration, Effect, Grant, Key, CAP_TELEMETRY,
};
use super::prompt::{Prompt, Prompter};
use super::store::GrantStore;
use super::telemetry::{resolve, Env, TelemetryDecision, DENIED};
use super::time::truncate;

/// The clock: the current time.
pub type Clock = Arc<dyn Fn() -> SystemTime + Send + Sync>;

/// The outcome of a check. `grant` is the governing grant (for
/// `Expired`, the grant that expired); `None` when `Unset`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Verdict {
    pub allowed: bool,
    pub reason: Reason,
    pub grant: Option<Grant>,
}

fn verdict_of(g: Grant) -> Verdict {
    let allowed = g.decision == Effect::Allow;
    Verdict {
        allowed,
        reason: if allowed {
            Reason::Allowed
        } else {
            Reason::Denied
        },
        grant: Some(g),
    }
}

/// The verdict of the grants held for one key: any deny, else the
/// first allow; `None` when none is held.
fn deny_wins_verdict(held: Vec<Grant>) -> Option<Verdict> {
    let deny = held.iter().position(|g| g.decision == Effect::Deny);
    let pick = deny.or_else(|| (!held.is_empty()).then_some(0))?;
    held.into_iter().nth(pick).map(verdict_of)
}

fn cause_of(v: &Verdict) -> RefusalCause {
    match v.reason {
        Reason::Denied => RefusalCause::StandingDeny,
        Reason::Expired => RefusalCause::Expired,
        _ => RefusalCause::NoPrompter,
    }
}

fn refused(capability: &str, subject: &str, reason: Reason, cause: Option<String>) -> Error {
    Error::Refused(RefusedError {
        capability: capability.to_string(),
        subject: subject.to_string(),
        reason,
        cause,
    })
}

#[derive(Default)]
pub(super) struct Session {
    mem: BTreeMap<Key, Grant>,
    refused: HashSet<(String, String, RefusalCause)>,
}

/// Answers "may this subject use this capability now", records
/// decisions, and audits every grant, deny, revoke, use and refusal.
///
/// One value is one session: once and session grants live in it and
/// die with it (or with [`Grants::end_session`]); until and always
/// grants go to the [`GrantStore`]. Every path fails closed.
///
/// Lookup for (capability, subject): the subject's own grant, then the
/// any-subject grant. Within one key every record held (in memory and
/// stored; for telemetry also the legacy records and env) is combined
/// deny-wins. An expired until grant counts as absent.
pub struct Grants {
    pub(super) store: Arc<dyn GrantStore>,
    pub(super) audit: Option<Arc<dyn Auditor>>,
    pub(super) clock: Clock,
    pub(super) env: Env,
    pub(super) app_prefix: String,
    pub(super) prompt_version: i64,
    pub(super) delivery: Option<Arc<dyn Delivery>>,
    #[cfg(feature = "consent-file")]
    pub(crate) relay: Option<Arc<super::relay::Relay>>,
    pub(super) session: Mutex<Session>,
}

impl Grants {
    fn now(&self) -> SystemTime {
        truncate((self.clock)())
    }

    fn lock(&self) -> MutexGuard<'_, Session> {
        self.session.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// The current verdict, without prompting, auditing or consuming.
    pub fn check(&self, capability: &str, subject: &str) -> Result<Verdict, Error> {
        validate_key(capability, subject)?;
        let s = self.lock();
        self.check_locked(&s, capability, subject)
    }

    fn check_locked(&self, s: &Session, capability: &str, subject: &str) -> Result<Verdict, Error> {
        let persisted = self.store.grants()?;
        if capability == CAP_TELEMETRY {
            return self.telemetry_verdict(&persisted);
        }
        let now = self.now();
        let mut expired: Option<Grant> = None;
        let mut keys = vec![(capability.to_string(), subject.to_string())];
        if !subject.is_empty() {
            keys.push((capability.to_string(), String::new()));
        }
        for k in keys {
            let mut held: Vec<Grant> = s.mem.get(&k).cloned().into_iter().collect();
            if let Some(p) = persisted.iter().find(|p| p.key() == k) {
                if p.expired(now) {
                    expired.get_or_insert_with(|| p.clone());
                } else {
                    held.push(p.clone());
                }
            }
            if let Some(v) = deny_wins_verdict(held) {
                return Ok(v);
            }
        }
        Ok(match expired {
            Some(g) => Verdict {
                allowed: false,
                reason: Reason::Expired,
                grant: Some(g),
            },
            None => Verdict {
                allowed: false,
                reason: Reason::Unset,
                grant: None,
            },
        })
    }

    /// Runs the telemetry resolver over the record, so `DO_NOT_TRACK`
    /// and `KIT_TELEMETRY_MODE=off` stay a standing no.
    fn telemetry_verdict(&self, persisted: &[Grant]) -> Result<Verdict, Error> {
        let rec = match self.store.telemetry() {
            Some(r) => r?,
            None => persisted
                .iter()
                .rev()
                .find(|p| p.capability == CAP_TELEMETRY)
                .map(TelemetryDecision::from_grant)
                .unwrap_or_else(TelemetryDecision::unknown),
        };
        let found = rec.grant().is_some();
        let d = resolve(&self.env, &self.app_prefix, &rec);
        if !found && d.state == DENIED && d.decision_source == DecisionSource::Config {
            return Ok(Verdict {
                allowed: false,
                reason: Reason::Unset,
                grant: None,
            });
        }
        Ok(verdict_of(d.grant().expect("resolver always decides")))
    }

    /// Records a decision and audits it as granted or denied. A `None`
    /// `decided_at` is stamped with the clock. Until and always grants go
    /// to the store and drop any in-memory grant for the key; once and
    /// session allows and session denies stay in memory; a once deny is
    /// audited only.
    pub fn decide(&self, mut g: Grant) -> Result<(), Error> {
        if g.decided_at.is_none() {
            g.decided_at = Some(self.now());
        }
        g.validate()?;
        let mut s = self.lock();
        if g.duration.persisted() {
            self.store.put_grant(&g)?;
            s.mem.remove(&g.key());
        } else if !(g.duration == Duration::Once && g.decision == Effect::Deny) {
            s.mem.insert(g.key(), g.clone());
        }
        let kind = match g.decision {
            Effect::Allow => AuditKind::Granted,
            Effect::Deny => AuditKind::Denied,
        };
        self.record(&AuditEvent::from_grant(kind, &g, self.now()))
    }

    fn record(&self, e: &AuditEvent) -> Result<(), Error> {
        if let Some(a) = &self.audit {
            a.record(e).map_err(Error::Audit)?;
        }
        if let Some(d) = &self.delivery {
            d.notify();
        }
        Ok(())
    }

    /// Audits a refusal that was not a decision, once per (capability,
    /// subject, cause) per session. Best effort: its errors are ignored.
    fn refuse_locked(
        &self,
        s: &mut Session,
        capability: &str,
        subject: &str,
        cause: RefusalCause,
        gov: Option<&Grant>,
    ) {
        if !s
            .refused
            .insert((capability.to_string(), subject.to_string(), cause))
        {
            return;
        }
        let source = gov
            .map(|g| g.decision_source.clone())
            .filter(|src| !src.as_str().is_empty())
            .unwrap_or(DecisionSource::Config);
        let mut e = AuditEvent::new(AuditKind::Refused, self.now(), capability, subject);
        e.decision_source = Some(source);
        e.reason = Some(cause);
        if cause == RefusalCause::AuditFailed {
            if let Some(d) = &self.delivery {
                d.publish_unrecorded(&e);
            }
            return;
        }
        let _ = self.record(&e);
    }

    /// An allow verdict, asking `prompter` when nothing stands. A standing
    /// deny is refused without asking; no prompter, a prompter error or
    /// an invalid answer is refused; an answer of deny is `Declined`.
    /// Store and audit failures come back as they are, never as refusals.
    pub fn request(
        &self,
        capability: &str,
        subject: &str,
        prompter: Option<&dyn Prompter>,
    ) -> Result<Verdict, Error> {
        validate_key(capability, subject)?;
        let v = {
            let mut s = self.lock();
            let v = match self.check_locked(&s, capability, subject) {
                Ok(v) => v,
                Err(e) => {
                    self.refuse_locked(
                        &mut s,
                        capability,
                        subject,
                        RefusalCause::StoreUnreadable,
                        None,
                    );
                    return Err(e);
                }
            };
            if v.allowed {
                return Ok(v);
            }
            if v.reason == Reason::Denied || prompter.is_none() {
                self.refuse_locked(&mut s, capability, subject, cause_of(&v), v.grant.as_ref());
                return Err(refused(capability, subject, v.reason, None));
            }
            v
        };
        let refuse = |why: String| {
            let mut s = self.lock();
            self.refuse_locked(
                &mut s,
                capability,
                subject,
                RefusalCause::PrompterError,
                v.grant.as_ref(),
            );
            refused(capability, subject, v.reason, Some(why))
        };
        let prompt = Prompt {
            capability: capability.to_string(),
            subject: subject.to_string(),
            prompt_version: self.prompt_version,
            reason: v.reason,
        };
        let answer = match prompter.expect("checked above").ask(&prompt) {
            Ok(a) => a,
            Err(e) => return Err(refuse(e)),
        };
        let g = Grant {
            decided_at: Some(self.now()),
            expires_at: answer.expires_at,
            prompt_version: self.prompt_version,
            ..Grant::new(
                capability,
                subject,
                answer.decision,
                answer.duration,
                DecisionSource::Prompt,
            )
        };
        if let Err(e) = g.validate() {
            return Err(refuse(e.to_string()));
        }
        self.decide(g.clone())?;
        if g.decision == Effect::Deny {
            return Err(refused(capability, subject, Reason::Declined, None));
        }
        Ok(verdict_of(g))
    }

    /// The gate before every tap: re-checks (a revoke since `request`
    /// wins), records a `used` event, then consumes a once grant. Never
    /// prompts. Anything but an allow is a refusal; an audit failure
    /// refuses the use with that error and keeps a once grant.
    pub fn use_grant(&self, capability: &str, subject: &str) -> Result<Grant, Error> {
        validate_key(capability, subject)?;
        let mut s = self.lock();
        let v = match self.check_locked(&s, capability, subject) {
            Ok(v) => v,
            Err(e) => {
                self.refuse_locked(
                    &mut s,
                    capability,
                    subject,
                    RefusalCause::StoreUnreadable,
                    None,
                );
                return Err(e);
            }
        };
        let Some(g) = v.grant.as_ref().filter(|_| v.allowed) else {
            self.refuse_locked(&mut s, capability, subject, cause_of(&v), v.grant.as_ref());
            return Err(refused(capability, subject, v.reason, None));
        };
        if let Err(e) = self.record(&AuditEvent::from_grant(AuditKind::Used, g, self.now())) {
            self.refuse_locked(
                &mut s,
                capability,
                subject,
                RefusalCause::AuditFailed,
                Some(g),
            );
            return Err(Error::Audit(format!(
                "consent: audit use of {capability}: {e}"
            )));
        }
        if g.duration == Duration::Once {
            s.mem.remove(&g.key());
        }
        Ok(g.clone())
    }

    /// Removes the grant for the exact key, in memory and in the store,
    /// and audits it when something was removed. `source` says who
    /// revoked (prompt for a user action, flag for a CLI verb, ...).
    pub fn revoke(
        &self,
        capability: &str,
        subject: &str,
        source: DecisionSource,
    ) -> Result<bool, Error> {
        validate_key(capability, subject)?;
        source.validate()?;
        let mut s = self.lock();
        let in_mem = s
            .mem
            .remove(&(capability.to_string(), subject.to_string()))
            .is_some();
        let removed = self.store.delete_grant(capability, subject)?;
        if !removed && !in_mem {
            return Ok(false);
        }
        let mut e = AuditEvent::new(AuditKind::Revoked, self.now(), capability, subject);
        e.decision_source = Some(source);
        self.record(&e)?;
        Ok(true)
    }

    /// Every grant this session sees: the stored ones with the in-memory
    /// ones laid over them, sorted. Expired grants are included.
    pub fn list(&self) -> Result<Vec<Grant>, Error> {
        let s = self.lock();
        let mut out: Vec<Grant> = self
            .store
            .grants()?
            .into_iter()
            .filter(|p| !s.mem.contains_key(&p.key()))
            .collect();
        out.extend(s.mem.values().cloned());
        sort_grants(&mut out);
        Ok(out)
    }

    /// Drops every once and session grant and resets the refusal
    /// debounce.
    pub fn end_session(&self) {
        *self.lock() = Session::default();
    }
}
