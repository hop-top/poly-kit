package cmdreflect

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/cli/cmdmeta"
)

// Cobra annotation keys this package reads. They are the canonical
// kit/ vocabulary registered by go/console/cli; the constants are
// repeated here rather than imported because most are unexported
// there, and a reflection package that cannot name the keys it
// reads is harder to audit than one that repeats eight strings.
const (
	annSideEffect       = "kit/side-effect"
	annNetwork          = "kit/network"
	annIdempotent       = "kit/idempotent"
	annArgs             = "kit/args"
	annExitCodes        = "kit/exit-codes"
	annAuthRequired     = "kit/auth-required"
	annRequiresConfirm  = "kit/requires-confirmation"
	annDestructiveToken = "kit/destructive-token"
	annDeprecatedSince  = "kit/deprecated-since"
	annRemovalTarget    = "kit/removal-target"
	annReplacedBy       = "kit/replaced-by"
	annSince            = "kit/since"
	annFlagSince        = "kit/flag-since"
	annDryRunRationale  = "kit/dry-run-rationale"
	annExec             = "kit/exec"
	annBusPublish       = "kit/bus-publish"
	annSpecCommand      = "kit/spec-command"
	annSelfHosting      = cmdmeta.KeySelfHosting
)

// requiredFlagAnnotation is the key cobra's MarkFlagRequired writes.
// pflag exposes required-ness only through this annotation, so
// reading it is the only way to reflect the fact.
const requiredFlagAnnotation = cobra.BashCompOneRequiredFlag

// serveVerb is the depth-1 word the serve-lifecycle contract gives to
// exactly one command: the supervisor and selector over the tool's
// services. Everything under it is self-hosting by construction —
// invoking it from inside a served invocation would start a server
// inside the server. The name is fixed by the contract, not by the
// reserved-verb lookup, so the classification holds on a bare cobra
// tree too.
const serveVerb = "serve"

// reservedLookup is the seam through which the walker learns which
// depth-1 verbs a tool reserves. [hop.top/kit/go/console/cli.Root]
// satisfies it. Taking an interface rather than the concrete Root
// keeps Reflect callable from tests and from consumers that hold
// only a bare cobra tree.
type reservedLookup interface {
	IsReserved(name string) bool
}

// Tree is the complete reflection of one cobra command tree. Every
// command in the source tree has exactly one descriptor here,
// invocable or not.
type Tree struct {
	// Root is the descriptor for the root command itself.
	Root *Descriptor
	// Descriptors are every command in the tree in depth-first
	// order, root first.
	Descriptors []*Descriptor
	// GlobalFlags are the root's persistent flags — the flags
	// every command inherits.
	GlobalFlags []Flag

	byPath map[string]*Descriptor
}

// Invocable returns the descriptors a consumer may project, in tree
// order. This is the list a transport mounts and a spec emitter
// publishes.
func (t *Tree) Invocable() []*Descriptor {
	out := make([]*Descriptor, 0, len(t.Descriptors))
	for _, d := range t.Descriptors {
		if d.Invocable {
			out = append(out, d)
		}
	}
	return out
}

// NonInvocable returns the descriptors a consumer may not project,
// each carrying the reason it was excluded. A "list every command"
// view renders these alongside the invocable ones; a "list callable
// tools" view drops them.
func (t *Tree) NonInvocable() []*Descriptor {
	out := make([]*Descriptor, 0, len(t.Descriptors))
	for _, d := range t.Descriptors {
		if !d.Invocable {
			out = append(out, d)
		}
	}
	return out
}

// Lookup returns the descriptor at the given path below the root
// ("widget add"), or nil.
func (t *Tree) Lookup(pathKey string) *Descriptor {
	if t == nil || t.byPath == nil {
		return nil
	}
	return t.byPath[pathKey]
}

// Len returns the number of descriptors in the tree, root included.
func (t *Tree) Len() int {
	if t == nil {
		return 0
	}
	return len(t.Descriptors)
}

// Option configures [Reflect].
type Option func(*config)

type config struct {
	reserved            reservedLookup
	allowDeprecated     bool
	allowHidden         bool
	allowInteractive    bool
	allowReserved       bool
	destructiveDenied   bool
	destructiveDenyFunc func(*Descriptor) bool
}

// WithReserved supplies the reserved-verb lookup, normally the kit
// Root. Without it no command is classified management-only, since
// nothing knows which verbs kit reserved.
func WithReserved(r reservedLookup) Option {
	return func(c *config) { c.reserved = r }
}

// AllowDeprecated makes deprecated commands invocable. The CLI
// surface passes this: a deprecated command still runs when typed,
// it is only withheld from projected surfaces.
func AllowDeprecated() Option {
	return func(c *config) { c.allowDeprecated = true }
}

// AllowHidden makes hidden commands invocable. Reserved for the
// local CLI surface and for diagnostics; a remote transport should
// not publish what an adopter chose to hide.
func AllowHidden() Option {
	return func(c *config) { c.allowHidden = true }
}

// AllowInteractive makes interactive commands invocable. The CLI
// surface passes this, since a terminal is exactly what it has.
func AllowInteractive() Option {
	return func(c *config) { c.allowInteractive = true }
}

// AllowReserved makes kit-reserved management verbs invocable. Spec
// generation passes this: the manifest must describe `<tool> spec`
// so an agent can find the schema of every other entry.
func AllowReserved() Option {
	return func(c *config) { c.allowReserved = true }
}

// DenyDestructive marks every destructive command non-invocable
// with ReasonUnauthorizedDestructive. A transport whose policy
// refuses destructive work passes this so the commands are still
// described, with the refusal recorded, rather than vanishing.
func DenyDestructive() Option {
	return func(c *config) { c.destructiveDenied = true }
}

// DenyDestructiveFunc denies destructive commands selectively. fn
// receives a descriptor whose Safety and Surface are already
// resolved and returns true to deny it. Overrides DenyDestructive
// when both are supplied.
func DenyDestructiveFunc(fn func(*Descriptor) bool) Option {
	return func(c *config) { c.destructiveDenyFunc = fn }
}

// Reflect walks root and returns the complete [Tree].
//
// root must be fully assembled: every subcommand registered, every
// flag declared. Reflecting a tree still under construction yields
// descriptors that do not match what the binary exposes.
//
// A nil root yields an empty tree rather than a nil one, so callers
// can range over the result unconditionally.
func Reflect(root *cobra.Command, opts ...Option) *Tree {
	t := &Tree{byPath: map[string]*Descriptor{}}
	if root == nil {
		return t
	}
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	t.GlobalFlags = reflectFlags(root.PersistentFlags(), flagSince(root))

	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		d := describe(cmd, path, root, cfg)
		t.Descriptors = append(t.Descriptors, d)
		if key := d.PathKey(); key != "" {
			t.byPath[key] = d
		}
		for _, child := range cmd.Commands() {
			walk(child, append(path, child.Name()))
		}
	}
	walk(root, []string{root.Name()})

	if len(t.Descriptors) > 0 {
		t.Root = t.Descriptors[0]
	}
	return t
}

// Describe reflects a single command in the context of its tree and
// returns its descriptor, with the same facts and the same
// invocability verdict [Reflect] would record for it. It is the
// per-command entry point for a consumer that already holds a
// resolved *cobra.Command — a runner about to execute it — and needs
// its tier, output schema, or self-hosting status without walking
// the whole tree again.
//
// root anchors the path and the depth-1 verb lookup; when nil, the
// command's own root is used. A nil cmd yields nil.
func Describe(root, cmd *cobra.Command, opts ...Option) *Descriptor {
	if cmd == nil {
		return nil
	}
	if root == nil {
		root = cmd.Root()
	}
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	var path []string
	for c := cmd; c != nil; c = c.Parent() {
		path = append([]string{c.Name()}, path...)
		if c == root {
			break
		}
	}
	return describe(cmd, path, root, cfg)
}

// describe builds one descriptor. It resolves every fact first and
// decides invocability last, so the verdict is a function of the
// fully-populated descriptor rather than of walk order.
func describe(cmd *cobra.Command, path []string, root *cobra.Command, cfg *config) *Descriptor {
	d := &Descriptor{
		Path:    append([]string(nil), path...),
		Use:     cmd.Use,
		Short:   cmd.Short,
		Long:    cmd.Long,
		Aliases: append([]string(nil), cmd.Aliases...),
		Flags:   reflectFlags(cmd.LocalFlags(), flagSince(cmd)),
		Args:    reflectArgs(cmd),
		Cmd:     cmd,
	}
	d.Surface = reflectSurface(cmd, root, cfg)
	d.Output = reflectOutput(cmd)
	d.Safety = reflectSafety(cmd)
	// Resolved after Safety: one of the three signals is the
	// network class the safety projection already decoded.
	d.Surface.SelfHosting = isSelfHosting(cmd, root, d.Safety)

	decide(d, cfg)
	return d
}

// isSelfHosting reports whether cmd hosts or modifies the tool
// itself. Three independent signals, any one of which suffices:
//
//   - the command sits under the depth-1 `serve` verb, which the
//     serve-lifecycle contract fixes as the supervisor and selector
//     over the tool's services;
//   - the command declares kit/network=ingress — it accepts inbound
//     connections, which is what a server does;
//   - the command carries kit/self-hosting, the explicit mark for
//     self-modifying commands (an upgrade that replaces the binary)
//     and for anything else an adopter knows must stay on the CLI.
//
// None of the three consults the reserved-verb lookup, so a tool
// that reflects without one still withholds its server.
func isSelfHosting(cmd, root *cobra.Command, s Safety) bool {
	if cmdmeta.IsSelfHosting(cmd) {
		return true
	}
	if s.Network == toolspec.PermNetworkIngress {
		return true
	}
	return depthOneName(cmd, root) == serveVerb
}

// reflectSurface resolves the presentation and transport metadata.
func reflectSurface(cmd *cobra.Command, root *cobra.Command, cfg *config) SurfaceMeta {
	m := SurfaceMeta{
		Hidden:         cmd.Hidden,
		Runnable:       cmd.Runnable(),
		HasSubCommands: cmd.HasSubCommands(),
		Builtin:        isBuiltin(cmd),
		TopLevelVerb:   cmdmeta.IsTopLevelVerb(cmd),
		Hierarchical:   cmdmeta.IsHierarchical(cmd),
		Passthrough:    cmdmeta.IsPassthrough(cmd),
		Group:          cmd.GroupID,
	}
	if cmd.Deprecated != "" {
		m.Deprecated = true
		m.DeprecatedSince = cmd.Deprecated
	}
	if cmd.Annotations != nil {
		if v := cmd.Annotations[annDeprecatedSince]; v != "" {
			m.Deprecated = true
			m.DeprecatedSince = v
		}
		if v := cmd.Annotations[annRemovalTarget]; v != "" {
			m.Deprecated = true
			m.RemovalTarget = v
		}
		m.ReplacedBy = cmd.Annotations[annReplacedBy]
		m.SinceVersion = cmd.Annotations[annSince]
		m.SpecCommand = cmd.Annotations[annSpecCommand] == "true"
	}
	if cfg.reserved != nil {
		m.Reserved = cfg.reserved.IsReserved(depthOneName(cmd, root))
	}
	return m
}

// reflectSafety resolves the risk profile.
func reflectSafety(cmd *cobra.Command) Safety {
	s := Safety{Network: toolspec.PermNetworkNone}
	ann := cmd.Annotations

	raw := ""
	if ann != nil {
		raw = ann[annSideEffect]
	}
	s.DeclaredSideEffect = raw
	s.Tier = resolveTier(raw)

	// An annotation that does not resolve is a defect, not a
	// default: leave the tier unknown so decide() can report it.
	// Only an ABSENT annotation falls through to the heuristic.
	if s.Tier == TierUnknown && raw == "" {
		if destructiveNames[cmd.Name()] {
			s.Tier = TierDestructiveShared
			s.TierInferred = true
		} else {
			s.Tier = TierRead
		}
	}

	s.Level = safetyLevel(s.Tier)
	if ann != nil {
		s.Network = resolveNetwork(ann[annNetwork])
		s.Idempotent = ann[annIdempotent]
		s.AuthRequired = ann[annAuthRequired] == "true"
		s.ExitCodes = splitCSV(ann[annExitCodes])
		s.DryRunRationale = ann[annDryRunRationale]
		if v := ann[annDestructiveToken]; v == "required" || v == "true" {
			s.DestructiveTokenRequired = true
		}
	}

	s.Permissions = []toolspec.Permission{fsPermission(s.Tier), s.Network}
	if ann != nil {
		if ann[annExec] != "" {
			s.Permissions = append(s.Permissions, toolspec.PermExecSubprocess)
		}
		if ann[annBusPublish] != "" {
			s.Permissions = append(s.Permissions, toolspec.PermBusPublish)
		}
	}

	// Confirmation is required when the command destroys, when it
	// reaches a private network or listens on one, when the
	// adopter said so explicitly, or when the destructive-name
	// heuristic fired. The heuristic clause is kept separate from
	// the tier clause on purpose: it must keep forcing
	// confirmation even if a future ladder change stops mapping
	// the heuristic onto a destructive tier.
	s.RequiresConfirmation = s.Destructive() ||
		s.Network == toolspec.PermNetworkEgressPrivate ||
		s.Network == toolspec.PermNetworkIngress ||
		s.TierInferred
	if ann != nil && ann[annRequiresConfirm] == "true" {
		s.ConfirmationDeclared = true
		s.RequiresConfirmation = true
	}

	s.Retryable = cmdmeta.IsRetryable(cmd)
	s.DryRunSupported = cmdmeta.IsDryRunSupported(cmd)
	return s
}

// reflectOutput resolves the structured-output metadata. A declared
// schema that is not valid JSON sets SchemaMalformed; the bytes are
// still carried so a diagnostic can show what was written.
func reflectOutput(cmd *cobra.Command) OutputMeta {
	var o OutputMeta
	if raw, ver, ok := cmdmeta.GetOutputSchemaJSON(cmd); ok && len(raw) > 0 {
		o.Schema = append([]byte(nil), raw...)
		o.SchemaVersion = ver
		if !json.Valid(raw) {
			o.SchemaMalformed = true
		}
	}
	if ex, ok := cmdmeta.GetExamples(cmd); ok {
		o.Examples = make([]Example, 0, len(ex))
		for _, e := range ex {
			o.Examples = append(o.Examples, Example{
				Title: e.Title, Command: e.Command, Output: e.Output,
			})
		}
	}
	if ns, ok := cmdmeta.GetNextSteps(cmd); ok {
		o.NextSteps = make([]NextStep, 0, len(ns))
		for _, n := range ns {
			o.NextSteps = append(o.NextSteps, NextStep{
				When: n.When, Suggest: n.Suggest, Reason: n.Reason,
			})
		}
	}
	return o
}

// decide sets Invocable and Reason from the already-resolved facts.
// Every rule that fires is collected, then [pickReason] picks the
// most specific, so the reported reason does not depend on the
// order the rules are written in here.
func decide(d *Descriptor, cfg *config) {
	fired := map[NonInvocableReason]bool{}

	if !d.Surface.Runnable {
		fired[ReasonNotRunnable] = true
	}
	if d.Surface.Builtin {
		fired[ReasonBuiltin] = true
	}
	if d.Safety.Tier == TierUnknown || d.Output.SchemaMalformed {
		fired[ReasonMalformedSchema] = true
	}
	if d.Surface.Hidden && !cfg.allowHidden {
		fired[ReasonHiddenInternal] = true
	}
	if d.Surface.Deprecated && !cfg.allowDeprecated {
		fired[ReasonDeprecated] = true
	}
	if d.Safety.Tier == TierInteractive && !cfg.allowInteractive {
		fired[ReasonInteractive] = true
	}
	if (d.Surface.Reserved || d.Surface.SpecCommand) && !cfg.allowReserved {
		fired[ReasonManagementOnly] = true
	}
	// No option lifts self-hosting. The relaxations exist so a
	// consumer can reflect for the surface it actually has — a
	// terminal lifts interactive, the spec lifts reserved — and no
	// projected surface ever has "is the process itself".
	if d.Surface.SelfHosting {
		fired[ReasonSelfHosting] = true
	}
	if d.Safety.Destructive() {
		switch {
		case cfg.destructiveDenyFunc != nil:
			if cfg.destructiveDenyFunc(d) {
				fired[ReasonUnauthorizedDestructive] = true
			}
		case cfg.destructiveDenied:
			fired[ReasonUnauthorizedDestructive] = true
		}
	}

	d.Reason = pickReason(fired)
	d.Invocable = d.Reason == ReasonNone
}

// isBuiltin reports a cobra or fang framework command: the help and
// completion trees, man, and the shell-integration hooks.
func isBuiltin(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case "help", "completion", "man", "__complete", "__completeNoDesc":
		return true
	}
	if p := cmd.Parent(); p != nil && p.Name() == "completion" {
		return true
	}
	return false
}

// depthOneName returns the name of cmd's depth-1 ancestor under
// root — for "mytool foo bar baz" that is "foo". A depth-1 command
// returns its own name; the root returns "".
func depthOneName(cmd *cobra.Command, root *cobra.Command) string {
	if cmd == nil || root == nil || cmd == root {
		return ""
	}
	cur := cmd
	for cur.Parent() != nil && cur.Parent() != root {
		cur = cur.Parent()
	}
	return cur.Name()
}
