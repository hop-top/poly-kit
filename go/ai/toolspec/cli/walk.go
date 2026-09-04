// WalkCobra converts a cobra command tree into a toolspec.ToolSpec —
// the neutral, recursive-tree representation of a CLI tool's surface.
//
// # Reflection happens once, in cmdreflect
//
// This file no longer walks cobra. [hop.top/kit/go/ai/cmdreflect] is
// the single reflector: it produces one Descriptor per command, and
// everything here is projection from that Descriptor into the
// toolspec shape. Filtering rules that used to live in a private skip
// predicate are now Descriptor.Invocable plus a
// cmdreflect.NonInvocableReason, so a command excluded from the spec
// is excluded for a named, inspectable reason rather than by falling
// through a walker's if-statement.
//
// The two projections in this package differ in output shape and
// consumer, not in what they reflect:
//
//   - BuildManifest → toolspec.Manifest (flat list, leaf commands
//     only) — kit's native agent-driveable wire format.
//   - WalkCobra → toolspec.ToolSpec (recursive tree) — neutral
//     knowledge form consumed by curation (ErrorPattern, Workflow,
//     StateIntrospection) and by adapters that need the hierarchy
//     (MCP schema, OpenAPI).
//
// Adopters do not call WalkCobra directly in normal use;
// RegisterSpecCommand calls it once and feeds the result to every
// configured FormatAdapter. WalkCobra stays exported so tests,
// capability negotiators, and external introspection tools can build
// a ToolSpec without going through the subcommand.
//
// The projection's defaults match RegisterSpecCommand's conventions:
//   - Hidden commands skipped.
//   - Cobra/fang built-ins skipped (help, completion, man,
//     __complete, __completeNoDesc, completion subcommand children).
//   - Spec subcommands skipped (kit/spec-command annotation).
//   - Deprecated commands included by default; opt out with
//     WithoutDeprecated().
//   - Local flags only on each command (persistent flags live on the
//     root spec); avoids inheritance double-counting.
//   - Destructive-name commands get inferred safety:dangerous +
//     RequiresConfirmation; everything else defaults to safety:safe.
//
// Tools that want different behavior pass options.

package cli

import (
	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/ai/toolspec"
)

// WalkOption configures WalkCobra. Options are pure functions so
// callers can compose them in any order.
type WalkOption func(*walkConfig)

// walkConfig holds resolved projection behavior. Internal; mutate
// via WalkOption functions.
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

// WithoutDeprecated suppresses the deprecation METADATA on commands
// carrying a deprecation marker (cobra Deprecated,
// kit/deprecated-since, kit/removal-target). The commands themselves
// still appear in the tree — a ToolSpec describes a surface, and a
// deprecated command is still on it.
//
// Default: deprecation metadata IS emitted (Deprecated=true,
// DeprecatedSince, ReplacedBy), so consumers can filter or surface it
// themselves. The Manifest emitter drops deprecated leaves entirely
// under --include-deprecated=false; ToolSpec consumers tend to want
// the metadata preserved, which is useful for migration prompts.
func WithoutDeprecated() WalkOption {
	return func(c *walkConfig) { c.includeDeprecated = false }
}

// WithCustomSafety overrides the default annotation-driven safety
// projection with a caller-provided classifier. The default reads
// the resolved cmdreflect.Safety; tools that want finer-grained
// safety pass their own.
//
// Returning nil from fn produces a Command with no Safety field,
// which means "unknown" for downstream consumers; returning a
// non-nil zero Safety means "explicitly classified".
func WithCustomSafety(fn func(*cobra.Command) *toolspec.Safety) WalkOption {
	return func(c *walkConfig) { c.safetyFn = fn }
}

// WithSkip overrides the default skip predicate. The default skips
// hidden commands (unless WithIncludeHidden), cobra/fang built-ins
// (help, completion and its children, man, __complete,
// __completeNoDesc), and commands carrying the kit/spec-command
// annotation. Custom predicates compose with the built-in rules —
// i.e. WithIncludeHidden suppresses the hidden-skip only.
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
// *ToolSpec directly if you call WalkCobra outside of
// RegisterSpecCommand.
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

	cfg := &walkConfig{includeDeprecated: true}
	for _, opt := range opts {
		opt(cfg)
	}

	// Reflect once, with every relaxation the ToolSpec projection
	// wants, then apply this package's own filter. The walker's
	// job is to describe; deciding what a spec publishes is this
	// projection's job.
	reflectOpts := []cmdreflect.Option{
		cmdreflect.AllowDeprecated(),
		cmdreflect.AllowInteractive(),
	}
	if cfg.includeHidden {
		reflectOpts = append(reflectOpts, cmdreflect.AllowHidden())
	}
	tree := cmdreflect.Reflect(root, reflectOpts...)

	return &toolspec.ToolSpec{
		Name:     root.Name(),
		Commands: projectChildren(tree, tree.Root, cfg),
		Flags:    projectFlags(tree.GlobalFlags),
	}
}

// projectChildren returns the visible children of d projected as
// toolspec.Command nodes, recursing.
func projectChildren(tree *cmdreflect.Tree, d *cmdreflect.Descriptor, cfg *walkConfig) []toolspec.Command {
	if d == nil {
		return nil
	}
	var out []toolspec.Command
	for _, child := range childrenOf(tree, d) {
		if specSkip(child, cfg) {
			continue
		}
		out = append(out, projectCommand(tree, child, cfg))
	}
	return out
}

// childrenOf returns the descriptors whose command is a direct child
// of d's command, in cobra's own order.
func childrenOf(tree *cmdreflect.Tree, d *cmdreflect.Descriptor) []*cmdreflect.Descriptor {
	if d == nil || d.Cmd == nil {
		return nil
	}
	byCmd := make(map[*cobra.Command]*cmdreflect.Descriptor, tree.Len())
	for _, x := range tree.Descriptors {
		byCmd[x.Cmd] = x
	}
	var out []*cmdreflect.Descriptor
	for _, c := range d.Cmd.Commands() {
		if child, ok := byCmd[c]; ok {
			out = append(out, child)
		}
	}
	return out
}

// specSkip decides whether the ToolSpec projection omits d.
//
// The reflector already recorded WHY each command is non-invocable;
// this maps those reasons onto the spec's own policy. Deprecated
// commands are kept by default (the spec is the place a migrating
// agent learns a command is going away), and interactive commands
// are kept because a spec describes a surface rather than gating
// invocation on it.
func specSkip(d *cmdreflect.Descriptor, cfg *walkConfig) bool {
	if d == nil || d.Cmd == nil {
		return true
	}
	switch d.Reason {
	case cmdreflect.ReasonHiddenInternal, cmdreflect.ReasonBuiltin:
		return true
	}
	// The spec subcommand annotation excludes a command even when
	// it is not otherwise reserved.
	if d.Surface.SpecCommand {
		return true
	}
	if cfg.skipFn != nil && cfg.skipFn(d.Cmd) {
		return true
	}
	return false
}

// projectCommand turns one descriptor (and its descendants) into a
// toolspec.Command.
func projectCommand(tree *cmdreflect.Tree, d *cmdreflect.Descriptor, cfg *walkConfig) toolspec.Command {
	cmd := toolspec.Command{
		Name:     d.Cmd.Name(),
		Aliases:  append([]string(nil), d.Aliases...),
		Flags:    projectLocalFlags(d),
		Children: projectChildren(tree, d, cfg),
		Safety:   projectSafety(d, cfg),
	}

	if cfg.includeDeprecated && d.Surface.Deprecated {
		cmd.Deprecated = true
		cmd.DeprecatedSince = d.Surface.DeprecatedSince
		cmd.ReplacedBy = d.Surface.ReplacedBy
	}

	if contract := projectContract(d); contract != nil {
		cmd.Contract = contract
	}
	return cmd
}

// projectSafety returns the toolspec.Safety for d, honoring a
// caller-supplied classifier when one was installed.
//
// Never returns nil under the default classifier so consumers can
// rely on Safety being present.
func projectSafety(d *cmdreflect.Descriptor, cfg *walkConfig) *toolspec.Safety {
	if cfg.safetyFn != nil {
		return cfg.safetyFn(d.Cmd)
	}
	return &toolspec.Safety{
		Level:                d.Safety.Level,
		RequiresConfirmation: d.Safety.RequiresConfirmation,
		Permissions:          d.Safety.PermissionStrings(),
	}
}

// projectContract reads the resolved side-effect and idempotency
// into a toolspec.Contract. Returns nil when neither is declared so
// the emitted JSON omits the field.
//
// SideEffects carries what the ADOPTER wrote, not kit's
// normalization of it: the canonical resolved tier surfaces via
// Safety.Permissions instead. A tool that declared the legacy
// "write" should read "write" back out of its own spec.
func projectContract(d *cmdreflect.Descriptor) *toolspec.Contract {
	var contract *toolspec.Contract
	if se := d.Safety.DeclaredSideEffect; se != "" {
		contract = &toolspec.Contract{SideEffects: []string{se}}
	}
	if d.Safety.Idempotent == "yes" {
		if contract == nil {
			contract = &toolspec.Contract{}
		}
		contract.Idempotent = true
	}
	return contract
}

// projectLocalFlags projects the descriptor's own flags. Hidden
// flags are omitted EXCEPT deprecated ones: pflag.MarkDeprecated
// implicitly hides a flag, but agents reading the spec want to see
// deprecated flags (with Deprecated=true) so they can warn users
// away from them. Always-hidden non-deprecated flags stay omitted.
func projectLocalFlags(d *cmdreflect.Descriptor) []toolspec.Flag {
	return projectFlags(d.Flags)
}

// projectFlags applies the hidden/deprecated rule to a flag slice.
func projectFlags(flags []cmdreflect.Flag) []toolspec.Flag {
	var out []toolspec.Flag
	for _, f := range flags {
		if f.Hidden && !f.Deprecated {
			continue
		}
		out = append(out, toolspec.Flag{
			Name:        f.Name,
			Short:       f.Short,
			Type:        f.Type,
			Description: f.Description,
			Deprecated:  f.Deprecated,
		})
	}
	return out
}
