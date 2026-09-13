# envelope

## What it answers

What a failure looks like on the wire: the `Error` shape, its code and
exit-code vocabulary, the transience class agents retry on, and the
renderer that serializes it. A leaf with no terminal-UI dependency, so
library packages can name a failure without linking the renderers. Wrong
package when you are rendering results — that is the parent,
`hop.top/kit/go/console/output`.

## Use it when

- a library package returns a typed failure → `envelope.ConflictError(msg)`
- you convert an existing error → `envelope.WrapError(err, code, exit)`
- you reclassify for retry → `env.WithTransience(envelope.TransienceTransient)`
- you reattach a sentinel across a conversion → `env.Retaining(err)`
- you serialize one yourself → `envelope.RenderError(w, format, env)`

## Quick start

```go
func (e *DeniedError) AsCLIError() *envelope.Error {
    env := envelope.ConflictError(e.Error())
    env.Cause = "denied by rule " + e.Rule
    return env
}
```

## Contract

- Constructors pin code, exit and transience. `ExitCodeForClass` is the
  table — read it rather than a list in prose, including this one: the
  line that used to sit here named eight classes and had already gone
  stale on `CONSENT_REFUSED` 7 and `PREREQUISITE` 70.
- `ExitClasses` enumerates the classes kit defines, `ClassForExitCode`
  reverses the lookup, and `ExtensionBand` records the >6 allocation
  including the slots owned by `console/cli/conformance` (66, 67) and
  `conformance/client` (68, 69).
- The retained error is unexported and never reaches the wire; `Cause` is
  the human-readable form, `Unwrap` the machine-matchable one.
- `WithTransience` and `Retaining` copy rather than mutate, so a shared
  package-level envelope returned from `AsCLIError` stays race-free.
- `RenderError` normalizes an unset transience to `unknown`, so every
  structured error carries a valid class.
- Imports stay stdlib plus the YAML encoder. Anything needing lipgloss,
  a TTY probe or column projection belongs in the parent package.

## Neighbours

- [`../`](../README.md): `--format` dispatch and the renderers; aliases
  every name declared here, so adopters import `output` as before
- [`../../cli`](../../cli/README.md): the RunE middleware that calls
  `AsCLIError` and reattaches the originating error

## See also

- [Output API reference](../../../../docs/adopters/reference/output.md#errors):
  constructor table, `errors.Is` semantics
