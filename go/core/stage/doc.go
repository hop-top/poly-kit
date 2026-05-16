// Package stage is a host-tool-agnostic primitive for declaring a
// scope's operating mode — active, public_feedback, feature_freeze,
// maintenance, sunset, archived. Every kit-using CLI (tlc, wsm, aps,
// pod, ctxt, …) can read the mode and (optionally) enforce it.
//
// The mode lives on the existing core/projects registry entry;
// enforcement rides on runtime/policy via a default stage.yaml ruleset
// shipped under runtime/policy/.
//
// Surface:
//
//   - Stage  — the enum (StageActive, StagePublicFeedback, …)
//   - State  — the persisted record (Stage, Since, Until, Reason, …)
//   - Read   — load State for a project (StageActive when missing)
//   - Set    — persist State + emit transitioned/entered post events
//   - Propose — pre-event seam for runtime/policy gating
//   - Tick   — scan for State.Until <= now and emit expired events
//
// Topics (configurable via WithTopicPrefix / WithTopics):
//
//   - kit.runtime.stage.proposed     (sync, veto-able)
//   - kit.runtime.stage.transitioned (post)
//   - kit.runtime.stage.entered      (post)
//   - kit.runtime.stage.expired      (post; emitted by Tick)
//   - kit.runtime.stage.violated     (post; emitted by runtime/policy)
//
// Backward compat: missing Stage field on a projects.Entry treats the
// scope as StageActive; missing publisher = no bus emit; missing
// stage.yaml include = no enforcement. Zero-touch for adopters not
// opting in.
package stage
