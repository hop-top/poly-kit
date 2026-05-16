# kit/runtime/policy — e2e stories

Living documentation for the policy engine. Each test file in this
directory is a runnable example of one adopter or operator user
story. The goal: an adopter reading a story title (and its top
docstring) should already know whether the story matches their need,
and the test body shows them exactly how to wire it.

All tests use the EXTERNAL test package (`package e2e_test`), so
every import is something an adopter can copy verbatim into their
own program. No internals, no test-only escape hatches.

## Reading order for new adopters

1. `story_use_cel_default` — minimal wiring with the CEL backend.
2. `story_admin_only_cancel` — first realistic policy.
3. `story_delete_requires_note` — context attributes from CLI flags.

## Index

### Adoption (how to wire the engine)

| Story | Test file | ADR § | Real-world use |
|-------|-----------|-------|----------------|
| Use CEL default | `story_use_cel_default_test.go` | §1, §2 | Most adopters; one-liner with cel-go |
| Swap evaluator for OPA / Cedar | `story_swap_evaluator_to_custom_test.go` | §1, §2 | Adopters with existing policy infra |

### Policy authoring (writing rules)

| Story | Test file | ADR § | Real-world use |
|-------|-----------|-------|----------------|
| Admin-only cancel | `story_admin_only_cancel_test.go` | §3, §4 | Role-gated state transitions |
| Delete requires note | `story_delete_requires_note_test.go` | §3, §4 | Audit trail for destructive ops |
| Writes need authenticated role | `story_writes_need_authenticated_role_test.go` | §3, §4 | Reject anonymous mutations |
| Force does not bypass | `story_force_does_not_bypass_test.go` | §3, Q2 | Per-policy admin overrides |

### Behavior (engine semantics)

| Story | Test file | ADR § | Real-world use |
|-------|-----------|-------|----------------|
| Unmatched event passes | `story_unmatched_event_passes_test.go` | §3 | Backward-compatible adoption |
| Deny-overrides compose | `story_deny_overrides_compose_test.go` | §8 | Most-restrictive-wins semantics |
| Compile fails loud | `story_compile_fails_loud_test.go` | §9 | Misconfig caught at boot |
| Async rejected at parse | `story_async_subscription_rejected_test.go` | §7 | Veto-safety guarantee |

## Layout

```
e2e/
├── README.md                                       (this file)
├── helpers_test.go                                 (busAdapter, fixture entity, staticPrincipal)
├── story_*.go                                      (one user story per file)
└── testdata/
    ├── admin-only-cancel.yaml
    ├── delete-requires-note.yaml
    ├── writes-need-role.yaml
    ├── force-not-bypass.yaml
    └── multi-policy-deny-wins.yaml
```

Each YAML in `testdata/` is intentionally close to a production
policy file — real role names (`admin`, `orchestrator`, `engineer`),
real exit messages, real topic names. Adopters can lift them as
starting points.

## Running

From the kit repo root:

```sh
go test ./go/runtime/policy/e2e/...
```

Each story runs in well under 100ms; the full set runs in parallel.
