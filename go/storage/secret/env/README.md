# env

## What it answers

Secrets the process already has in its environment, read-only. Key
`db/password` with prefix `APP_` reads `APP_DB_PASSWORD` (slashes to
underscores, upper-cased). Wrong package when the value must be written
(`go/storage/secret/file`, `go/storage/secret/keyring`).

## Use it when

- `secret.Config{Backend: "env", Prefix: "APP_"}` after a blank import of this package; `Prefix` defaults to empty
- CI or a container exports the value; as the `RO` fallback in a `composite`

## Quick start

```go
os.Setenv("APP_DB_PASSWORD", "hunter2")
defer os.Unsetenv("APP_DB_PASSWORD")

store := env.New("APP_")
s, err := store.Get(context.Background(), "db/password")
if err != nil {
	panic(err)
}
fmt.Println(string(s.Value))
// Output: hunter2
```

## Contract

- Registered as `"env"`; `Open` wraps `*Store` so `Set` and `Delete` return `ErrNotSupported`.
- `List` filters `os.Environ()` by store prefix plus the given prefix and strips the store prefix.
- `Metadata` always returns `ErrNotSupported`: a variable name carries no expiry or provenance. Tests: embedded, `t.Setenv` only.

## Neighbours

- `hop.top/kit/go/storage/secret/ghsecrets`: reads env too, as the fallback for write-only GitHub secrets.
- `hop.top/kit/go/storage/secret/composite`: where env becomes a fallback behind a writable store.
