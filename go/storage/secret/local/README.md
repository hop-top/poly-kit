# local

## What it answers

The `secret.Keeper` that encrypts values at rest with NaCl secretbox, keyed
from an `identity.Keypair` via `DeriveKey`. Not a backend and not in
`secret.Backends`: it plugs into `file.New(dir, keeper)`. Wrong package for
multi-recipient files (`go/storage/secret/agefile`).

## Use it when

- `file.New(dir, local.NewKeeper(kp))` where `kp` comes from `identity.Generate` or the loaded device identity

## Quick start

```go
dir, _ := os.MkdirTemp("", "secret-local")
defer os.RemoveAll(dir)

kp, err := identity.Generate()
if err != nil {
	panic(err)
}
store := file.New(dir, local.NewKeeper(kp))
ctx := context.Background()
_ = store.Set(ctx, "api-token", []byte("t0k3n"))
s, _ := store.Get(ctx, "api-token")
fmt.Println(string(s.Value))
// Output: t0k3n
```

## Contract

- `Encrypt`/`Decrypt` delegate to `identity.Encrypt`/`identity.Decrypt`; random nonce per call, so ciphertext differs across writes. A different keypair fails to decrypt; no rotation helper.
- Key derivation is pinned across ports by [`contracts/identity-v1/derive-key.json`](../../../../contracts/identity-v1/derive-key.json). Tests: embedded.

## Neighbours

- `hop.top/kit/go/core/identity`: keypair, `DeriveKey`, secretbox helpers. `hop.top/kit/go/storage/secret/file`: the store this keeper wraps.
