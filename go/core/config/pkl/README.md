# pkl

## What it answers

How to drive a config schema, an onboarding wizard, shell completion and
value validation from one PKL module. Loading YAML with precedence and
hot reload is `go/core/config`; the interactive steps themselves are
`go/console/wizard`.

## Use it when

- you ship a `kit config` style wizard: `root.AddCommand(pkl.NewConfigCommand(pklPath, opts))`
- you run the wizard yourself, headless in CI or interactive: `RunWizard(ctx, pklPath, WizardOpts{...})`
- you need keys and values for completion: `LoadSchema` then `CompletionKeys(s)` / `CompletionValues(s, key)`
- you validate one `key=value` before writing it: `ValidateValue(s, key, value)`
- you resolve computed fields from user answers: `Resolve(ctx, pklPath, answers)`

## Quick start

```go
s, err := pkl.LoadSchema("testdata/basic.pkl")
if err != nil {
    fmt.Println("err:", err)
    return
}
for _, item := range pkl.CompletionKeys(s) {
    fmt.Println(item.Value)
}
fmt.Println(pkl.ValidateValue(s, "port", "abc") != nil)
// Output:
// name
// port
// debug
// true
```

## Contract

- `Resolve` and `RunWizard` shell out to the `pkl` binary (`brew install pkl`, or the apple/pkl releases). `LoadSchema`, `ValidateValue`, `CompletionKeys`, `CompletionValues` and `WizardSteps` parse the source text and need no binary.
- The module must end with `output { renderer = new JsonRenderer {} }` for `Resolve` to read it.
- Field types: string, int, float, bool, duration, string enum (union type), string list. Constraints: min/max length, min/max, pattern, between. `///` comments become descriptions, non-nullable fields are required.
- `@wizard.group` groups steps; `@wizard.when` gates a field on another field's value. Computed fields (cross-field expressions) are skipped by `WizardSteps` and `CompletionKeys` and filled by `Resolve`.
- Validation errors are the typed errors of `go/core/config`, so callers match them the same way as for YAML input.

## Neighbours

- `go/core/config`: `Options`, `Scope`, the writer the wizard hands answers to.
- `go/console/wizard`: `Step`, `RunOption`, the TUI.
- `go/console/cli/completion`: consumes `CompletionItem` lists.

## See also

- [config/README.md](../README.md)
- [wizard-api.md](../../../../docs/adopters/reference/wizard-api.md)
- [completion-api.md](../../../../docs/adopters/reference/completion-api.md)
