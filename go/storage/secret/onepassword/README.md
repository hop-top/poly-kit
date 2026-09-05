# onepassword

## What it answers

1Password items in one vault, by title, password field only. Two modes:
CLI shells out to `op`; Connect speaks to a 1Password Connect server over
HTTP with a bearer token. Wrong package for a self-hosted KV
(`go/storage/secret/openbao`, `go/storage/secret/infisical`).

## Use it when

- CLI: `secret.Config{Backend: "onepassword", Vault: "Engineering"}` after a blank import of this package; `op` must be signed in
- Connect: add `ConnectURL` and `Token`; `ConnectURL` set selects Connect mode
- `Vault` is required in both modes

## Contract

- Registered as `"onepassword"`. Open makes no network call; an unreachable server or missing `op` fails on first use.
- CLI mode runs `op item get --vault V --fields password --format json` and `op item list`; Connect mode uses `/v1/vaults/{vault}/items`.
- `Metadata` never requests fields containing values.
- Tests: `httptest` server for Connect; a fake `op` script placed first on `PATH` for CLI. No credentials, no cassette.

## Neighbours

- `hop.top/kit/go/storage/secret/keyring`: the local keychain when 1Password is not the source of truth.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
