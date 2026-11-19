# ref

## What it answers

A config or env value may name a secret instead of holding it. `ref.Resolve`
turns `op://<vault>/<item>/<field>[?account=<name>]` or `secret:NAME` into the
secret, and returns every other value unchanged. Wrong package for storing or
listing secrets (`go/storage/secret`).

## Use it when

- a config file, `.env` or profile carries a pointer to a vault instead of the value
- the caller already has a store (any `secret.Store`) and wants `secret:NAME` to read from it

## Quick start

```go
o := ref.Opener{
	Lookup: func(ctx context.Context, name string) (string, bool, error) {
		s, err := store.Get(ctx, name)
		if errors.Is(err, secret.ErrNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return string(s.Value), true, nil
	},
}
env, err := ref.ResolveMap(ctx, rawEnv, o) // error names the key, never the value
```

## Contract

- Literals pass through untouched; the vault is never consulted for them.
- Fail closed: any failure returns `""` (or a nil map) and an error wrapping `ErrUnresolved`. The literal reference and an empty value are never returned in its place.
- Errors name the reference kind and, from `ResolveMap`, the key. They never contain a resolved value. `op` diagnostics are passed through truncated.
- A reference that resolves to another reference is refused.
- `op://` is read with `op read --no-newline`; `?account=` becomes `--account`. `Opener.OpRead` replaces the CLI (tests, Connect).
- Each `op read` is bounded to 30 seconds.
- Tests: embedded; the `op` CLI is a stand-in script on `PATH`.

## Neighbours

- `hop.top/kit/go/storage/secret`: the `Store` behind `Opener.Lookup`.
- `hop.top/kit/go/storage/secret/onepassword`: title/field-based backend; `ref` reads a precise `op://` path instead.

## See also

- [`secret/README.md`](../README.md)
