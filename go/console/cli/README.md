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
