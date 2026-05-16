// Walk converts a cobra command tree into a toolspec.ToolSpec —
// the neutral, recursive-tree representation of a CLI tool's surface.
// Distinct from BuildManifest in spec_command.go, which produces the
// flat-leaves Manifest shape kit's <tool> spec subcommand emits today.
//
// Both walkers operate on the same cobra tree; they differ in output
// shape and consumer:
//   - BuildManifest → Manifest (flat list, leaf commands only) — kit's
//     native agent-driveable wire format.
//   - WalkCobra → ToolSpec (recursive tree) — neutral knowledge form
//     consumed by curation (ErrorPattern, Workflow, StateIntrospection)
//     and by adapters that need the hierarchy (MCP schema, OpenAPI,
//     etc.).
//
// Adopters do not call WalkCobra directly in normal use;
// RegisterSpecCommand calls it once and feeds the result to every
// configured FormatAdapter. WalkCobra is exported so tests, capability
// negotiators, and external introspection tools can build a ToolSpec
// without going through the subcommand.
//
// The walker's defaults match the conventions used by RegisterSpecCommand:
//   - Hidden commands skipped.
//   - Cobra/fang built-ins skipped (help, completion, man, __complete,
//     __completeNoDesc, completion subcommand children).
//   - Spec subcommands skipped (kit/spec-command annotation).
//   - Deprecated commands included by default; opt out with
//     WithoutDeprecated() to match --include-deprecated=false default.
//   - Local flags only on each command (persistent flags live on the
//     root spec via collectPersistentFlags); avoids inheritance
//     double-counting.
//   - Destructive-name commands get inferred safety:dangerous +
//     RequiresConfirmation; everything else defaults to safety:safe.
//
// Tools that want different behavior pass options.

package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/ai/toolspec"
)

// destructiveNames is the lightweight heuristic kit applies when no
// explicit kit/side-effect annotation is set: any command whose leaf
// name matches is classified safety:dangerous + requires_confirmation.
// Adopters should still cross-check with --help; this is a default,
// not a contract.
var destructiveNames = map[string]bool{
	"delete":  true,
	"remove":  true,
	"rm":      true,
	"destroy": true,
	"purge":   true,
	"drop":    true,
}

// resolvedTier is the canonical 6-tier ladder declared in ADR-0019.
// Internal — used only as the projection target the walker maps
// every accepted side-effect annotation value into.
type resolvedTier string

const (
	tierRead              resolvedTier = "read"
	tierWriteLocal        resolvedTier = "write-local"
	tierWriteShared       resolvedTier = "write-shared"
	tierDestructiveLocal  resolvedTier = "destructive-local"
	tierDestructiveShared resolvedTier = "destructive-shared"
	tierInteractive       resolvedTier = "interactive"
	tierUnknown           resolvedTier = ""
)

// resolveTier maps a kit/side-effect annotation value into the
// canonical 6-tier ladder. Both legacy 4-tier values (read|write|
// destructive|interactive) and the expanded 6-tier values are
// accepted. Legacy values map conservatively: bare "write" lands at
// write-shared; bare "destructive" lands at destructive-shared.
// Unknown values return tierUnknown so the walker can fall back to
// the destructive-name heuristic.
//
// See ADR-0019 §"Legacy → new mapping".
func resolveTier(raw string) resolvedTier {
	switch raw {
	case "read":
		return tierRead
	case "write":
		// Conservative legacy default: assume shared scope.
		return tierWriteShared
	case "write-local":
		return tierWriteLocal
	case "write-shared":
		return tierWriteShared
	case "destructive":
		// Conservative legacy default: assume shared scope.
		return tierDestructiveShared
	case "destructive-local":
		return tierDestructiveLocal
	case "destructive-shared":
		return tierDestructiveShared
	case "interactive":
		return tierInteractive
	}
	return tierUnknown
}

// fsPermissionForTier returns the kit:fs:* permission token the
// walker emits for a given resolved tier. Interactive maps to
// kit:fs:read because an interactive session by itself doesn't
// imply mutation; the user types commands inside it that carry
// their own tiers.
func fsPermissionForTier(t resolvedTier) toolspec.Permission {
	switch t {
	case tierRead, tierInteractive:
		return toolspec.PermFSRead
	case tierWriteLocal:
		return toolspec.PermFSWriteLocal
	case tierWriteShared:
		return toolspec.PermFSWriteShared
	case tierDestructiveLocal:
		return toolspec.PermFSDestructiveLocal
	case tierDestructiveShared:
		return toolspec.PermFSDestructiveShared
	}
	return toolspec.PermFSRead
}

// safetyLevelForTier projects the resolved tier into the legacy
// 3-value Safety.Level enum so existing consumers keep working.
// Per ADR-0019 §"Safety.Level (legacy 3-tier) projection".
func safetyLevelForTier(t resolvedTier) toolspec.SafetyLevel {
	switch t {
	case tierRead:
		return toolspec.SafetyLevelSafe
	case tierWriteLocal, tierWriteShared, tierInteractive:
		return toolspec.SafetyLevelCaution
	case tierDestructiveLocal, tierDestructiveShared:
		return toolspec.SafetyLevelDangerous
	}
	return toolspec.SafetyLevelSafe
}

// resolveNetwork maps a kit/network annotation value to its
// permission token. Empty / missing / unknown values default to
// kit:network:none.
//
// Accepted values: none | egress:public | egress:private | ingress.
func resolveNetwork(raw string) toolspec.Permission {
	switch raw {
	case "egress:public":
		return toolspec.PermNetworkEgressPublic
	case "egress:private":
		return toolspec.PermNetworkEgressPrivate
	case "ingress":
		return toolspec.PermNetworkIngress
	}
	return toolspec.PermNetworkNone
}

// WalkOption configures WalkCobra. Options are pure functions so
// callers can compose them in any order.
type WalkOption func(*walkConfig)

// walkConfig holds resolved walker behavior. Internal; mutate via
// WalkOption functions.
type walkConfig struct {
	includeHidden     bool
	includeDeprecated bool
	safetyFn          func(*cobra.Command) *toolspec.Safety
	skipFn            func(*cobra.Command) bool
}

// WithIncludeHidden includes commands marked Hidden=true in the
// emitted tree. Default: false (hidden commands omitted).
func WithIncludeHidden() WalkOption {
	return func(c *walkConfig) { c.includeHidden = true }
}

// WithoutDeprecated omits commands carrying any deprecation marker
// (cobra Deprecated, kit/deprecated-since, kit/removal-target).
// Default: deprecated commands ARE included with Deprecated=true on
// the resulting toolspec.Command, so consumers can choose to filter
// or surface them. The Manifest emitter uses --include-deprecated=false
// by default; ToolSpec consumers tend to want the deprecation metadata
// preserved (useful for migration prompts).
func WithoutDeprecated() WalkOption {
	return func(c *walkConfig) { c.includeDeprecated = false }
}

// WithCustomSafety overrides the default destructive-name heuristic
// with a caller-provided classifier. The default classifier inspects
// the command's leaf name against destructiveNames; tools that want
// finer-grained safety (e.g. inferring from kit/side-effect annotations
// or per-tool conventions) pass their own.
//
// Returning nil from fn produces a Command with no Safety field, which
// means "unknown" for downstream consumers; returning a non-nil zero
// Safety means "explicitly classified".
func WithCustomSafety(fn func(*cobra.Command) *toolspec.Safety) WalkOption {
	return func(c *walkConfig) { c.safetyFn = fn }
}

// WithSkip overrides the default skip predicate. The default skips
// hidden commands (unless WithIncludeHidden), cobra/fang built-ins
// (help, completion and its children, man, __complete, __completeNoDesc),
// and commands carrying the kit/spec-command annotation. Custom
// predicates compose with the hidden-skip — i.e. WithIncludeHidden
// suppresses the hidden-skip only.
func WithSkip(fn func(*cobra.Command) bool) WalkOption {
	return func(c *walkConfig) { c.skipFn = fn }
}

// WalkCobra walks root and produces a toolspec.ToolSpec. The root
// command itself is reflected as ToolSpec.Name and its persistent
// flags as ToolSpec.Flags; each child is recursively projected into
// ToolSpec.Commands.
//
// Curation fields (ErrorPatterns, Workflows, StateIntrospection) are
// not populated by WalkCobra — they're caller-provided concerns
// supplied via RegisterSpecCommand options. Set them on the returned
// *ToolSpec directly if you call WalkCobra outside of RegisterSpecCommand.
//
// schemaVersion is left empty here; RegisterSpecCommand sets it from
// its own argument. Direct callers can set it after the walk:
//
//	spec := WalkCobra(root)
//	spec.SchemaVersion = "1.0"
func WalkCobra(root *cobra.Command, opts ...WalkOption) *toolspec.ToolSpec {
	if root == nil {
		return &toolspec.ToolSpec{}
	}

	cfg := &walkConfig{
		includeDeprecated: true,
		safetyFn:          defaultSafety,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	spec := &toolspec.ToolSpec{
		Name:     root.Name(),
		Commands: walkChildren(root, cfg),
		Flags:    persistentFlags(root),
	}
	return spec
}

// walkChildren returns the visible children of c projected as
// toolspec.Command nodes, recursing.
func walkChildren(c *cobra.Command, cfg *walkConfig) []toolspec.Command {
	var out []toolspec.Command
	for _, sub := range c.Commands() {
		if shouldSkip(sub, cfg) {
			continue
		}
		out = append(out, projectCommand(sub, cfg))
	}
	return out
}

// projectCommand turns a single cobra.Command (and its descendants)
// into a toolspec.Command, applying the configured safety classifier
// and projecting kit annotations into Contract / OutputSchema fields.
func projectCommand(c *cobra.Command, cfg *walkConfig) toolspec.Command {
	cmd := toolspec.Command{
		Name:     c.Name(),
		Aliases:  append([]string(nil), c.Aliases...),
		Flags:    localFlags(c),
		Children: walkChildren(c, cfg),
		Safety:   cfg.safetyFn(c),
	}

	if cfg.includeDeprecated && isDeprecated(c) {
		cmd.Deprecated = true
		if c.Deprecated != "" {
			cmd.DeprecatedSince = c.Deprecated
		}
		if since := annotation(c, "kit/deprecated-since"); since != "" {
			cmd.DeprecatedSince = since
		}
		if replaced := annotation(c, "kit/replaced-by"); replaced != "" {
			cmd.ReplacedBy = replaced
		}
	}

	if contract := projectContract(c); contract != nil {
		cmd.Contract = contract
	}

	return cmd
}

// projectContract reads kit/side-effect and kit/idempotent annotations
// into a toolspec.Contract. Returns nil when neither is set so the
// emitted JSON omits the field. SideEffects is a slice for forward
// compat with multi-effect declarations; the annotation is a single
// string drawn from either the legacy 4-tier vocabulary
// (read|write|destructive|interactive) or the expanded 6-tier ladder
// from ADR-0019 (read|write-local|write-shared|destructive-local|
// destructive-shared|interactive). Whichever the adopter wrote is
// preserved verbatim in SideEffects[0]; the canonical resolved tier
// surfaces via Safety.Permissions on the Command.
func projectContract(c *cobra.Command) *toolspec.Contract {
	if c.Annotations == nil {
		return nil
	}
	var contract *toolspec.Contract
	if se := c.Annotations["kit/side-effect"]; se != "" {
		contract = &toolspec.Contract{SideEffects: []string{se}}
	}
	if id := c.Annotations["kit/idempotent"]; id == "yes" {
		if contract == nil {
			contract = &toolspec.Contract{}
		}
		contract.Idempotent = true
	}
	return contract
}

// defaultSafety projects the cobra command's annotations into a
// Safety record. Resolution order:
//
//  1. If kit/side-effect is set and resolves to a known tier
//     (legacy or expanded), use it.
//  2. Otherwise, fall back to the destructive-name heuristic
//     (delete|remove|rm|destroy|purge|drop → dangerous).
//  3. Otherwise, default to "safe".
//
// Safety.Permissions is always populated:
//
//   - Exactly one kit:fs:* token (derived from the resolved tier;
//     the destructive-name heuristic emits kit:fs:destructive:shared
//     since the legacy heuristic doesn't disambiguate scope).
//   - Exactly one kit:network:* token (derived from kit/network;
//     defaults to kit:network:none when absent).
//   - Optional kit:exec:subprocess when the kit/exec annotation is
//     present (any non-empty value).
//   - Optional kit:bus:publish when the kit/bus-publish annotation
//     is present.
//
// RequiresConfirmation is set when the resolved tier is destructive
// (either local or shared), or when the network annotation is
// egress:private or ingress. See ADR-0019 default-policy table.
//
// Never returns nil so consumers can rely on Safety being present.
func defaultSafety(c *cobra.Command) *toolspec.Safety {
	if c == nil {
		return nil
	}

	// Step 1: try to resolve the explicit kit/side-effect annotation.
	tier := tierUnknown
	if c.Annotations != nil {
		tier = resolveTier(c.Annotations["kit/side-effect"])
	}

	// Step 2: fall back to the destructive-name heuristic. We treat
	// the heuristic as "destructive-shared" for permissions purposes
	// — the lightweight name match doesn't know whether the effect
	// is local- or shared-scoped, so we pick the conservative read
	// (matches the legacy "destructive" → destructive-shared map).
	heuristicHit := false
	if tier == tierUnknown && destructiveNames[c.Name()] {
		tier = tierDestructiveShared
		heuristicHit = true
	}

	// Step 3: default to "read" when nothing else fired.
	if tier == tierUnknown {
		tier = tierRead
	}

	level := safetyLevelForTier(tier)
	netPerm := toolspec.PermNetworkNone
	if c.Annotations != nil {
		netPerm = resolveNetwork(c.Annotations["kit/network"])
	}

	perms := []string{
		fsPermissionForTier(tier).String(),
		netPerm.String(),
	}
	if c.Annotations != nil {
		if c.Annotations["kit/exec"] != "" {
			perms = append(perms, toolspec.PermExecSubprocess.String())
		}
		if c.Annotations["kit/bus-publish"] != "" {
			perms = append(perms, toolspec.PermBusPublish.String())
		}
	}

	requiresConfirm := tier == tierDestructiveLocal ||
		tier == tierDestructiveShared ||
		netPerm == toolspec.PermNetworkEgressPrivate ||
		netPerm == toolspec.PermNetworkIngress

	// The destructive-name heuristic was historically paired with
	// RequiresConfirmation; preserve that behavior even when the
	// tier resolution didn't escalate (it does today, but keep the
	// guard explicit so a future tier-table tweak doesn't quietly
	// silence the heuristic).
	if heuristicHit {
		requiresConfirm = true
	}

	return &toolspec.Safety{
		Level:                level,
		RequiresConfirmation: requiresConfirm,
		Permissions:          perms,
	}
}

// shouldSkip applies the configured skip rules. Hidden commands are
// always skipped unless WithIncludeHidden was passed; built-ins always
// skipped; the configured custom skip predicate (if any) runs last.
func shouldSkip(c *cobra.Command, cfg *walkConfig) bool {
	if c == nil {
		return true
	}
	if c.Hidden && !cfg.includeHidden {
		return true
	}
	if isBuiltin(c) {
		return true
	}
	if cfg.skipFn != nil && cfg.skipFn(c) {
		return true
	}
	return false
}

// isBuiltin returns true for cobra/fang built-in subcommands and the
// spec subcommand itself. Mirrors isSpecOrBuiltin in spec_command.go,
// but lives here so WalkCobra has no dependency on spec subcommand
// internals (the spec subcommand depends on the walk, not the other
// way around).
func isBuiltin(c *cobra.Command) bool {
	if c.Annotations != nil && c.Annotations["kit/spec-command"] == "true" {
		return true
	}
	switch c.Name() {
	case "help", "completion", "man", "__complete", "__completeNoDesc":
		return true
	}
	if p := c.Parent(); p != nil && p.Name() == "completion" {
		return true
	}
	return false
}

// isDeprecated reports whether c carries any deprecation marker.
// Mirrors commandIsDeprecated in spec_command.go to keep the two
// walkers consistent without an internal-package import.
func isDeprecated(c *cobra.Command) bool {
	if c == nil {
		return false
	}
	if c.Deprecated != "" {
		return true
	}
	if c.Annotations == nil {
		return false
	}
	return c.Annotations["kit/deprecated-since"] != "" ||
		c.Annotations["kit/removal-target"] != ""
}

// annotation returns the value of c.Annotations[key] or empty string
// if c or its annotation map is nil.
func annotation(c *cobra.Command, key string) string {
	if c == nil || c.Annotations == nil {
		return ""
	}
	return c.Annotations[key]
}

// localFlags projects pflag.Flag → toolspec.Flag for flags declared
// directly on c (not inherited from ancestors). Persistent globals
// live on the spec's top-level Flags via persistentFlags. Hidden
// flags are omitted EXCEPT deprecated flags: pflag.MarkDeprecated
// implicitly sets Hidden=true, but agents reading the spec want to
// see deprecated flags (with Deprecated=true) so they can warn users
// away from them. Always-hidden non-deprecated flags stay omitted.
func localFlags(c *cobra.Command) []toolspec.Flag {
	var out []toolspec.Flag
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden && f.Deprecated == "" {
			return
		}
		out = append(out, projectFlag(f))
	})
	return out
}

// persistentFlags projects pflag.Flag → toolspec.Flag for persistent
// flags declared directly on c. Used at the root to populate
// ToolSpec.Flags. Subcommand persistent flags surface as local flags
// on whichever subcommand declared them. Deprecated-flag handling
// matches localFlags.
func persistentFlags(c *cobra.Command) []toolspec.Flag {
	var out []toolspec.Flag
	c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden && f.Deprecated == "" {
			return
		}
		out = append(out, projectFlag(f))
	})
	return out
}

// projectFlag converts a pflag.Flag into a toolspec.Flag. Names are
// stored raw (without "--") to match kit's existing Manifest
// convention; downstream renderers add the prefix when emitting.
func projectFlag(f *pflag.Flag) toolspec.Flag {
	flag := toolspec.Flag{
		Name:        f.Name,
		Short:       f.Shorthand,
		Type:        f.Value.Type(),
		Description: f.Usage,
	}
	if f.Deprecated != "" {
		flag.Deprecated = true
	}
	return flag
}
