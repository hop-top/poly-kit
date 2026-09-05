# infisical

## What it answers

Infisical secrets for one project and environment over the
`/api/v3/secrets/raw` REST API with a bearer token. Wrong package for KV
v2 servers (`go/storage/secret/openbao`).

## Use it when

- `secret.Config{Backend: "infisical", Addr: "https://app.infisical.com", Token: "...", Project: "<workspaceId>", Env: "dev"}` after a blank import of this package; all four fields are required
- staging and production differ by `Env`; `Metadata.Source` embeds project and env so callers can tell them apart

## Contract

- Registered as `"infisical"`. Open makes no call; failures surface on first use.
- Keys are path-escaped in URLs and JSON-encoded in bodies; binary-safe values are pinned by regression tests.
- `Metadata` strips the value from the payload the API returns alongside it.
- Tests: `httptest` server only; no credentials, no cassette. `SetClient` injects the HTTP client.

## Neighbours

- `hop.top/kit/go/storage/secret/openbao`: KV v2 alternative.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
