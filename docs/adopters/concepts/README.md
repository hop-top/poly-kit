# concepts

Mental models an adopter needs before reaching for a guide: what each subsystem is, when to run it, how it fits with the rest of a tool.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`bus-overview.md`](bus-overview.md) | in-process (optionally cross-machine) pub/sub hub, `go/runtime/bus` | you decide whether to publish or subscribe to events |
| [`engine-overview.md`](engine-overview.md) | `kit serve` sidecar exposing storage, sync, identity, peers and events over localhost | you decide whether to run the engine and how a tool connects to it |
| [`notifications-overview.md`](notifications-overview.md) | outbound sinks from bus events to webhooks, email, desktop, `go/runtime/notify` | you push events outward to humans |
| [`spaced-showcase.md`](spaced-showcase.md) | kit/cli features demonstrated on `spaced`, the reference CLI | you want to see the default help, flags and output before reading the API |
| [`storage-abstractions.md`](storage-abstractions.md) | the five storage layers and which access pattern each fits | you choose where to persist data inside a kit-based tool |
