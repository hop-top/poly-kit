// Package webhook receives inbound webhooks on an api router: verify
// the sender, answer a subscription handshake, refuse a stale or
// repeated delivery, acknowledge within the sender's patience, and
// publish the payload on the bus.
//
// A [Receiver] is an [http.Handler]; mount it with api.Router.Handle.
// Per request, in order:
//
//  1. a [Handshake] may answer the request (MS Graph's validationToken
//     echo); handshakes carry no signature and are tried first;
//  2. every [Verifier] must accept it ([HMAC], [ClientState],
//     [BasicAuth]); a refusal is 401 webhook_unverified and says
//     nothing about why;
//  3. the replay window, when set, refuses a delivery whose timestamp
//     is too far from now (401 webhook_stale);
//  4. the idempotency key is claimed in a kv store with an atomic
//     put-if-absent; a key already claimed is acknowledged 200 and not
//     published again;
//  5. the payload is published and the sender gets 202, within the ack
//     budget (2.5s by default) whether or not the publish has finished.
//
// The published payload is a map: receiver, key, kind, received_at,
// content_type, encoding ("json" with the body as a JSON value, else
// "base64" with the body as a string) and body. Topics follow the bus
// grammar: kit.webhook.notification.received, and
// kit.webhook.lifecycle.received for lifecycle notices.
//
// [Graph], [Pipedrive] and [GenericHMAC] build the receivers for the
// shared conformance fixtures in contracts/webhook-v1.
package webhook
