# How engine security works

Concept page. Explains the kit engine's identity, trust mesh, and
encryption model so you can reason about what is protected and
what isn't.

## Who this is for

Operators and tool authors deciding whether the engine's defaults
fit their threat model. Readers wiring up two engines for the
first time should also skim this before configuring trust.

> **Looking for step-by-step recipes?** Task pages are tracked as
> follow-ups: `docs/adopters/guides/trust-a-peer.md`, `docs/adopters/guides/encrypt-engine-data.md`.
> The illustrations below are inline only — not full task guides.

## Identity lifecycle

Each `kit serve` instance has its **own** Ed25519 keypair. A Go
app's identity and a TS engine's identity are equivalent — same
key format, same JWT signing, same trust model.

On first run, the engine auto-generates an Ed25519 keypair and
persists it in the data directory:

```
<data-dir>/
  identity/
    public.pem      # PKIX-encoded Ed25519 public key
    private.pem     # PKCS8-encoded Ed25519 private key
```

If keys exist, the engine loads them. No manual key generation.

**Fingerprint** = first 8 bytes of SHA-256(public key), hex (16
chars). Used as the engine's peer ID.

## Identity API

### GET /identity

```sh
curl http://localhost:9090/identity
```

```json
{
  "public_key": "-----BEGIN PUBLIC KEY-----\nMCow...\n-----END PUBLIC KEY-----",
  "fingerprint": "a1b2c3d4e5f67890"
}
```

### POST /identity/verify

Validates a JWT against known peer public keys.

```sh
curl -X POST http://localhost:9090/identity/verify \
  -H 'Content-Type: application/json' \
  -d '{"token":"eyJhbGciOiJFZERTQSIs..."}'
```

Valid:

```json
{"valid": true, "payload": {"sub": "peer-a", "scope": "sync"}, "signer": "a1b2c3d4e5f67890"}
```

Invalid:

```json
{"valid": false, "error": "unknown signer"}
```

Verification checks:

1. Signature valid against any known peer's public key.
2. Token not expired.
3. If `expected_fingerprint` provided: signer must match.

## Peer authentication

Peers authenticate via JWTs signed with their Ed25519 keypairs.
Sync endpoints validate incoming requests:

1. Peer presents JWT in `Authorization: Bearer <token>`.
2. Engine verifies signature against peer registry.
3. Only `trusted` peers accepted; `blocked` peers rejected (401).

No shared secrets. Each peer signs with its own private key;
receivers verify with the sender's stored public key.

## Trust flow

```
  ┌──────────┐                         ┌──────────┐
  │ Engine A │                         │ Engine B │
  └────┬─────┘                         └────┬─────┘
       │                                     │
       │  1. mDNS announce + browse          │
       │<────────── discover ────────────────>│
       │                                     │
       │  2. GET /identity                   │
       │────────────────────────────────────>│
       │<──── {public_key, fingerprint} ─────│
       │                                     │
       │  3. AcceptTOFU → PendingTOFU        │
       │  (stored locally, not yet trusted)  │
       │                                     │
       │  4. User approves:                  │
       │     POST /peers/:id/trust           │
       │  → TrustLevel = Trusted             │
       │                                     │
       │  5. Sync begins (both directions)   │
       │<═══════════ sync diffs ════════════>│
       │                                     │
```

### Trust levels

| Level        | Meaning                            |
|--------------|------------------------------------|
| unknown      | never seen                         |
| pending_tofu | discovered, awaiting user approval |
| trusted      | explicitly approved, sync allowed  |
| blocked      | rejected, all communication denied |

Promotion (illustration only — see [`trust-a-peer.md`](../guides/trust-a-peer.md) for the full how-to):

```sh
curl http://localhost:9090/peers
curl -X POST http://localhost:9090/peers/a1b2c3d4e5f67890/trust
curl -X POST http://localhost:9090/peers/a1b2c3d4e5f67890/block
```

## Encryption at rest

Enabled with the `--encrypt` flag on `kit serve`. The full
greenfield-or-migration how-to lives at
[`encrypt-engine-data.md`](../guides/encrypt-engine-data.md).

### How it works

1. Derive 32-byte symmetric key from Ed25519 private key via
   HKDF-SHA256 (domain: `kit-identity-encryption-v1`).
2. Encrypt each document with NaCl secretbox (XSalsa20-Poly1305).
3. Storage format: `nonce (24 bytes) || ciphertext`.
4. Decrypt on read using the same derived key.

Key derivation is deterministic — the same keypair always produces
the same encryption key. No separate key management.

### Properties

- Authenticated encryption (Poly1305 MAC).
- Random nonce per write (no nonce reuse).
- Tied to identity — moving data without the private key = useless.
- Transparent to API consumers (encrypt/decrypt in engine layer).

## Cross-language trust

A Go app and a TS engine trust each other identically:

```sh
# Go app at :8080, TS engine at :9090
curl http://localhost:8080/identity            # → fingerprint "go-fp-123"
curl -X POST http://localhost:9090/peers/go-fp-123/trust
curl -X POST http://localhost:8080/peers/ts-fp-456/trust
```

Both peers now sync freely. The wire protocol is identical
regardless of implementation language.

### Key format compatibility

| Aspect      | Go (`core/identity`) | Engine (`kit serve`) |
|-------------|----------------------|----------------------|
| Algorithm   | Ed25519              | Ed25519              |
| Public PEM  | PKIX                 | PKIX                 |
| Private PEM | PKCS8                | PKCS8                |
| JWT algo    | EdDSA                | EdDSA                |
| Fingerprint | SHA-256 first 8B     | SHA-256 first 8B     |

No conversion needed. Keys are byte-for-byte compatible.

## Threat model

### Protected

| Threat                | Mitigation                        |
|-----------------------|-----------------------------------|
| Data at rest exposure | NaCl secretbox encryption         |
| Peer impersonation    | Ed25519 signature verification    |
| Replay attacks        | JWT expiry + HLC timestamps       |
| Unauthorized sync     | trust registry (must be Trusted)  |
| Key compromise detect | pubkey mismatch = hard error      |

### NOT protected (by design)

| Scenario             | Assumption                              |
|----------------------|-----------------------------------------|
| Localhost transport  | trusted host; no TLS on loopback        |
| LAN eavesdropping    | mDNS is plaintext; use VPN if needed    |
| Physical access      | attacker with disk + private key wins   |
| DoS on engine port   | host-level firewall responsibility      |

### Recommendations

- Run engine on loopback only (default `127.0.0.1`).
- For remote peers over WAN: terminate TLS at a reverse proxy.
- Rotate keys by deleting `identity/` and restarting (requires
  re-establishing trust with all peers).
- Monitor `peer.discovered` events for unexpected peers.
- Block unknown peers promptly in production environments.

## Related pages

- [`engine-overview.md`](../concepts/engine-overview.md) — what the engine is, when to run it
- [`run-the-engine.md`](../guides/run-the-engine.md) — start the engine and connect a tool
- [`trust-a-peer.md`](../guides/trust-a-peer.md) — add a peer to the trust mesh
- [`encrypt-engine-data.md`](../guides/encrypt-engine-data.md) — turn on or migrate to encrypted storage
- [`engine-protocol.md`](engine-protocol.md) — HTTP/WS protocol spec
- [`engine-sync.md`](engine-sync.md) — sync internals
- [`cmd/kit/README.md`](../../../cmd/kit/README.md) — sidecar binary install + flag list
