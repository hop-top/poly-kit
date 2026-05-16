# core/stage — scope operating-mode primitive

Host-tool-agnostic primitive for declaring a scope's operating mode.
Every kit-using CLI (tlc, wsm, aps, pod, ctxt, …) reads the mode and
optionally enforces it. Mode lives on the existing `core/projects`
registry entry; enforcement rides on `runtime/policy` via the default
`runtime/policy/stage.yaml` ruleset.

## The 6 stages

| Stage             | Allowed                          | Blocked                                    |
| ----------------- | -------------------------------- | ------------------------------------------ |
| `active`          | everything                       | nothing                                    |
| `public_feedback` | feedback-typed track creates     | non-feedback track creates                 |
| `feature_freeze`  | fix/chore/docs tasks             | new tracks; feature/refactor tasks         |
| `maintenance`     | fix/chore/docs tasks             | new tracks                                 |
| `sunset`          | updates / deletes                | creates                                    |
| `archived`        | reads                            | all mutations                              |

## State shape

```go
type State struct {
    Stage  Stage      // active | public_feedback | feature_freeze | maintenance | sunset | archived
    Since  time.Time  // when the stage was entered (UTC)
    Until  *time.Time // optional auto-expiry
    Reason string     // free-form audit note
    Allow  []string   // advisory CEL hints
    Deny   []string   // advisory CEL hints
    Actor  string     // who set it
}
```

Persisted on `core/projects.Entry.Stage`. Missing field reads as
`StageActive` — zero-touch for adopters not opting in.

## The 5 events

| Topic                                | Phase | Veto-able | Source       |
| ------------------------------------ | ----- | --------- | ------------ |
| `kit.runtime.stage.proposed`         | sync  | yes       | `Propose`    |
| `kit.runtime.stage.transitioned`     | post  | no        | `Set`        |
| `kit.runtime.stage.entered`          | post  | no        | `Set`        |
| `kit.runtime.stage.expired`          | post  | no        | `Tick`       |
| `kit.runtime.stage.violated`         | post  | no        | `runtime/policy` |

Topics override via `WithTopicPrefix("myapp.runtime.stage")` or
`WithTopics(stage.Topics{Created: "..."})` (matches the
`runtime/domain.Topics` convention).

## projects.yaml extension

Schema version bumped from 1 to 2. New shape:

```yaml
schema: 2
projects:
  ops:
    path: /tmp/ops
    source: wsm
    stage:
      stage: feature_freeze
      since: 2026-05-01T00:00:00Z
      until: 2026-06-01T00:00:00Z
      reason: "preparing v2 release"
      actor: jad
```

Old (v1) files read transparently; the parser treats missing `stage:`
as `StageActive`.

## 15-line wiring example

```go
// 1. The publisher — adapter from your bus.Bus to domain.EventPublisher.
type pubAdapter struct{ b bus.Bus }
func (a *pubAdapter) Publish(ctx context.Context, topic, src string, p any) error {
    return a.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), src, p))
}

// 2. The stage manager.
mgr := stage.NewManager(stage.WithPublisher(&pubAdapter{b: myBus}))

// 3. The policy engine, wired with a stage resolver so stage.yaml works.
cfg, _ := policy.LoadConfig("path/to/runtime/policy/stage.yaml")
eng, _ := withcel.New(cfg,
    policy.WithStageResolver(func(scope string) (map[string]any, error) {
        st, err := stage.Read(scope)
        if err != nil { return nil, err }
        return map[string]any{"mode": string(st.Stage)}, nil
    }),
    policy.WithViolationPublisher(&violationAdapter{b: myBus}),
)
policy.Wire(myBus, eng)
```

## See also

- `runtime/policy/stage.yaml` — default rule set.
- `console/stage` — shared CLI subcommand (`<tool> stage show|set|why|list`).
- `~/.ops/docs/glossary.md` § Stage — terminology.
- `~/.ops/docs/glossary-event-names.md` — canonical kit topics.
