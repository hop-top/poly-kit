# celpermission

## What it answers

How a served kit tool enforces the `permissions:` rules of its
`--policy` file: CEL expressions over each invocation, evaluated by the
same engine and evaluator [`runtime/policy`](../../../runtime/policy/README.md)
runs on bus events. Separate from `go/console/cli` so only a tool that
wires it links cel-go. Wrong package for a decision that needs your own
data (use `cli.WithPermission`) or for scopes alone (`kit/permissions`).

## Use it when

- a served tool should honor rules in its policy file → `celpermission.With()` beside `cli.WithPolicy(...)`
- you hold rules in code, or test them → `celpermission.New(rules)`, a `cmdsurface.PermissionFunc`

## Quick start

```go
gate, err := celpermission.New([]policy.PermissionRule{{
    Name: "acme-only", When: `principal.tenant == "acme"`,
    Effect: policy.RuleAllow, Otherwise: policy.RuleDeny, Message: "acme tenants only",
}})
b := cmdsurface.New(root, cmdsurface.WithPermission(gate))
_, err = b.Invoke(ctx, inv) // Tenant "globex":
// cmdsurface: permission denied: list on rest: permission rule "acme-only": acme tenants only
```

`example_test.go` in this directory verifies the snippet.

## Contract

- Bindings: `principal` {`id`, `tenant`, `scopes` (verified only),
  `established`, `source`: `verified`|`transport`|`none`}; `resource`
  {`kind`: `command`, `id` (path key), `path`, `tier`}; `context`
  {`surface`, `client_addr` (IP, no port)}; `payload` {`args`, `flags`
  the caller set}. An undeclared name is a compile error.
- Every rule compiles in `New`; one that cannot fails with `permission
  rule "<name>" does not compile`, and `cli` refuses to serve (exit 2).
- Deny overrides; zero denials allow. A rule that fails to evaluate
  (a flag read without `has()`) denies.
- Reason: `permission rule "<name>": <message>`, carried by
  `ErrPermissionDenied` and the audit record. Never `CallerIndependent`.
- Runs in slot 6 after the scope check and the policy's `allow`, before
  `cli.WithPermission`: rules only narrow.
- Budget: one decision over three rules ≤ 10 µs (about 3.5 µs, Apple
  M1); `TestGate_WithinBudget` fails above 50 µs.

## Neighbours

- `hop.top/kit/go/console/cli/policy`: `PermissionRule`, the YAML loader.
- `hop.top/kit/go/runtime/policy`, `.../policy/cel`: engine and evaluator.
- `hop.top/kit/go/transport/cmdsurface`: `PermissionFunc`, `InvocationFromContext`.

## See also

- [Secure remote serving, step 5](../../../../docs/adopters/guides/secure-remote-serving.md#5-require-scopes-then-wire-a-permission-policy)
