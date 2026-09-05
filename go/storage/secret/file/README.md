# file

## What it answers

One file per key under a directory, `0600`, subdirectories per `/` in
the key, plaintext unless a `secret.Keeper` is supplied. Wrong package for
one shared encrypted document (`go/storage/secret/agefile`) or an OS
keychain (`go/storage/secret/keyring`).

## Use it when

- `secret.Config{Backend: "file", Dir: "~/.config/app/secrets"}` after a blank import of this package; `Dir` is required
- encryption at rest: `file.New(dir, local.NewKeeper(kp))` in code; `Config` cannot carry a live `Keeper`

## Quick start

```go
dir, _ := os.MkdirTemp("", "secret-file")
defer os.RemoveAll(dir)

store := file.New(dir, nil)
ctx := context.Background()
_ = store.Set(ctx, "db/password", []byte("hunter2"))
keys, _ := store.List(ctx, "db/")
fmt.Println(keys)
// Output: [db/password]
```

## Contract

- Registered as `"file"`; `Open` always builds a plaintext store.
- Keys that escape the root (`../`) are rejected before any I/O.
- `List` walks subdirectories and respects path boundaries: prefix `db` matches `db` and `db/...`, not `dbx`.
- `Metadata` sets only `UpdatedAt` (file mtime); scopes and expiry are not encoded. Tests: embedded, `t.TempDir` only.

## Neighbours

- `hop.top/kit/go/storage/secret/local`: the `Keeper` that encrypts these files.
- `hop.top/kit/go/storage/blob/local`: files that are not secrets.
