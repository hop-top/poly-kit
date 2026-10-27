# consent

## What it answers

May this subject use this capability now, did the user agree to telemetry, and why is the state what it is. This package persists per-capability grants and the telemetry decision, gates each use, and audits every grant, deny, revoke and use. What gets collected, and how telemetry leaves the process, is `go/runtime/telemetry`; OS permission prompts and capture code belong to the consumer.

## Use it when

- you gate a sensitive tap (clipboard, screen, files fed to a prompt): `OpenGrants()`, then `Request(cap, subject, prompter)` once and `Use(cap, subject)` before every tap
- you record or withdraw a decision from your own UI: `Decide`, `Revoke`; read back with `Check`, `List`
- you need bus events or your own audit sink: `WithAuditor(BusAuditor(pub, DefaultTopics))`, `MultiAuditor`, `FileAuditor`
- wiring telemetry at bootstrap: `Install()` builds the `FileStore` and installs the hook into kit-telemetry
- you have your own `Store`: `NewHook(store)` then `telemetry.SetConsentHook`
- you must decide telemetry from flags, env and the persisted record: `Resolve` / `ResolveWithDiagnostics` with `Inputs`
- you need the `DO_NOT_TRACK` check on its own: `DoNotTrackEnabled(env)`

## Quick start

```go
g := consent.NewGrants(&consent.MemoryGrantStore{}) // OpenGrants() in production
ask := consent.PrompterFunc(func(context.Context, consent.Prompt) (consent.Answer, error) {
    return consent.Answer{Decision: consent.Allow, Duration: consent.Session}, nil
})
_, err := g.Request(ctx, "io.clipboard.read", "app:mytool", ask)
fmt.Println(err)
_, err = g.Use(ctx, "io.clipboard.read", "app:mytool") // before every tap
fmt.Println(err)
_, err = g.Use(ctx, "io.screen.capture", "app:mytool")
fmt.Println(consent.KindOf(err), errors.Is(err, consent.ErrRefused))
// Output:
// <nil>
// <nil>
//  true
```

## Contract

- Grants: capability (`telemetry`, or dotted `io.screen.capture` style) + subject (empty = any) + `allow|deny` + `once|session|until|always`. `until` and `always` persist to `<XDG_CONFIG_HOME>/kit/config.yaml` partition `kit.consent`; `once` and `session` live in the `Grants` value only.
- Fail closed: no grant and no prompter, a prompter error, an unreadable store, or an audit failure on `Use` all refuse. A standing deny is never prompted.
- Refusals are `*RefusedError` (`errors.Is(err, ErrRefused)`): `denied` is `UNAUTHORIZED`/5 permanent; `unset`, `expired`, `declined` are `CONSENT_REFUSED`/7 transient.
- Audit: JSON lines at `<XDG_STATE_HOME>/kit/consent/audit.jsonl` by default; bus topics `kit.core.consent.{granted,denied,revoked,used}`.
- Telemetry is the `telemetry` capability over the unchanged `kit.telemetry.consent` record (fields `state`, `decided_at`, `prompt_version`, `decision_source`); legacy `kit/telemetry.yaml` is read as a fallback. `Clear` (and revoking telemetry) removes the record from both files, other keys kept.
- `Store.Set` writes a `*.tmp` sibling then renames, perms 0600, and preserves every other key. `Get` on a missing record returns `StateUnknown`; malformed YAML is an error.
- Telemetry precedence, highest first: `<APP>_TELEMETRY_MODE=off` or `KIT_TELEMETRY_MODE=off`; `DO_NOT_TRACK`; `--telemetry=on|off`; `KIT_TELEMETRY_CONSENT=granted|denied`; persisted decision; default denied with `SourceConfig`. `Grants.Check` applies the same chain.
- Cross-language: [contracts/consent-v1](../../../contracts/consent-v1/README.md), `make test-parity-consent`.
- `NewHook(nil)` and `NewGrants(nil)` panic: a nil store is a wiring bug.

## Neighbours

- `go/runtime/telemetry`: `ConsentHook` interface, `Mode` (off / anon / full), emitter and sink.
- `go/console/output/envelope`: exit codes refusals render to.
- `go/runtime/bus`, `go/runtime/domain`: topics and the publisher `BusAuditor` uses.
- `go/core/xdg`: resolves the config and audit paths.
- `go/core/compliance`: F13 `ConsentingTelemetry` check.
- `cmd/kit`: `kit telemetry enable | disable | reset | status`.

## See also

- [consent-grants.md](../../../docs/adopters/reference/consent-grants.md)
- [telemetry.md](../../../docs/adopters/guides/telemetry.md)
- [telemetry-compliance.md](../../../docs/adopters/reference/telemetry-compliance.md)
- [architecture.md](../../../docs/contributors/architecture/architecture.md), security surface map
