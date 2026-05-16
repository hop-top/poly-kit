# Trust a peer

> Audience: end-users running the kit engine who want to add a
> remote engine to their trust mesh so the two can sync. For the
> threat model and trust-level semantics, see
> [engine-security.md](../reference/engine-security.md).

This page walks the TOFU (trust-on-first-use) flow from "two
engines on the network" to "engines syncing freely" in four steps.

## Prerequisites

- Two engines running and reachable from each other. Get them up
  with [run-the-engine.md](run-the-engine.md).
- The remote engine's URL (or both engines on the same network for
  mDNS discovery).
- `curl` or any HTTP client.

## 1. Discover the peer

If both engines are on the same network and mDNS is available, the
local engine sees the remote automatically. List discovered peers:

```bash
curl http://localhost:8080/peers
```

Each peer entry includes:

- `id` — the peer's identity fingerprint (first 8 bytes of
  SHA-256 over its public key).
- `trust_level` — one of `unknown`, `pending_tofu`, `trusted`,
  `blocked`.
- `address` — the peer's reachable URL.

A freshly discovered peer arrives at `pending_tofu`. It's seen, but
no sync is allowed yet.

If mDNS isn't an option, fetch the peer's identity directly:

```bash
curl http://<remote-host>:<remote-port>/identity
```

Response:

```json
{"public_key": "...", "fingerprint": "a1b2c3d4e5f67890"}
```

Verify the fingerprint out-of-band (chat, signal, in-person) before
proceeding. Anyone on the network can claim a fingerprint until you
verify it once.

## 2. Trust the peer

Promote the peer from `pending_tofu` to `trusted`:

```bash
curl -X POST http://localhost:8080/peers/a1b2c3d4e5f67890/trust
```

What happens:

- The local engine stores the peer's public key and marks it
  trusted.
- Future requests signed by this peer's private key now pass
  authentication on this engine.

Trust is one-way. The remote engine must trust *you* in the same
way before bidirectional sync works.

## 3. Have the remote trust you back

On the remote engine (or have its operator run it):

```bash
# Find your local engine's fingerprint
curl http://localhost:8080/identity
# Then on the remote, trust it:
curl -X POST http://<remote>:<port>/peers/<your-fingerprint>/trust
```

After this round-trip, both peers consider each other `trusted`.

## 4. Verify sync works

Sync begins automatically once both peers are mutually trusted. To
confirm:

```bash
curl http://localhost:8080/peers
```

The peer should now show `trust_level: trusted` and a recent
`last_seen` timestamp.

If you have a document store with content on one side, write a
document there and read it back from the other side after a few
seconds.

## Revoking trust

To stop syncing with a peer:

```bash
curl -X POST http://localhost:8080/peers/a1b2c3d4e5f67890/block
```

This sets `trust_level: blocked` — the peer is rejected at the
authentication layer (401). To re-trust later, repeat step 2.

## Cross-language peers

A Go app and a TypeScript engine trust each other through the
same flow — the wire protocol is identical regardless of the
implementing language. See
[engine-security.md](../reference/engine-security.md) ("Cross-language trust")
for a worked example.

## Troubleshooting

- **Peer doesn't appear in `/peers`** — mDNS may be filtered on
  your network. Fetch the peer's `/identity` directly and POST
  to `/peers/<fingerprint>/trust` with the address explicit.
- **`401` on sync** — likely one side hasn't trusted the other
  yet. Run step 3.
- **Fingerprint mismatch** — the public key the peer announced
  doesn't match the one stored locally. This is a real security
  signal, not a bug — investigate before proceeding.

## Next

- Threat model: [engine-security.md](../reference/engine-security.md).
- Sync model: [engine-sync.md](../reference/engine-sync.md).
- Wire format: [engine-protocol.md](../reference/engine-protocol.md).
- Encrypt the data the peer mesh syncs:
  [encrypt-engine-data.md](encrypt-engine-data.md).
