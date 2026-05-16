# runtime/policy — declarative guard engine

CEL-backed policy engine wired to kit's pre-* event seams. Adopters
declare expression rules in YAML; the engine subscribes via
`runtime/bus` and vetoes mutating ops by returning `PolicyDeniedError`
(wraps `domain.ErrConflict`).

Three veto-able topics are watched:

- `kit.runtime.state.pre_transitioned`  (state machine)
- `kit.runtime.entity.pre_validated`    (entity CRUD, raw input)
- `kit.runtime.entity.pre_persisted`    (entity CRUD, validated)

Default for events with zero matching policies: ALLOW. Composition
across N matching policies is deny-overrides; first-deny wins.

The core package is evaluator-agnostic: callers MUST supply an
`Evaluator`. Use the `policy/withcel` subpackage for the CEL backend, or
pass `WithEvaluator` to plug Cedar/OPA/etc.

## Wiring

```go
cfg, _ := policy.LoadConfig("policies.yaml")
eng,  _ := withcel.New(cfg) // CEL backend
policy.Wire(b, eng)         // b is bus.Bus
```

## Stage-driven policies

The CEL `stage` binding, populated by `policy.WithStageResolver`,
exposes the active operating mode for the resource's scope (see
`go/core/stage`). Policies that reference `stage.mode` automatically
emit `kit.runtime.stage.violated` on denial — adopters wire
`policy.WithViolationPublisher` to receive the events.

Binding shape:

```yaml
stage:
  mode:   "active" | "public_feedback" | "feature_freeze" | "maintenance" | "sunset" | "archived"
  since:  <RFC3339 timestamp>
  until:  <RFC3339 timestamp or null>
  reason: <string>
  allow:  <list of strings>
  deny:   <list of strings>
```

Default ruleset shipped at `runtime/policy/stage.yaml` covers all 6
stages — adopters include verbatim or copy-and-customize. Override
individual rules by name: subsequent loaders may replace entries with
the same `name`.

The `entity` binding (also new) is populated automatically by the
policy subscriber from `domain.PreEntityPayload`:

```yaml
entity:
  kind:       "track" | "task" | ...
  op:         "create" | "update" | "delete"
  track_type: "feature" | "fix" | "chore" | "docs" | "feedback"
```

See `go/core/stage/README.md` for the full primitive overview, and
`runtime/policy/stage.yaml` for the ruleset.
