# cli

standardized CLI framework and flag parsing.

## Flag validators

`Root.WithFlagValidator(name, fn)` registers a closure that rejects ill-formed
values for a persistent flag and routes the rejection through kit's structured
`output.RenderError` envelope (honoring `--format json|yaml|table|text`).

```go
root.WithFlagValidator("api-version", func(v string) *output.Error {
    if !semver.IsValid(v) {
        return &output.Error{
            Code:     "INVALID_API_VERSION",
            Message:  "api-version must be semver",
            ExitCode: 2,
        }
    }
    return nil
})
root.WrapRunE() // installs the validator on every leaf
```

This replaces the hand-rolled tree-walking pattern (e.g. an `installAPIVersionGuard`
that wraps each leaf's RunE manually). The middleware:

- runs once per leaf invocation, AFTER cobra parses the flag and BEFORE the adopter RunE
- only fires when the user actually set the flag (`flag.Changed == true`); defaults pass through
- last-registered wins for a given name (ergonomic for tests)
- silently never fires when the named flag doesn't exist anywhere on the tree (no panic)

Ordering: call `WithFlagValidator` BEFORE `WrapRunE` (or before `Execute`, which calls
`WrapRunE`). Validators registered after the subtree is wrapped are inert.

## Sub-packages

| Path | What it answers |
|------|-----------------|
| [`breaker/`](breaker/README.md) | `breaker` subcommand tree: which breakers are registered, their state, closing one |
| [`cmdmeta/`](cmdmeta/README.md) | read the `kit/*` annotations on a cobra command without importing `cli` |
| [`completion/`](completion/README.md) | dynamic shell completions for flags and positionals |
| [`config/`](config/README.md) | `config path` and `config paths` subcommands: which file loads and the precedence chain |
| [`conformance/`](conformance/README.md) | the `kit conformance` command tree and its exit codes |
| [`idemstore/`](idemstore/README.md) | storage backend for `--idempotency-key` recorded results |
| [`policy/`](policy/README.md) | delegation-safety policy YAML enforced per agent-driven invocation |
| [`router/`](router/README.md) | `kit llm router` subtree: start, stop, list, inspect RouteLLM instances |
| [`scope/`](scope/README.md) | `kit scope show`, `check`, `test`: would the path policy allow this path |
