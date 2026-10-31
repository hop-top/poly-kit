//! The filter decorator.

use crate::bus::{BusError, Context, Event, Sink};

use super::severity::{severity_of, Severity};

type Predicate = Box<dyn Fn(&Event) -> bool + Send + Sync>;

/// Wraps a [`Sink`], forwarding an event only when every configured
/// check allows it: Go's `FilterSink`. With no checks it passes
/// everything. A rejection is silent (`drain` returns `Ok`, no I/O);
/// an inner error propagates unchanged.
///
/// It composes with itself: a filter around a filter ANDs the two, in
/// either order. The checks run topic, severity, then predicate.
pub struct FilterSink {
    inner: Box<dyn Sink>,
    pattern: Option<String>,
    min_severity: Option<Severity>,
    predicate: Option<Predicate>,
}

impl FilterSink {
    /// Wraps `inner` with no checks: Go's `NewFilterSink`.
    pub fn new(inner: impl Sink + 'static) -> Self {
        FilterSink {
            inner: Box::new(inner),
            pattern: None,
            min_severity: None,
            predicate: None,
        }
    }

    /// Drops events whose topic does not match `pattern`: exact, `*` for
    /// one segment, `#` for the trailing segments, as
    /// [`Topic::matches`](crate::bus::Topic::matches). Go's
    /// `WithTopicPattern`; a later call replaces an earlier one.
    pub fn topic_pattern(mut self, pattern: impl Into<String>) -> Self {
        self.pattern = Some(pattern.into());
        self
    }

    /// Drops events whose [`severity_of`] is below `s`: Go's
    /// `WithMinSeverity`.
    pub fn min_severity(mut self, s: Severity) -> Self {
        self.min_severity = Some(s);
        self
    }

    /// Drops events for which `f` returns false, after the topic and
    /// severity checks pass: Go's `WithPredicate`.
    pub fn predicate(mut self, f: impl Fn(&Event) -> bool + Send + Sync + 'static) -> Self {
        self.predicate = Some(Box::new(f));
        self
    }
}

impl Sink for FilterSink {
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        if let Some(p) = &self.pattern {
            if !event.topic.matches(p) {
                return Ok(());
            }
        }
        if let Some(floor) = self.min_severity {
            if severity_of(event) < floor {
                return Ok(());
            }
        }
        if let Some(f) = &self.predicate {
            if !f(event) {
                return Ok(());
            }
        }
        self.inner.drain(ctx, event)
    }

    fn close(&self) -> Result<(), BusError> {
        self.inner.close()
    }
}
