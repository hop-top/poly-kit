// Package job provides a backend-agnostic asynchronous job queue.
//
// The core types (Job, Service, Status) are engine-independent and
// import only the domain package. Adapters live in sub-packages
// (e.g. job/mock for testing).
//
// # Architecture
//
// Service is the primary interface. Implementations handle storage,
// atomicity, and state machine enforcement. The state machine rules
// allow the following transitions:
//
//   - pending → active, canceled
//   - active  → succeeded, failed, timeout, pending, canceled
//   - failed  → pending (retry)
//   - timeout → pending (retry)
//
// # Backoff
//
// BackoffStrategy computes exponential delays with jitter for retries.
// DefaultBackoff returns 30s initial, 15m max, 2x factor, 25% jitter.
//
// # Polling
//
// Poller provides a long-running poll loop that claims, routes, and
// completes jobs. RunOne offers single-shot opportunistic execution
// with cooldown guards.
//
// # Events
//
// Job lifecycle events are published via domain.EventPublisher using
// the Topic* constants (e.g. TopicCreated, TopicFailed).
package job
