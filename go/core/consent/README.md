# consent

## What it answers

Did the user agree to telemetry, and why is the state what it is. This
package persists the decision and resolves the precedence chain. What gets
collected, and how it leaves the process, is `go/runtime/telemetry`.

## Use it when

- wiring an application at bootstrap: `Install()` builds the `FileStore` and installs the hook into kit-telemetry
- you have your own `Store`: `NewHook(store)` then `telemetry.SetConsentHook`
- you must decide from flags, env and the persisted record: `Resolve` / `ResolveWithDiagnostics` with `Inputs`
- you need the `DO_NOT_TRACK` check on its own (prompt short-circuit, enable guard): `DoNotTrackEnabled(env)`
- `kit telemetry status` style diagnostics: `FileStore.Get`, `FileStore.Path`

## Quick start

```go
d := consent.Resolve(context.Background(), consent.Inputs{
    Env:       consent.MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
    Persisted: consent.Decision{State: consent.StateGranted},
})
fmt.Println(d.State, d.DecisionSource, d.Granted())
// Output: denied env false
```

## Contract

- On disk: `<XDG_CONFIG_HOME>/kit/config.yaml`, partition `kit.telemetry.consent`, fields `state`, `decided_at`, `prompt_version`, `decision_source`. Legacy `kit/telemetry.yaml` (bare `telemetry.consent`) is read as a fallback, never written.
- `Store.Set` writes a `*.tmp` sibling then renames, perms 0600, and preserves every other key in the file. `Get` on a missing record returns `StateUnknown` with a nil error; malformed YAML is an error.
- Precedence, highest first: `<APP>_TELEMETRY_MODE=off` or `KIT_TELEMETRY_MODE=off`; `DO_NOT_TRACK` (beats `--telemetry=on`); `--telemetry=on|off`; `KIT_TELEMETRY_CONSENT=granted|denied`; persisted decision; default denied with `SourceConfig`.
- The resolver is pure (env only via `Inputs.Env`) and never emits `SourcePrompt`; that source belongs to the interactive prompt path. Legacy env values (`allow`, `1`, `true`, ...) yield a `ResolveError` and count as unset.
- `NewHook(nil)` panics: a nil store is a wiring bug.

## Neighbours

- `go/runtime/telemetry`: `ConsentHook` interface, `Mode` (off / anon / full), emitter and sink.
- `go/core/xdg`: resolves the config path.
- `go/core/compliance`: F13 `ConsentingTelemetry` check.
- `cmd/kit`: `kit telemetry enable | disable | reset | status`.

## See also

- [telemetry.md](../../../docs/adopters/guides/telemetry.md)
- [telemetry-compliance.md](../../../docs/adopters/reference/telemetry-compliance.md)
- [architecture.md](../../../docs/contributors/architecture/architecture.md), security surface map
