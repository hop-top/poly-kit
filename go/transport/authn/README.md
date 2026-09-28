# authn

## What it answers

How a kit HTTP listener verifies a bearer token itself instead of
asking the adopter's `api.AuthFunc`: a JWT signed by the tool's own
identity keypair, by a key in a JSON Web Key Set, or by an OpenID
Connect provider found through discovery. The result is an
`api.AuthFunc`, so the api router, the rpc interceptor and the mcp
HTTP gate take it unchanged.

## Use it when

- configure it, no code → `services.<svc>.auth.mode: jwt | jwks | oidc`
- trust the tool's own keypair → `NewJWT([]Key{IdentityKey(kp)}, opts)`
- trust key files → `ParsePublicKeyPEM(data)`, then `NewJWT`
- trust a JWKS URL → `NewJWKS(url, Remote{...}, opts)`
- trust an OpenID provider → `NewOIDC(issuer, Remote{...}, opts)`
- deny revoked tokens → `Options.Check`; the same check on API keys → `APIKeys.WithCheck`; on a verifier `auth.mode` chose → `cli.WithTokenCheck`
- issue and check API keys → `NewAPIKeys(kvStore, nil)`: `Create`, `List`, `Revoke`, `Verify`, `AuthFunc`
- plug into a router → `Verifier.AuthFunc()`; check a token string → `Verifier.Verify(ctx, raw)`

## Quick start

```go
v, err := authn.NewOIDC("https://login.example.com/", authn.Remote{},
    authn.Options{Audience: []string{"https://api.example.com"}})
if err != nil {
    return err
}
cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: v.AuthFunc()})
```

## Contract

- Accepted: a compact JWS under EdDSA, RS*, PS* or ES* (never `none`, never HMAC), signed by a trusted key, with `exp` and `sub`, inside `exp`/`nbf`/`iat` give or take `ClockSkew` (default `1m`), matching `Issuer` and `Audience` when set, and passing `Check`.
- Refusals wrap `ErrInvalidToken`; a request without a bearer token wraps `ErrNoToken`; a remote key set never fetched wraps `ErrKeySetUnavailable`.
- Claims: `api.Claims{Subject: sub, Tenant: <TenantClaim, default "tenant">, Scopes: api.ScopesOf(payload)}`.
- A fixed key set selects by `kid`, and tries every key when the `kid` matches none (an external signer's own naming): the signature decides.
- API keys are `kit_<id>_<secret>` (64-bit id, 256-bit secret, hex), read from `X-API-Key` else `Authorization: Bearer`; the store keeps a SHA-256 of the domain-separated id and secret, compared in constant time, and the principal, tenant, scopes, expiry and revocation.
- Audiences compare exactly except for a trailing slash (RFC 8707 resource indicators and minted `aud` values disagree on it).
- A remote key set is fetched on first use, again after `Refresh` (default `1h`), and on an unknown `kid` at most once per `MinRefresh` (default `1m`); a failed refetch keeps the last good set. URLs are `https`, or `http` to a loopback host. OIDC discovery must name the configured issuer exactly.

## Neighbours

- `go/transport/api`: `AuthFunc`, `Auth`, `Claims`, `ScopesOf`.
- `go/core/identity`: the Ed25519 keypair and `SignJWT` whose tokens `IdentityKey` verifies.
- `go/console/cli`: `ResolveServeTLS` / `ResolveServeVerifier`, which build a verifier from `services.<svc>.auth`.

## See also

- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md#bearer-tokens): the configuration keys, defaults and refusals
- [secure-remote-serving.md](../../../docs/adopters/guides/secure-remote-serving.md): exposing a tool beyond loopback
