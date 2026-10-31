# webhook

## What it answers

How a tool receives a webhook safely: prove the sender, answer a subscription handshake, refuse a stale or repeated delivery, acknowledge fast, and hand the payload to the bus. Wrong place for outbound webhooks and for routing: mount the receiver on an `api.Router`, subscribe to the topic.

## Use it when

- Microsoft Graph change notifications → `webhook.New(name, webhook.Graph(cfg)...)`: validationToken echo, `clientState`, lifecycle notices on their own topic
- Pipedrive v2 → `webhook.Pipedrive(cfg)`: basic auth, `meta.id` as the key, `meta.timestamp` for the replay window
- an HMAC-signed sender → `webhook.GenericHMAC(cfg)`: any header, sha1/256/512, hex/base64, optional signed timestamp
- retries must not republish → `WithIdempotency(store, ttl, key)` over a `kv.TTLInserter`

## Quick start

```go
rc, err := webhook.New("graph",
    append(webhook.Graph(webhook.GraphConfig{ClientState: secret}),
        webhook.WithPublisher(bus),
        webhook.WithIdempotency(store, 0, nil))...)
router.Handle("POST", "/hooks/graph", rc.ServeHTTP)
```

## Contract

- A receiver needs a name, a publisher and a verifier; an open endpoint is refused at construction.
- Order: handshake, verifiers (all), replay window, idempotency claim, publish, answer. A handshake request carries no signature and is answered before verification; it is recognised by its own token only.
- Refusals are `{"status","code","message"}`: `webhook_unverified` and `webhook_stale` (401, never saying why), `body_too_large` (413), 405 for a non-POST. Signatures, `clientState` and credentials compare in constant time.
- The claim is one atomic `PutIfAbsentWithTTL`, under `webhook/<name>/<sha256 of the key>` (receiver names are `[a-z0-9_-]+`). A key taken from a header is never trusted: the claim is the body hash plus the signed timestamp, so a replay with a changed header repeats. A signed-timestamp header must be plain decimal Unix seconds, checked before the MAC. A repeat is `200 {"status":"duplicate"}` and not published. A failed publish releases the claim, so the sender's retry is processed. The default key is the SHA-256 of the body.
- The answer is `202` when the publish finishes or the ack budget (2.5s) runs out; the publish then continues detached.
- Published payload: `receiver`, `key`, `kind`, `received_at`, `content_type`, `encoding` (`json` with the body as a JSON value, else `base64`), `body`. Topics `kit.webhook.notification.received` and `kit.webhook.lifecycle.received`, source `webhook.receiver`.
- Go is canonical: [`contracts/webhook-v1`](../../../contracts/webhook-v1/README.md) is run by the Go and Rust receivers (`make test-parity-webhook`).

## Neighbours

- `go/transport/api`: the router it mounts on; `go/storage/kv`: the dedupe store; `go/runtime/bus`: the topics
- `sdk/rs/src/api_server/webhook`: the Rust port (feature `webhook`)
