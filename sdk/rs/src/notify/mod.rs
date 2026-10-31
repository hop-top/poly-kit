//! Notification primitives over the bus [`Sink`](crate::bus::Sink). Gated
//! behind the `notify` feature.
//!
//! Ports the Go canonical `hop.top/kit/go/runtime/notify`:
//!
//! - the [severity convention](Severity): [`severity_of`] reads an event's
//!   urgency;
//! - [`FilterSink`], a decorator that forwards only events passing a topic
//!   pattern, a severity floor and a predicate;
//! - [`RetrySink`], a decorator with at-least-once delivery: bounded
//!   attempts, [`exponential_backoff`], a dead-letter sink, and a breaker
//!   refusal as a terminal error;
//! - the [templates](Template) the outbound sinks render with.
//!
//! The outbound sinks sit behind their own features, native only:
//! `webhook` (`notify-webhook`), `email` (`notify-email`) and `osnotify`
//! (`notify-os`). Each runs the pipeline
//! `template render -> redactor -> breaker -> egress`; a redactor and a
//! breaker are opt-in.
//!
//! The core is wasm-safe. See the module README for composition and what
//! differs from Go.
//!
//! # Example
//!
//! ```
//! use std::time::Duration;
//! use hop_top_kit::bus::{BusError, Context, Event, Sink};
//! use hop_top_kit::notify::{FilterSink, RetrySink, Severity};
//! use serde_json::json;
//!
//! struct Stdout;
//! impl Sink for Stdout {
//!     fn drain(&self, _: &Context, e: &Event) -> Result<(), BusError> {
//!         println!("{}", e.topic);
//!         Ok(())
//!     }
//!     fn close(&self) -> Result<(), BusError> { Ok(()) }
//! }
//!
//! // Retry outermost, filter inside, the reference sink innermost.
//! let sink = RetrySink::new(
//!     FilterSink::new(Stdout)
//!         .topic_pattern("billing.#")
//!         .min_severity(Severity::Error),
//! )
//! .max_attempts(2)
//! .backoff(|_| Duration::ZERO);
//!
//! let e = Event::new("billing.finance.invoice.failed", "billing", json!({"severity": "critical"}));
//! sink.drain(&Context::background(), &e).unwrap();
//! ```

mod backoff;
mod filter;
mod retry;
mod severity;
mod template;

#[cfg(all(feature = "notify-email", not(target_arch = "wasm32")))]
pub mod email;
#[cfg(all(feature = "notify-os", not(target_arch = "wasm32")))]
pub mod osnotify;
#[cfg(all(feature = "notify-webhook", not(target_arch = "wasm32")))]
pub mod webhook;

pub use backoff::{exponential_backoff, BackoffFn};
pub use filter::FilterSink;
pub use retry::{is_broken_circuit, RetrySink, Sleeper, BROKEN_CIRCUIT};
pub use severity::{severity_of, Severity};
pub use template::{LiteralTemplate, Template, TemplateError, TextTemplate};
