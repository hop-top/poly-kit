# Encrypt engine data at rest

> Audience: end-users running the kit engine who want SQLite
> documents encrypted on disk. For the underlying crypto and
> threat model, see [engine-security.md](../reference/engine-security.md).

This page covers two flows:

1. **Greenfield** — turning encryption on for a new engine.
2. **Migration** — turning encryption on for an engine that
   already has cleartext data.

## Prerequisites

- `kit` binary installed (see
  [run-the-engine.md](run-the-engine.md)).
- An existing identity keypair, or willingness to let the engine
  generate one on first run.

## How it works (in one paragraph)

The engine derives a 32-byte symmetric key from its Ed25519
private key (HKDF-SHA256 with the domain string
`kit-identity-encryption-v1`). Each document is encrypted with
NaCl secretbox (XSalsa20-Poly1305) using that derived key and a
fresh random nonce per write. Storage layout is `nonce || ciphertext`
in the same SQLite column. Decryption is transparent to the
HTTP API — clients see plaintext JSON regardless. **The
encryption key is bound to the identity**: lose the private key
and the data is unrecoverable.

## 1. Greenfield: start an encrypted engine

```bash
kit serve --port 8080 --data ./engine-data --encrypt
```

The first run generates the identity keypair (if one doesn't
exist) and initialises the SQLite store with encryption on. From
that point forward every document write is encrypted; reads are
transparent.

Verify:

```bash
curl -X POST http://localhost:8080/documents \
  -H 'Content-Type: application/json' \
  -d '{"kind":"note","payload":{"title":"hello"}}'

# Then peek at the SQLite file directly — payload column should
# be opaque bytes, not JSON.
sqlite3 ./engine-data/documents.db 'SELECT payload FROM documents LIMIT 1;'
```

If the payload reads as JSON, encryption isn't on — re-check the
`--encrypt` flag and the engine startup log.

## 2. Migration: encrypt an existing engine's data

The `--encrypt` flag does not auto-rekey existing cleartext rows.
Toggling it on a populated data dir will produce a mix of
cleartext and ciphertext that the engine cannot decrypt.

The safe migration:

### a. Stop the running engine

```bash
kill $(cat ./engine-data/kit-engine.pid)
# or Ctrl-C the foreground kit serve
```

### b. Back up the data dir

```bash
cp -r ./engine-data ./engine-data.bak.$(date +%Y%m%d-%H%M%S)
```

This is **not optional**. If the rekey step fails partway, the
backup is your only path back to readable data.

### c. Export documents to plaintext JSON

With the engine *not* running, dump the document store via
`sqlite3` (or via any kit tool that reads documents). Each row
should yield a single JSON object.

### d. Move the old SQLite aside

```bash
mv ./engine-data/documents.db ./engine-data/documents.db.cleartext
```

Keep the identity keypair (`./engine-data/identity/`) in place —
the encrypted store will be derived from the same key, so peer
trust survives the migration.

### e. Start the engine with `--encrypt`

```bash
kit serve --port 8080 --data ./engine-data --encrypt
```

The engine creates a new, encrypted `documents.db`.

### f. Re-import the documents

POST each exported JSON object back via the engine's
`/documents` endpoint. Once the import completes, every row is
encrypted with the derived key.

### g. Confirm and remove the cleartext backup

After verifying that all expected documents are present and
readable through the API:

```bash
shred -u ./engine-data/documents.db.cleartext
# and only after you are sure everything is migrated:
rm -rf ./engine-data.bak.*
```

`shred` (or platform equivalent) overwrites the file before
unlinking. The cleartext copy is what an attacker would target
if they got disk access between the migration and this cleanup.

## Operational notes

- **Backups must encrypt.** A backup of the encrypted SQLite
  file alone does not protect against an attacker who also has
  the identity keypair. Back up the keypair separately, or use
  filesystem-level encryption for backup media.
- **Key rotation = identity rotation.** There is no separate
  "encryption key" to rotate. Rotating the identity rotates the
  derived encryption key, which in turn requires repeating the
  migration above.
- **Cross-engine sync still works.** Sync diffs travel encrypted
  in transit (the wire protocol is its own concern); peers store
  the synced documents under their own derived keys. Two peers
  with different identities each end up with their own encrypted
  copy of the same logical document.

## Troubleshooting

- **Engine refuses to start with `--encrypt`** — likely a mix of
  cleartext and ciphertext rows from a partial migration. Restore
  from backup and redo the migration cleanly.
- **`/documents` returns garbled bytes** — the engine is
  decrypting with a different key than the data was encrypted
  with. Check that `./engine-data/identity/` matches the
  identity in use when the data was first written.
- **Lost the private key** — data is unrecoverable. The
  derivation is one-way and there is no backdoor.

## Next

- Threat model: [engine-security.md](../reference/engine-security.md).
- Identity rotation procedure (touches encryption indirectly):
  see "Key rotation" in
  [engine-security.md](../reference/engine-security.md).
- Trust mesh: [trust-a-peer.md](trust-a-peer.md).
