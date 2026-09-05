# openbao

## What it answers

OpenBao or HashiCorp Vault KV v2 through the official `api` client: one
mount, optional key prefix, metadata from the `/metadata` endpoint. Wrong
package for Infisical (`go/storage/secret/infisical`) or 1Password
(`go/storage/secret/onepassword`).

## Use it when

- `secret.Config{Backend: "openbao", Addr: "http://127.0.0.1:8200", Token: "...", Mount: "secret"}` after a blank import of this package; `Addr` is required, `Mount` defaults to `secret`
- a prefix inside the mount: `openbao.NewWithClient(client, mount, prefix)` in code

## Contract

- Registered as `"openbao"`. Open validates the address and makes no round-trip; an unreachable server fails on first use.
- Values are stored under the `value` field of the KV v2 secret at `<prefix><key>`.
- `Metadata` surfaces custom metadata as `scope:<k>=<v>` entries and never returns the value.
- Tests: recorded with `hop.top/xrr` against an OpenBao container. Replay is the default and reads `testdata/cassettes`; set `XRR_MODE=record` (and Docker) to re-record, `XRR_CASSETTE_DIR` to relocate. Skipped under `-short` and in CI.

## Neighbours

- `hop.top/kit/go/storage/secret/infisical`: the other REST-backed vault.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
