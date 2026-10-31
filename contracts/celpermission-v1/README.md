# celpermission-v1

## What it answers

What the `permissions:` rules of a `--policy` file decide for one served invocation, independent of language: which bindings a rule reads, how rules compose, what a failing expression does, and which rules are refused at load.

## Use it when

- you change the rule bindings, the refusal text or the composition in any SDK: add a vector first, then make every port pass it
- you port `go/console/cli/celpermission`

## Quick start

```sh
go test ./go/console/cli/celpermission/ -run '^TestConformanceCelPermission'
cargo test --features cli-permissions --test celpermission_conformance   # from sdk/rs
```

## Contract

Go is canonical. A runner failure means the implementation drifted, not that the vector should be edited.

`vectors.json` holds a `tree` (command path to side-effect tier), `suites` and `compile_errors`. A suite names `rules` (a case may carry its own) and `cases`; a case gives a `request` (`caller`, `tenant`, `scopes`, `source` of `verified|transport|none`, `surface`, `remote_addr`, `path`, `args`, `flags`) and the verdict: `allowed`, plus the refusal `reason` exactly or its `reason_prefix` where the text carries an interpreter message. `compile_errors` rules must be refused at construction, the message starting with `error_prefix` and containing `error_contains`.

Pinned: every binding (`principal.id|tenant|scopes|established|source`, `resource.kind|id|path|tier`, `context.surface|client_addr`, `payload.args|flags`), the port stripped from the client address (IPv4, bracketed IPv6), scopes that count only for a verified caller, the first denying rule naming the refusal, `otherwise: deny`, evaluation errors and non-boolean results denying, the reason with and without a message, an empty block permitting all, and the load-time refusals (syntax error, undeclared name including inside a macro body, bad effect, missing name or `when`, duplicate name).

Not pinned: the text of an interpreter's evaluation error (only its `evaluation error` prefix), and static type errors (Go's CEL checker rejects a mistyped operand at load, Rust's denies at evaluation).

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/celpermission/conformance_test.go` |
| Rust | `sdk/rs/tests/celpermission_conformance.rs` |

## See also

- [`cli-root-v1`](../cli-root-v1/README.md): the root that loads the policy
- [`go/console/cli/celpermission`](../../go/console/cli/celpermission/README.md)
