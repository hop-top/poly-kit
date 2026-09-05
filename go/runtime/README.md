# runtime

Execution logic for kit applications: events, entities, background work, guards, sync, and the seams that keep side effects honest.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`bus/`](bus/README.md) | in-process event bus, sinks, veto-able pre-events | you need to publish, subscribe to, or veto an event |
| [`domain/`](domain/README.md) | `Entity`, `Repository[T]`, `Service[T]`, `StateMachine` | you model a persisted entity or guarded transitions |
| [`job/`](job/README.md) | backend-agnostic job queue and its engine adapters | work must outlive the request or run on a worker |
| [`notify/`](notify/README.md) | severity, filter, retry decorators and outbound sinks | a bus event must reach a human (webhook, email, desktop) |
| [`peer/`](peer/README.md) | mesh networking and peer discovery | nodes must find each other |
| [`policy/`](policy/README.md) | declarative guard engine on the pre-event seams | an adopter must veto operations from YAML rules |
| [`provenance/`](provenance/README.md) | `Cached[T]` / `Synthesized[T]` wrappers and the render guard | structured output must say where a value came from |
| [`sideeffect/`](sideeffect/README.md) | `FS`, `HTTP`, `Bus`, `Exec` seams with real / dryrun / testfake impls | a command mutates state and must honour `--dry-run` |
| [`sync/`](sync/README.md) | state replication between nodes | entity state must converge across peers |
| [`telemetry/`](telemetry/README.md) | opt-in, redact-before-egress CLI telemetry | you ship usage data with consent |

## Conventions

- `domain` imports nothing else in this tree; every other package may import `domain` and `bus`.
- Vetoes travel as errors wrapping `domain.ErrConflict`; transports map that to HTTP 409 and CLI exit 4.
- Backend adapters live one level below the interface they implement (`job/<engine>`, `sideeffect/<impl>`, `notify/sinks/<sink>`).
