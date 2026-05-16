# Secret Management Guide

## Overview

`secret/` provides a unified interface for reading and writing
application secrets (API keys, DB passwords, tokens) across
multiple backends. One import, swap providers via config.

Why it exists:

- Every secret provider has its own SDK, auth, error model
- Apps couple to a single provider; migration = rewrite
- Testing with real backends is slow, flaky, expensive
- kit's `secret.Store` interface eliminates all three problems

## Quick Start

```go
import "hop.top/kit/go/storage/secret/env"

store := env.New("MYAPP_")
s, err := store.Get(ctx, "db_password")
if err != nil { ... }
fmt.Println(string(s.Value))
```

Set `MYAPP_DB_PASSWORD=hunter2` in your shell; done.

## Backends

| Backend     | Package              | Use when                                                  |
|-------------|----------------------|-----------------------------------------------------------|
| env         | `secret/env`         | Local dev, CI, 12-factor apps                             |
| file        | `secret/file`        | Encrypted files, SOPS-style (NaCl secretbox via Keeper)   |
| agefile     | `secret/agefile`     | age-encrypted YAML; multi-recipient, hardware/SSH keys    |
| keyring     | `secret/keyring`     | Desktop apps, CLI tools                                   |
| onepassword | `secret/onepassword` | 1Password vaults via op CLI or Connect API                |
| ghsecrets   | `secret/ghsecrets`   | GitHub Actions repository secrets via gh CLI              |
| openbao     | `secret/openbao`     | Production, team secrets                                  |
| infisical   | `secret/infisical`   | Cloud-native, SaaS teams                                  |
| memory      | `secret/memory`      | Testing                                                   |
| composite   | `secret/composite`   | Routing keys across multiple backends (see below)         |

### env

Reads `PREFIX + UPPER(key)` from environment. Slashes become
underscores: key `db/password` reads `MYAPP_DB_PASSWORD`.

```go
store := env.New("MYAPP_")
```

### file

Reads/writes secrets as individual files under a directory.
Supports optional encryption via `Keeper`.

```go
store := file.New("/etc/myapp/secrets", nil)       // plaintext
store := file.New("/etc/myapp/secrets", keeper)    // encrypted
```

### agefile

[age](https://age-encryption.org)-encrypted YAML, single flat
`map[string]string` payload. Complements `file`+`Keeper`: age supports
multiple recipients (each team member can decrypt with their own
identity), SSH-key recipients, and hardware-backed keys (YubiKey via
plugins). Read-only — re-encrypt the file out-of-band to mutate.

```go
store := agefile.New("/etc/myapp/secrets.age", "~/.config/myapp/age.key")
```

`Set` and `Delete` return `ErrNotSupported`; edit the encrypted file
with your usual age tooling (`age -d secrets.age | $EDITOR | age -e -R recipients.txt > secrets.age`).

### keyring

OS keychain (macOS Keychain, Windows Credential Manager,
Linux Secret Service). Good for CLI tools storing user tokens.

```go
store := keyring.New("myapp")
```

Note: `List` returns `ErrNotSupported` (OS limitation).

### onepassword

1Password vault, either via the `op` CLI (read-only) or via 1Password
Connect (full read/write). Items are looked up by title; values are
read from the `password` field.

```go
// CLI mode (read + list only — Set/Delete return ErrNotSupported)
store := onepassword.NewCLI("Personal")

// Connect mode (full read/write)
store := onepassword.NewConnect("https://connect.example", token, "Production")
```

### ghsecrets

GitHub Actions repository secrets via the `gh` CLI. GitHub secrets
are write-only by design (the API never returns plaintext); `Get`
falls back to environment variables — useful inside workflow runs
where the secret is exported as an env var of the same name.

```go
store := ghsecrets.New("owner/repo")          // explicit repo
store := ghsecrets.New("")                    // current repo (gh auto-detects)
```

### openbao

HashiCorp Vault-compatible (OpenBao fork). Production use with
ACLs, audit, rotation.

```go
store := openbao.New("https://vault:8200", token, "secret")
```

### infisical

Cloud-hosted or self-hosted secret manager with REST API.

```go
store := infisical.New(baseURL, token, projectID, "production")
```

### memory

In-process map. Use in tests to avoid I/O or network.

```go
store := memory.New()
_ = store.Set(ctx, "api_key", []byte("test-value"))
```

## Composing Backends

A single `secret.Config` selects exactly one backend, but apps often
need different keys to live in different places — e.g. CI tokens in
an age-encrypted file, developer credentials in the OS keyring, and
runtime config in environment variables as a fallback.

`secret/composite` routes operations across a list of `Member`s. Each
Member wraps an opened backend and a predicate that decides which
keys it owns:

```go
import (
    "strings"

    "hop.top/kit/go/storage/secret"
    "hop.top/kit/go/storage/secret/composite"

    _ "hop.top/kit/go/storage/secret/agefile"
    _ "hop.top/kit/go/storage/secret/env"
    _ "hop.top/kit/go/storage/secret/keyring"
)

age, _ := secret.Open(secret.Config{
    Backend:      "agefile",
    Path:         "/etc/myapp/secrets.age",
    IdentityFile: "/etc/myapp/id.txt",
})
kr,  _ := secret.Open(secret.Config{Backend: "keyring", Service: "myapp"})
env, _ := secret.Open(secret.Config{Backend: "env", Prefix: "MYAPP_"})

store := composite.New(
    composite.Member{
        Name:  "age",
        Store: age,
        Owns:  composite.HasPrefix("ci/"),
    },
    composite.Member{
        Name:  "keyring",
        Store: kr,
        Owns:  composite.HasPrefix("dev/"),
    },
    composite.Member{
        Name:  "env",
        Store: env,
        Owns:  nil,  // catch-all
        RO:    true, // never written to
    },
)
```

Routing rules:

- **Reads** (`Get`, `Exists`, `Metadata`) — first Member whose `Owns`
  matches the key AND that has the key wins. If no owner has it, fall
  through to non-owning Members in declaration order. The `RO` flag
  does not skip a Member from reads.
- **Writes** (`Set`, `Delete`) — first non-`RO` Member whose `Owns`
  matches wins. No match → `composite.ErrNoWriter`. `Delete`
  additionally returns `ErrNotFound` when writable owners exist but
  none hold the key.
- **List** — sorted, deduped union across all Members.
- **Metadata** — follows the read path. The returned `Source` is
  rewritten to `composite/<member-name>` so callers can see which
  Member answered. Members that don't implement `MetadataReader` or
  return `ErrNotSupported` are skipped (non-fatal).

`secret.Mint` works unchanged: minting writes to whichever Member owns
the minted key.

### Predicate helpers

`composite` ships small helpers so most wirings don't need a custom
`func(string) bool`:

| Helper                       | Matches                                        |
|------------------------------|------------------------------------------------|
| `HasPrefix(p)`               | keys starting with `p`                         |
| `HasSuffix(s)`               | keys ending with `s`                           |
| `AnyOf(names...)`            | keys exactly equal to one of `names`           |
| `MatchRegexp(re)`            | keys matching a compiled `*regexp.Regexp`      |
| `Or(p1, p2, ...)`            | any predicate matches (nil = catch-all)        |
| `And(p1, p2, ...)`            | all predicates match (nil = catch-all)         |
| `Not(p)`                     | inversion of `p` (`Not(nil)` matches nothing)  |

A nil `Owns` predicate (or `Or(..., nil)`) is the conventional
catch-all — used for the env fallback above.

### When not to use composite

- **Mirroring** (write the same secret to two backends for redundancy)
  is not what `composite` does — it writes to exactly one owner. If
  you need fan-out, build that on top.
- **Cross-backend transactions** are not supported. Each operation
  hits one Member; failures surface from that Member directly.
- **Config-driven layouts**: composite is a code-level constructor
  only, because routing predicates are arbitrary Go funcs. Drive the
  list of Members from your own config loader.

## Encryption at Rest

The `file` adapter accepts a `Keeper` for transparent
encrypt-on-write / decrypt-on-read:

```go
import (
    "hop.top/kit/go/storage/secret/file"
    "hop.top/kit/go/storage/secret/local"
    "hop.top/kit/go/core/identity"
)

kp, _ := identity.LoadKeypair("~/.config/myapp/key")
keeper := local.NewKeeper(kp)
store := file.New("/etc/myapp/secrets", keeper)

// Writes NaCl secretbox-encrypted file
_ = store.Set(ctx, "db_password", []byte("hunter2"))

// Reads + decrypts transparently
s, _ := store.Get(ctx, "db_password")
```

Keeper interface (implement for KMS, age, etc.):

```go
type Keeper interface {
    Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
    Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}
```

## Configuration

`secret.Open(Config)` is the factory for config-driven backend
selection:

```go
import "hop.top/kit/go/storage/secret"

store, err := secret.Open(secret.Config{
    Backend: "env",
    Prefix:  "MYAPP_",
})
```

Config fields per backend:

| Field        | env | file | agefile | keyring | onepassword | ghsecrets | openbao | infisical |
|--------------|-----|------|---------|---------|-------------|-----------|---------|-----------|
| Prefix       | x   |      |         |         |             |           |         |           |
| Dir          |     | x    |         |         |             |           |         |           |
| Path         |     |      | x       |         |             |           |         |           |
| IdentityFile |     |      | x       |         |             |           |         |           |
| Service      |     |      |         | x       |             |           |         |           |
| Vault        |     |      |         |         | x           |           |         |           |
| ConnectURL   |     |      |         |         | x (Connect) |           |         |           |
| Token        |     |      |         |         | x (Connect) |           | x       | x         |
| Repo         |     |      |         |         |             | x         |         |           |
| Addr         |     |      |         |         |             |           | x       | x         |
| Mount        |     |      |         |         |             |           | x       |           |
| Project      |     |      |         |         |             |           |         | x         |
| Env          |     |      |         |         |             |           |         | x         |

Backends self-register via `secret.RegisterBackend`. Import the
adapter package for side-effect registration:

```go
import _ "hop.top/kit/go/storage/secret/env"  // registers "env" backend
```

## Testing

Use `memory` adapter for deterministic, fast tests:

```go
func TestMyService(t *testing.T) {
    store := memory.New()
    _ = store.Set(ctx, "api_key", []byte("fake-key"))

    svc := myservice.New(store)
    // ... assertions
}
```

For recorded integration tests, pair with xrr cassettes:

```go
func TestInfisicalIntegration(t *testing.T) {
    // Record mode: hits real Infisical, saves responses
    // Replay mode: serves saved responses, no network
    srv := xrr.Replay(t, "testdata/cassettes/infisical")
    store := infisical.New(srv.URL, "tok", "proj", "dev")

    s, err := store.Get(ctx, "db_password")
    assert.NoError(t, err)
    assert.Equal(t, "expected-value", string(s.Value))
}
```

## Cross-Language Access

TS/Python apps access secrets via kit's gRPC/HTTP serve layer:

```
GET /api/v1/secrets/{key}
Authorization: Bearer <service-token>
```

Response:

```json
{"key": "db_password", "value": "aHVudGVyMg=="}
```

Value is base64-encoded. Backend selection configured server-side;
clients are backend-agnostic.
