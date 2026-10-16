# admitted

## What it answers

"Who called this served command, on which surface, and under which
store key does its `--idempotency-key` replay?" without importing
`hop.top/kit/go/transport/cmdsurface`. Leaf package: the standard
library, pflag and a module-internal context key only.

## Use it when

- read the caller a transport established → `admitted.CallerFrom(ctx)`
- tell a verified caller from a transport-trusted one → `Caller.Established`, `EstablishedVerified`, `EstablishedTransport`
- record or look up a replay key → `admitted.ScopeIdempotencyKey(ctx, key)`
- name a surface without linking the bridge → `admitted.SurfaceREST`, `admitted.AllSurfaces()`
- re-parse a command tree between invocations → `CaptureFlag`, `RestoreFlag`, `ResetFlagToDefault`

## Quick start

```go
func run(cmd *cobra.Command, _ []string) error {
    if c, ok := admitted.CallerFrom(cmd.Context()); ok &&
        c.Established == admitted.EstablishedVerified {
        fmt.Fprintln(cmd.OutOrStdout(), "called by", c.Principal)
    }
    key := admitted.ScopeIdempotencyKey(cmd.Context(), "k1")
    _ = key // "k1" locally; "served:<hash>" inside a served run
    return nil
}
```

## Contract

- `CallerFrom` is false outside a served run. Only the cmdsurface
  bridge stamps the caller; a test guards that no other package
  imports the internal key.
- Principal, Tenant and Scopes are empty unless the caller is
  verified. The returned Scopes are a copy.
- `ScopeIdempotencyKey` returns the key as typed outside a served run.
  Inside one, or in a subprocess child holding `KIT_IDEMPOTENCY_SCOPE`,
  it returns `served:` plus the SHA-256 of scope and key, so two
  callers never share a replay. An empty key stays empty.
- `cmdsurface.Surface` and `cmdsurface.Establishment` are aliases of
  the types here.

## Neighbours

- `hop.top/kit/go/transport/cmdsurface`: the bridge that admits the
  invocation and writes the caller.
- `hop.top/kit/go/console/cli`: reads the caller in its `--policy` and
  `--idempotency-key` middleware.
- `hop.top/kit/go/console/cli/service`: the serve plane that runs the
  services.

## See also

- [serve-lifecycle.md](../../../../docs/contracts/serve-lifecycle.md)
