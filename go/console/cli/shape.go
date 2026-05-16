package cli

import "github.com/spf13/cobra"

// Shape-related annotation defaults used by the validator (design
// the static-conformance contract). Adopters override via Config.
const (
	// defaultMaxTopLevelVerbs caps the number of depth-1 leaves a
	// root can carry before the validator reports an error.
	defaultMaxTopLevelVerbs = 10
	// defaultMaxHierarchyDepth caps how deep the command tree may
	// nest. Adopter override via Config.MaxHierarchyDepth; values
	// above hardMaxHierarchyDepth are clamped.
	defaultMaxHierarchyDepth = 3
	// hardMaxHierarchyDepth is the absolute upper bound on
	// MaxHierarchyDepth, regardless of Config override. Trees this
	// deep are almost certainly modeled wrong.
	hardMaxHierarchyDepth = 5
)

// PassthroughStrictness values control how the validator treats
// commands annotated kit/passthrough (cmds that forward `-- args`
// to a child process). Default is "warn"; adopters who consider
// passthrough commands an error in their tool flip to "reject";
// those who tolerate them silently use "silent".
const (
	PassthroughWarn   = "warn"
	PassthroughReject = "reject"
	PassthroughSilent = "silent"
)

// SetTopLevelVerb marks cmd as an intentional depth-1 leaf
// (`<tool> <verb>` shape; e.g. `kit init`). Without it the shape
// validator rejects depth-1 runnable leaves under EnforceValidate.
func SetTopLevelVerb(cmd *cobra.Command) {
	setAnnotationTrue(cmd, kitTopLevelVerb)
}

// SetHierarchical marks an intermediate non-runnable node as an
// intentional grouping level. Required when leaves under the node
// sit at depth >= 3.
func SetHierarchical(cmd *cobra.Command) {
	setAnnotationTrue(cmd, kitHierarchical)
}

// SetPassthrough marks cmd as accepting opaque positional `-- args`
// forwarded to a child process. Surfaces in the spec manifest.
func SetPassthrough(cmd *cobra.Command) {
	setAnnotationTrue(cmd, kitPassthrough)
}

// IsTopLevelVerb reports whether kit/top-level-verb is "true" on cmd.
func IsTopLevelVerb(cmd *cobra.Command) bool {
	return readBoolAnnotation(cmd, kitTopLevelVerb)
}

// IsHierarchical reports whether kit/hierarchical is "true" on cmd.
func IsHierarchical(cmd *cobra.Command) bool {
	return readBoolAnnotation(cmd, kitHierarchical)
}

// IsPassthrough reports whether kit/passthrough is "true" on cmd.
func IsPassthrough(cmd *cobra.Command) bool {
	return readBoolAnnotation(cmd, kitPassthrough)
}

func setAnnotationTrue(cmd *cobra.Command, key string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[key] = "true"
}

func readBoolAnnotation(cmd *cobra.Command, key string) bool {
	if cmd == nil || cmd.Annotations == nil {
		return false
	}
	return cmd.Annotations[key] == "true"
}

// IsReserved reports whether name is in the set of subcommand names
// kit considers reserved on this root. Adopters who shadow a reserved
// name with their own command implementation still pass the
// presence-by-name check; reservation is informational for the shape
// validator and for projector annotations.
func (r *Root) IsReserved(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.reservedSubcommands[name]
	return ok
}

// MarkReserved records name as a kit-reserved subcommand on r. Only
// kit-shipped factories that mount AFTER cli.New (legacy
// RegisterSpecCommand etc.) should call this; adopters never do.
func (r *Root) MarkReserved(name string) {
	if r == nil {
		return
	}
	if r.reservedSubcommands == nil {
		r.reservedSubcommands = make(map[string]struct{})
	}
	r.reservedSubcommands[name] = struct{}{}
}

// ReservedSubcommands returns the sorted list of reserved subcommand
// names. Intended for the spec manifest's status section; not on the
// hot path.
func (r *Root) ReservedSubcommands() []string {
	if r == nil || len(r.reservedSubcommands) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.reservedSubcommands))
	for k := range r.reservedSubcommands {
		out = append(out, k)
	}
	// Stable order without importing sort here keeps the file
	// dependency-free; callers that need lexical order can sort
	// themselves. We do a tiny insertion sort to keep output
	// deterministic for tests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// reservedSnapshot captures the current child names of r.Cmd into
// r.reservedSubcommands. Called once from cli.New after the
// functional opts run; legacy factories use MarkReserved for
// late-mount entries.
func (r *Root) reservedSnapshot() {
	if r == nil || r.Cmd == nil {
		return
	}
	if r.reservedSubcommands == nil {
		r.reservedSubcommands = make(map[string]struct{})
	}
	for _, c := range r.Cmd.Commands() {
		r.reservedSubcommands[c.Name()] = struct{}{}
	}
}
