# stage

## What it answers

Which operating mode a scope is in, and which mutations that mode permits:
active, public feedback, feature freeze, maintenance, sunset, archived.
Where the mode is stored is `core/projects`; what enforces it is
`runtime/policy`.

## Use it when

- read the current mode → `stage.Read(scope)`
- propose a transition (veto-able) → `Manager.Propose`
- commit a transition → `Manager.Set`
- expire a time-boxed stage → `Manager.Tick`
- construct a manager with a bus → `stage.NewManager(stage.WithPublisher(pub))`
- rename topics → `stage.WithTopicPrefix("myapp.runtime.stage")`, `stage.WithTopics(stage.Topics{...})`
- drive it from the shell → `<tool> stage show|set|why|list`

## Quick start

```go
mgr := stage.NewManager(stage.WithPublisher(pub))
st, err := stage.Read("ops")
```

## Contract

- Six stages: `active` allows everything; `public_feedback` allows only feedback-typed track creates; `feature_freeze` allows fix, chore and docs tasks but blocks new tracks and feature or refactor tasks; `maintenance` allows fix, chore and docs tasks and blocks new tracks; `sunset` allows updates and deletes but no creates; `archived` allows reads only.
- `State` is persisted on `core/projects.Entry.Stage` and carries `Stage`, `Since` (UTC), optional `Until`, `Reason`, advisory CEL `Allow` and `Deny` hints, and `Actor`.
- A missing `Stage` field reads as `StageActive`, so adopters not opting in are unaffected.
- Five topics: `kit.runtime.stage.proposed` is sync and veto-able from `Propose`; `transitioned` and `entered` are post-phase from `Set`; `expired` is post-phase from `Tick`; `violated` is post-phase from `runtime/policy`. Topic overrides follow the `runtime/domain.Topics` convention.
- `projects.yaml` schema is version 2. Version 1 files read transparently and a missing `stage:` key means `StageActive`.

## Neighbours

- `go/core/projects`: the registry entry that stores `State`.
- `go/runtime/policy`: the engine that enforces the mode, with the default ruleset in [`stage.yaml`](../../runtime/policy/stage.yaml).
- `go/console/stage`: the shared `<tool> stage` subcommand tree.

## See also

- [Stage reference](../../../docs/adopters/reference/stage.md): stage table, `State` shape, event topics, `projects.yaml` v2 schema, wiring example
- [Go primitives index](../../../docs/adopters/reference/go-primitives.md)
