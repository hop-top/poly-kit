# OAuth token source: design

Concept page. Records how kit turns a long-lived credential held in a
`secret` store into a short-lived OAuth 2 access token, and why each choice
was made over its alternative. Summary: [`go/storage/secret`](../../../go/storage/secret/README.md).

## Who this is for

Hosts that call a provider (Microsoft Graph, Gmail) with a token that
expires in about an hour, and authors of the code that mints it.

## Decisions

Each decision names the alternative that was rejected.

### Package: `go/storage/secret/oauth`

Chosen: a sub-package on top of `secret.Store` / `secret.MutableStore`.
It keeps `golang.org/x/oauth2` and the HTTP client out of the core
`secret` package, so a host that only reads secrets links neither.

Rejected: methods or files in `secret` itself. Every importer of the store
interface would inherit the dependency and the network code.

### Dependency: `golang.org/x/oauth2`, direct

Chosen: `clientcredentials` and `jwt` subpackages, pinned at `v0.37.0`
(already in `go.mod` as indirect; code that imports it promotes it to
direct via `go mod tidy`). Vetting facts are in the next section.

Rejected: hand-rolled token requests. The library already owns the form
encoding, auth-style detection, expiry parsing and the RS256 service
account assertion; rebuilding them is the workaround the "built-in over
hand-rolled" rule forbids. Also rejected: `oauth2.Config` with
`ReuseTokenSource` for caching, because its cache lives in memory only and
cannot write the refreshed token back to a store.

Probe results, `v0.37.0`, throwaway program against `httptest` servers:

- `clientcredentials.Config.EndpointParams` carries `client_assertion` and
  `client_assertion_type` into the token request body unchanged, beside
  `grant_type=client_credentials`, `scope` and `client_id`. Set
  `AuthStyle: oauth2.AuthStyleInParams`. With `AuthStyleAutoDetect` the
  library tries HTTP Basic first, which sends the id with an empty secret
  and omits `client_id` from the body.
- `jwt.Config.Subject` lands in the assertion claims as `sub`. The library
  also emits the legacy `prn` with the same value. Header is
  `{"alg":"RS256","typ":"JWT"}`; `aud` defaults to `TokenURL`, `iss` is
  `Email`. The grant type is `urn:ietf:params:oauth:grant-type:jwt-bearer`.
- Not provided: an `x5t` (certificate thumbprint) header. The Entra
  certificate flow therefore builds its own assertion in kit and passes it
  through `EndpointParams`; `jwt.Config` serves the Google flow only.

### API

```go
func TokenSource(ctx context.Context, store secret.Store, ref string, opts ...Option) (*Source, error)

func (s *Source) Token(ctx context.Context) (Token, error)
func (s *Source) Invalidate()
func Invalidate(ref string)

type Token struct {
    AccessToken string
    ExpiresAt   time.Time
}
```

Options: `WithHTTPClient`, `WithSkew` (default 5 minutes), `WithClock`,
`WithLockDir`, `WithWriteBackRetry`. `TokenSource` reads and validates the
grant and fails with `ErrNotWritable` for a `refresh_token` grant on a
store that cannot hold the rotated token. A token with the skew or less
left counts as expired; for a token minted in this process the skew is capped
at half its lifetime, so a token that lives no longer than the skew is
reused for the first half of its life instead of being minted on every call.
The cached envelope carries no issue time (uma rejects extra fields), so a
token minted by another process is judged by the full skew. `*Source` satisfies `cli.OAuthTokenSource`.

`Invalidate(ref)` drops the cached token so the next `Token` call refreshes
once. A host calls it after the provider answers 401. Package-level
`Invalidate(ref)` is the same operation, applied to every live `Source` in the
process for that ref, for hosts that do not hold the `Source`. Refresh is
single-flight per `Source`; `Sources` of one ref share the persisted token
and the file lock, so they do not refresh twice either.

Rejected: returning `*oauth2.Token`, which would make `x/oauth2` part of
every caller's API and of the contract ports must mirror. Also rejected:
a `Refresh(ctx, ref, rejected Token)` variant; `Invalidate` is simpler and
lets two callers that both saw 401 share one refresh.

### Envelope and mapping to uma

Chosen: the grant is a typed JSON envelope stored at `<ref>`, versioned
(`"v":1`), with `kind` one of `client_credentials`, `client_assertion`,
`jwt_bearer`, `refresh_token`, plus token URL, client id, scopes, subject
and references to key material in the same store. Key material is not
inlined: a reference points at an envelope in uma's format
(`hop-top/uma` `contrib/kit/secret`, kinds `secret`, `client_cert`):

| Grant kind | Key material | uma envelope the reference points at |
|------------|--------------|--------------------------------------|
| `client_credentials` | client secret | `secret` (`value`) |
| `client_assertion` | certificate and private key | `client_cert` (`certificate_pem`, `private_key_pem`) |
| `jwt_bearer` | service account key | `secret` (`value` = key JSON) |
| `refresh_token` | refresh token, rewritten on rotation | `secret` (`value`) |
| `refresh_token`, confidential client | optional client secret (`client_secret_ref`) | `secret` (`value`) |

`client_secret_ref` is allowed on `refresh_token` only. Public clients
(PKCE, client id only) leave it empty.

The cached access token is stored at `<ref>.token.<d>` as exactly a uma
`oauth2` envelope: `{"v":1,"kind":"oauth2","access_token":...,"expires_at":...}`
with optional `user` (from the grant subject) and `authzid`. uma decodes
strictly and rejects unknown fields, so the cached value carries nothing
else. The mapping to and from uma `oauth2` is therefore the identity on
`access_token` and `expires_at`, and a host that reads the token key
through uma needs no conversion. `<d>` is the first 16 hex characters of
SHA-256 over the canonical JSON of `client_cert_ref`, `client_id`,
`client_secret_ref`, `kind`, sorted `scopes`, `secret_ref`, `subject` and
`token_url` (alphabetical keys; absent strings `""`): editing a grant, or
re-pointing its key material, cannot serve a token minted for another
mailbox, scope or credential. Superseded keys are orphaned, not read.

The host owns the key layout of `<ref>` (heo keeps
`uma/<tenant>/<reference>`); kit appends suffixes only.

Rejected: one new kit-only envelope for everything, which forces a host to
keep two formats. Also rejected: inlining the cached token in the grant,
which would rewrite the long-lived credential on every refresh.

### Expiry lives in the cached value

`MutableStore.Set(ctx, key, []byte)` writes bytes only; `Secret.Metadata`
and `StoredMeta` are read-side. Expiry is the `expires_at` field inside the
cached envelope. Any `Metadata` a backend reports is derived, never relied on.

Rejected: writing `Metadata.ExpiresAt`. No backend can accept it through
`Set`.

### Write-back per backend

From the backend code. Rotating grants (`refresh_token`) require a
writable backend; the rest only cache.

| Backend | `Set` today | Rotating grant | Cached token persisted |
|---------|-------------|----------------|------------------------|
| `keyring` | overwrites the item | allowed | yes |
| `file` | `os.WriteFile`, becomes temp-then-rename | allowed | yes |
| `openbao` | KV v2 `Put`, new version; `Get` reads latest | allowed | yes |
| `composite` | the first writable member owning the key; `ErrNoWriter` otherwise | per owning member | per owning member |
| `memory` | process-local | refused (lost on exit) | memory only |
| `onepassword` connect | POSTs a new item on every `Set`; `Get` reads the first match | refused until `Set` upserts | memory only |
| `onepassword` CLI | `ErrNotSupported` | refused | memory only |
| `infisical` | POST only; overwrite of an existing key not exercised by its tests | refused until proven | memory only |
| `agefile` | `ErrNotSupported` | refused | memory only |
| `env` | no `Set`: not a `MutableStore` | refused | memory only |
| `ghsecrets` | `gh secret set`, but `Get` reads only the environment: write-only | refused | memory only |

"Refused" means configuration fails with a typed `not writable` error
naming the backend; a rotated refresh token is never dropped silently.
Writability is declared by the backend through an optional method, absent
meaning not writable. Rejected: probing with a test write, which has side
effects and on 1Password connect leaves a duplicate item.

### Cross-process safety: advisory lock, not CAS

Chosen: an advisory lock per reference (`flock(2)` on unix, `LockFileEx` on
Windows through `golang.org/x/sys`, already a direct dependency; not
`gofrs/flock`, which `go/console/cli` must not link) held around read, refresh and write-back, with a
re-read after the lock is taken so the second process uses the first
one's write. The lock file lives under the user cache directory (`hop-kit-oauth/`), keyed by
the SHA-256 of `<ref>`. The `file` backend's `Set` becomes
write-temp-then-rename so a crash cannot truncate the grant.

Limit: the lock is per machine. One rotating grant shared by several hosts
through `openbao` or another remote store is unsupported and documented as
such; non-rotating grants tolerate the race (both mint a valid token).

Rejected: compare-and-swap. `MutableStore` has no such primitive, and
adding one to every backend (keyring has none) is a larger change than the
problem needs.

### Language-neutral formats

The stored formats are not Go-shaped: the grant envelope, the cached token
(the uma `oauth2` envelope) and the error taxonomy (credential rejected,
needs re-consent, not writable, transient) are JSON and names only. They
are pinned in [`contracts/oauth-token-v1`](../../../contracts/oauth-token-v1/README.md)
(JSON Schema, a corpus of valid and invalid documents, mapping fixtures).
Go is the reference and its conformance test reads that corpus. The
TypeScript, Python, Rust and PHP ports follow once each has a secret store.

## Examples

Each example is runnable: [`example_*_test.go`](../../../go/storage/secret/oauth/)
in the package, against a loopback token server and the `file` store.
Every grant is stored at a ref (`app/graph`); key material sits at the refs
the grant names. Shared setup:

```go
store := file.New(dir, nil)
raw, _ := oauth.EncodeGrant(grant)
_ = store.Set(ctx, "app/graph", raw)
src, err := oauth.TokenSource(ctx, store, "app/graph", oauth.WithLockDir(lockDir))
tok, err := src.Token(ctx) // tok.AccessToken, tok.ExpiresAt
```

Only the grant and its key material differ.

### Client credentials (secret)

```go
grant := oauth.Grant{
    Kind: oauth.ClientCredentials, TokenURL: tokenURL, ClientID: "my-app",
    Scopes: []string{"api.read"}, SecretRef: "app/graph-secret",
}
// app/graph-secret: {"v":1,"kind":"secret","value":"<client secret>"}
```

### Client assertion (certificate)

The token request carries a signed assertion with an `x5t` header; no
secret is sent.

```go
grant := oauth.Grant{
    Kind: oauth.ClientAssertion, TokenURL: tokenURL, ClientID: "my-app",
    Scopes: []string{"api.read"}, ClientCertRef: "app/cert",
}
// app/cert: {"v":1,"kind":"client_cert","certificate_pem":"...","private_key_pem":"..."}
```

### JWT bearer (service account)

```go
grant := oauth.Grant{
    Kind: oauth.JWTBearer, TokenURL: tokenURL, Subject: "user@example.com",
    Scopes: []string{"mail.read"}, SecretRef: "app/sa-key",
}
// app/sa-key: {"v":1,"kind":"secret","value":"<service account key JSON>"}
```

### Refresh token with rotation

`NewRefreshGrant` validates; `EncodeRefreshSecret` builds the envelope for
the first refresh token. The store must be writable (see below).

```go
grant, err := oauth.NewRefreshGrant(tokenURL, "my-app", "app/rt", "", []string{"mail.read"})
rt, _ := oauth.EncodeRefreshSecret(refreshToken)
_ = store.Set(ctx, "app/rt", rt)
```

Each exchange that returns a new refresh token is written back to
`app/rt` before the next exchange.

### Getting the first refresh token: PKCE consent

`cmdsurface.LoopbackPKCE` runs one interactive consent for a CLI (a
loopback listener, S256 challenge, one pre-issued state);
`cmdsurface.MountOAuthPKCE` serves the same flow from an HTTP router for a
service. Both return a `PKCEResult`: the validated refresh-token `Grant`,
the `RefreshSecret` envelope and the first access token. Persist the grant
at the ref and the secret at `Grant.SecretRef`:

```go
res, err := cmdsurface.LoopbackPKCE(ctx, provider, cmdsurface.LoopbackOptions{
    OnReady: func(authorizeURL string) { openBrowser(authorizeURL) },
})
raw, _ := oauth.EncodeGrant(res.Grant)
_ = store.Set(ctx, ref, raw)
_ = store.Set(ctx, res.Grant.SecretRef, res.RefreshSecret)
```

`MountOAuthPKCE` binds each state to the browser that hit `/authorize`: an
HttpOnly, SameSite=Lax cookie holds a nonce whose hash is stored with the
state (`PKCEStateStore.Issue` and `Consume` take that binding). The callback
requires it, so an attacker-minted callback URL fails in the victim's browser
(400, state kept for the genuine one). The cookie is `__Host-` prefixed and
Secure when the request arrived over TLS; behind a TLS-terminating proxy set
`PKCEProvider.SecureCookie` to force that. `InMemoryPKCEStateStore` prunes
expired states and refuses new ones (503 from `/authorize`) past 10000
pending. Token endpoint 5xx, 429, 408 and
network failures return `ErrTransient` (502 to the browser); TLS verification
failures do not.

The runnable version fakes the browser step. `cli.NewOAuthIntrospector(src,
identity, scopes)` reports a `Source` through `auth status`.

## Write-back per backend (verified)

A backend is writable when it declares `Writable() bool`; only `keyring`,
`file` and `openbao` do. Writable backends persist the cached token and
accept rotating grants; all others keep the cached token in memory and
refuse a rotating grant with `ErrNotWritable`.

| Backend | Writable | Notes |
|---------|----------|-------|
| `keyring` | yes | |
| `file` | yes | atomic write (temp file, fsync, rename, directory fsync); a symlinked secret path keeps the link and updates its target |
| `openbao` | yes | KV v2, `Get` reads latest |
| `composite` | only if every non-read-only member is writable | no non-read-only member: not writable |
| `memory` | no | lost on exit |
| `agefile` | no | read-only |
| `ghsecrets` | no | write-only |
| `onepassword` (connect and CLI) | no | |
| `infisical` | no | |
| `env` | no | no `Store` write path (not a `MutableStore`) |

## Errors

Match with `errors.Is`. Messages carry a status and an allowlisted OAuth
error code, never a credential value.

| Class | Meaning | Host action |
|-------|---------|-------------|
| `ErrCredentialRejected` | endpoint refused the credential (bad secret, revoked key, invalid assertion, other 4xx) | fix or replace the credential; retrying cannot help |
| `ErrNeedsReconsent` | refresh token expired or revoked, or the provider wants the user back (`invalid_grant`, `interaction_required`, `consent_required`, `login_required` on a `refresh_token` grant; other kinds map these to `ErrCredentialRejected`) | run consent again |
| `ErrNotWritable` | rotating grant on a non-writable backend (`*NotWritableError` names it) | use `keyring`, `file`, `openbao` or a writable composite |
| `ErrTransient` | network, timeout, cancellation, 5xx, 429, 408, store read failure | retry with backoff |
| none (plain error) | 3xx reply (redirects are not followed), TLS certificate verification failure (untrusted authority, bad or expired certificate, hostname mismatch), 2xx reply missing `access_token` or `expires_in` | fix the trust or URL configuration, or treat as provider fault |

A malformed grant or missing referenced secret is a `*ValidationError`
naming the field.

### On "needs re-consent"

The stored refresh token is dead; no retry recovers it. Run the PKCE
consent again (`LoopbackPKCE` in a CLI, the mounted `/oauth/<name>/authorize`
route in a service), store the new `Grant` and `RefreshSecret` as above,
then create a new `Source` with `TokenSource`. Do not reuse the old
`Source`: it may still hold a pending rotated token from before.

## Operating notes

- Hold one `Source` per ref per process. Single-flight is per `Source`; on
  non-writable stores `Source`s do not share a cache, so two of them mint
  twice.
- `Invalidate` also deletes the persisted token. If the store refuses the
  delete, the `Source` remembers the dropped token in memory and ignores a
  persisted copy equal to it (other processes and new `Source`s still see it
  until it expires or is overwritten). A refresh in flight during `Invalidate`
  neither caches nor persists its token.
- `Invalidate` takes no token. Several concurrent 401s can each invalidate
  and cause one extra refresh.
- Package `Invalidate(ref)` reaches only the `Source`s of this process.
- Rotation write-back is bounded (`WithWriteBackRetry`, default 4 attempts,
  100 ms doubling). If it exhausts retries, `Token` still returns the access
  token; the rotated refresh token stays pending in memory, shared by every
  `Source` of that ref in the process, and is flushed on the next lock acquisition and used in place of
  the stored one. A pending token is dropped once the store holds anything
  other than what it superseded (a fresh consent, another writer). It is
  shared only between `Source`s on the same store value, never across stores
  (tenants), and fails closed: if the stored refresh token cannot be read, only
  the `Source` that received the pending token may use it; others return the
  read error. Limit: a
  process that exits before that flush, or another process reading the store
  in that window, holds the old refresh token. On providers with reuse detection
  that can force re-consent.
- A 2xx reply that rotates the refresh token but lacks a usable
  `access_token` or `expires_in` still fails `Token` with the plain error, but
  the rotated refresh token is persisted (or held pending) first.
- Write-back ignores cancellation of the caller's `ctx` (bounded to 10
  seconds per attempt), so a cancel cannot lose a rotation.
- The lock directory must be owned by the current user and not writable by
  group or others (checked on unix); otherwise the lock is refused. A
  `refresh_token` grant then fails; non-rotating grants (client credentials,
  client assertion, JWT bearer) proceed without the cross-process lock, still
  single-flight per `Source`, and at worst two processes both mint.
- The advisory lock is per machine. A rotating grant shared across hosts
  through a remote store is unsupported.
- Token requests never follow redirects; a 3xx reply is an error.
- Formats are pinned in `contracts/oauth-token-v1`; TypeScript, Python, Rust
  and PHP ports follow.

## Vetting: `golang.org/x/oauth2` v0.37.0

| Check | Result |
|-------|--------|
| `rsx analyze golang/oauth2` | PASS, score 5.5/10, policy `balanced`; low activity and release-hygiene scores reflect a Go module versioned by tags |
| License | BSD-3-Clause |
| Advisories (OSV, Go, `0.37.0`) | none |
| Age | `v0.37.0` published 2026-08-25, over 80 days; `v0.38.0` (2026-11-06) is under 14 days and not taken |
| Pin | `v0.37.0` stays exact; first direct importer runs `go mod tidy` |
