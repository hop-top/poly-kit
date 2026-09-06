# secret

## What it answers

Where do credentials come from, and which backend holds them? `secret.Store`
is `Get`/`List`/`Exists`; `MutableStore` adds `Set`/`Delete`; `Keeper`
encrypts at rest; `MetadataReader` serves `auth status` without the value.
Wrong package for non-sensitive state (`go/storage/kv`) or for the Ed25519
identity that keys the local keeper (`go/core/identity`).

Backends (registered by blank import, resolved by `secret.Open`): `env`,
`file`, `agefile`, `keyring`, `onepassword`, `ghsecrets`, `openbao`,
`infisical`, `memory`.

## Use it when

| Backend | Config fields | Writes | Pick it when |
|---------|---------------|--------|--------------|
| [`agefile`](agefile/README.md) | `Path`, `IdentityFile` | no | one age-encrypted YAML shared with a team |
| [`env`](env/README.md) | `Prefix` | no | CI and containers export the value |
| [`file`](file/README.md) | `Dir` | yes | one file per key on disk; add a `Keeper` for encryption |
| [`ghsecrets`](ghsecrets/README.md) | `Repo` | write-only | pushing Actions secrets through `gh` |
| [`infisical`](infisical/README.md) | `Addr`, `Token`, `Project`, `Env` | yes | Infisical, cloud or self-hosted |
| [`keyring`](keyring/README.md) | `Service` (default `kit`) | yes | a developer laptop with an OS keychain |
| [`memory`](memory/README.md) | none | yes | tests |
| [`onepassword`](onepassword/README.md) | `Vault`, `ConnectURL`, `Token` | yes | the team already lives in 1Password |
| [`openbao`](openbao/README.md) | `Addr`, `Token`, `Mount` (default `secret`) | yes | OpenBao or Vault KV v2 |

Not backends: [`local`](local/README.md) is the NaCl `Keeper` for `file`;
[`composite`](composite/README.md) routes keys across several stores and
is assembled in code, since its predicates are Go funcs.

## Quick start

```go
store, err := secret.Open(secret.Config{Backend: "memory"})
if err != nil {
	panic(err)
}

ctx := context.Background()
_ = store.Set(ctx, "api-token", []byte("t0k3n"))
s, _ := store.Get(ctx, "api-token")
fmt.Println(s.Key, string(s.Value))
// Output: api-token t0k3n
```

Needs `_ "hop.top/kit/go/storage/secret/memory"` in the imports; see
[`example_test.go`](example_test.go).

## Contract

- `secret.Backends` is the list of names; `backends_test.go` asserts every entry resolves through `Open` and that the paragraph above matches it, so a backend cannot be documented without being registered.
- Missing key: `ErrNotFound`. Unsupported operation (read-only backend, no metadata): `ErrNotSupported`, wrapped with the backend name.
- `StoredMeta` never carries the value; `Source` is the only required field.
- `Mint` is the one sanctioned way to generate a bearer token into a store.
- Tests: `env`, `file`, `agefile`, `memory`, `local`, `composite` run embedded; `keyring` is `!ci` and skips without a keychain; `onepassword`, `infisical` use `httptest` and a fake CLI on `PATH`; `openbao` replays an xrr cassette; `ghsecrets` only tests the env fallback.

## Neighbours

- `hop.top/kit/go/core/identity`: keypair and `DeriveKey` behind `local.NewKeeper`.
- `hop.top/kit/go/storage/kv`: bytes that are not secrets.
- `hop.top/kit/go/console/output`: renders `StoredMeta` for `auth status`.

## See also

- [Secret management guide](../../../docs/adopters/guides/secret-management-guide.md)
- [Storage abstractions](../../../docs/adopters/concepts/storage-abstractions.md)
