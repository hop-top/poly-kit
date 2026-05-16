package config

// ReloadFailReason enumerates the failure modes Reloadable surfaces on
// the reload_failed topic. Future reasons should be additive; existing
// values must not be repurposed because subscribers may match on them.
type ReloadFailReason string

const (
	// ReloadFailReasonImmutableChanged means Reload was vetoed because at
	// least one field not tagged `reload:"true"` differs between the held
	// snapshot and the freshly-loaded one.
	ReloadFailReasonImmutableChanged ReloadFailReason = "immutable_changed"

	// ReloadFailReasonLoadError means Load itself failed (file parse
	// error, missing required path, partition error). The snapshot is
	// unchanged.
	ReloadFailReasonLoadError ReloadFailReason = "load_error"
)

// ReloadedPayload is the bus payload for kit.config.snapshot.reloaded.
//
// MutableDiff carries one entry per dotted path whose value changed in
// the new snapshot. When no mutable field changed, MutableDiff is empty
// — the event still fires so subscribers can observe the no-op reload
// (e.g. for liveness probes).
//
// SourcePaths records the file paths Load consulted, in layer order
// (system, user, project, then ExtraConfigPaths). Empty layers stay as
// "" placeholders so subscribers can identify which slot was unset.
type ReloadedPayload struct {
	MutableDiff map[string]FieldDiff `json:"mutable_diff"`
	SourcePaths []string             `json:"source_paths"`
}

// ReloadFailedPayload is the bus payload for kit.config.snapshot.reload_failed.
//
// Reason categorizes the failure. Offending lists the dotted paths that
// triggered the veto when Reason == ReloadFailReasonImmutableChanged;
// it is empty for ReloadFailReasonLoadError.
//
// MutableDiff is best-effort: when an immutable veto fires we also report
// any mutable fields that would have changed, so operators can see the
// full would-have-been delta. For a load error the diff is left empty.
//
// Error is the stringified error for human inspection; subscribers
// should not parse it.
type ReloadFailedPayload struct {
	Reason      ReloadFailReason     `json:"reason"`
	Offending   []string             `json:"offending,omitempty"`
	Error       string               `json:"error"`
	MutableDiff map[string]FieldDiff `json:"mutable_diff,omitempty"`
	SourcePaths []string             `json:"source_paths"`
}
