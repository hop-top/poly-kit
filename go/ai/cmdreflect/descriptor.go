package cmdreflect

import (
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/toolspec"
)

// Descriptor is everything kit knows about one command. It is the
// single reflection product: spec generation, OpenAPI, MCP, and the
// command transports all read this type rather than the cobra tree.
//
// A Descriptor exists for every command in the source tree,
// including the ones no consumer may invoke. Invocable and Reason
// carry that verdict; see [NonInvocableReason].
type Descriptor struct {
	// Path is the command path from the root, root segment
	// included (e.g. ["mytool", "widget", "add"]). It matches
	// cobra.Command.CommandPath split on spaces.
	Path []string

	// Use is cobra.Command.Use verbatim, including any positional
	// placeholders the adopter wrote.
	Use string
	// Short is the one-line description.
	Short string
	// Long is the multi-line description.
	Long string
	// Aliases are the command's alternate names.
	Aliases []string

	// Flags are the flags declared directly on this command.
	// Persistent flags inherited from ancestors are not repeated
	// here; read them from the ancestor's descriptor, or from
	// Tree.GlobalFlags for the root's.
	Flags []Flag
	// Args are the declared positional arguments, from the
	// kit/args annotation. Cobra does not introspect argument
	// names, so a command that declares none has an empty slice
	// even when it accepts positionals.
	Args []Arg

	// Safety is the resolved risk profile: tier, permissions,
	// confirmation.
	Safety Safety
	// Surface is the presentation and transport metadata.
	Surface SurfaceMeta
	// Output describes the command's structured output when the
	// adopter declared a schema.
	Output OutputMeta

	// Invocable reports whether a consumer may project this
	// command onto a surface and call it. False implies Reason is
	// set.
	Invocable bool
	// Reason names the rule that set Invocable false. ReasonNone
	// when Invocable is true.
	Reason NonInvocableReason

	// Cmd is the source cobra command. Consumers that must reach
	// back to the tree — to run the command, or to read an
	// annotation this package does not model — use this rather
	// than walking from the root again.
	Cmd *cobra.Command
}

// PathKey returns the path below the root joined by spaces, the
// form cmdsurface uses as a leaf key ("widget add"). The root
// segment is dropped because a transport addresses commands
// relative to the tool, not including its binary name.
func (d *Descriptor) PathKey() string {
	if len(d.Path) <= 1 {
		return ""
	}
	return strings.Join(d.Path[1:], " ")
}

// IsRoot reports whether d describes the root command itself.
func (d *Descriptor) IsRoot() bool { return len(d.Path) <= 1 }

// Flag describes one declared flag.
type Flag struct {
	// Name is the long name without the leading dashes.
	Name string
	// Short is the single-character shorthand, empty when none.
	Short string
	// Type is the pflag value type ("string", "bool", "int", …).
	Type string
	// Description is the usage string.
	Description string
	// Default is the default value rendered as pflag prints it.
	Default string
	// Required reports whether cobra marks the flag required
	// (MarkFlagRequired sets the BannerRequired annotation).
	Required bool
	// Hidden reports whether the flag is hidden from help.
	Hidden bool
	// Deprecated reports whether the flag carries a deprecation
	// message. pflag hides deprecated flags implicitly, so a
	// deprecated flag is reflected with both fields set.
	Deprecated bool
	// SinceVersion is the schema version in which the flag first
	// appeared, from the command's kit/flag-since annotation.
	SinceVersion string
}

// Arg describes one declared positional argument.
type Arg struct {
	// Name is the argument name as written in kit/args, with any
	// optional marker stripped.
	Name string
	// Required is false when the kit/args entry ended in "?".
	Required bool
}

// Safety is the resolved risk profile of a command, expressed in
// the vocabulary [hop.top/kit/go/ai/toolspec] defines. This package
// resolves annotations into that vocabulary rather than inventing a
// parallel one.
type Safety struct {
	// Tier is the resolved position on the six-tier side-effect
	// ladder. Always set: a command with no annotation resolves to
	// TierRead unless the destructive-name heuristic fires.
	Tier Tier
	// DeclaredSideEffect is the raw kit/side-effect annotation
	// value, preserved so consumers can echo what the adopter
	// wrote rather than kit's normalization of it. Empty when the
	// annotation is absent.
	DeclaredSideEffect string
	// TierInferred reports that Tier came from the
	// destructive-name heuristic rather than an annotation. A
	// consumer that wants to trust only declared metadata checks
	// this.
	TierInferred bool

	// Level is the legacy three-value projection of Tier, kept so
	// existing toolspec consumers keep working unchanged.
	Level toolspec.SafetyLevel
	// Network is the resolved network permission from
	// kit/network. Defaults to kit:network:none.
	Network toolspec.Permission
	// Permissions is the full permission token set: exactly one
	// kit:fs:*, exactly one kit:network:*, plus any capability
	// tokens the annotations enabled.
	Permissions []toolspec.Permission

	// RequiresConfirmation is the RESOLVED confirmation verdict:
	// true when the command destroys, when it reaches or listens
	// on a private network, when the destructive-name heuristic
	// fired, or when the adopter declared
	// kit/requires-confirmation. This is the answer a spec
	// consumer wants — "will a human be asked before this runs".
	RequiresConfirmation bool
	// ConfirmationDeclared is the narrow fact: the adopter set
	// kit/requires-confirmation explicitly. Kept separate from the
	// resolved verdict because a transport that already gates
	// destructiveness through its own policy needs to know whether
	// the adopter asked for a confirmation CHANNEL on top of that,
	// which is a different question from whether the command is
	// dangerous.
	ConfirmationDeclared bool
	// DestructiveTokenRequired reports kit/destructive-token.
	DestructiveTokenRequired bool
	// AuthRequired reports kit/auth-required.
	AuthRequired bool
	// Idempotent is the raw kit/idempotent value
	// (yes|no|conditional), empty when unset.
	Idempotent string
	// Retryable reports whether the command is safely re-runnable.
	Retryable bool
	// DryRunSupported reports whether kit installs a --dry-run
	// flag for this command.
	DryRunSupported bool
	// DryRunRationale is the adopter's explanation when dry-run
	// was opted out of.
	DryRunRationale string
	// ExitCodes is the declared exit-code symbol set.
	ExitCodes []string
}

// PermissionStrings returns Permissions as plain strings, the form
// toolspec.Safety.Permissions takes.
func (s Safety) PermissionStrings() []string {
	if len(s.Permissions) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Permissions))
	for _, p := range s.Permissions {
		out = append(out, p.String())
	}
	return out
}

// Destructive reports whether the resolved tier is either
// destructive band. Mirrors the predicate the cmdsurface policy
// gate applies.
func (s Safety) Destructive() bool {
	return s.Tier == TierDestructiveLocal || s.Tier == TierDestructiveShared
}

// SurfaceMeta is the presentation and transport metadata a
// consumer needs to decide where a command belongs.
type SurfaceMeta struct {
	// Hidden reports cobra.Command.Hidden.
	Hidden bool
	// Deprecated reports whether any deprecation marker is set.
	Deprecated bool
	// DeprecatedSince is the version the command was deprecated
	// in, from cobra's Deprecated string or kit/deprecated-since
	// (the annotation wins when both are present).
	DeprecatedSince string
	// RemovalTarget is the kit/removal-target annotation.
	RemovalTarget string
	// ReplacedBy is the kit/replaced-by annotation.
	ReplacedBy string
	// SinceVersion is the kit/since annotation.
	SinceVersion string

	// Builtin reports a cobra or fang framework command.
	Builtin bool
	// SpecCommand reports the kit/spec-command annotation.
	SpecCommand bool
	// Reserved reports whether the command's depth-1 ancestor is a
	// kit-reserved verb.
	Reserved bool
	// Runnable reports whether the command has a Run or RunE.
	Runnable bool
	// HasSubCommands reports whether the command has children.
	HasSubCommands bool

	// TopLevelVerb reports kit/top-level-verb.
	TopLevelVerb bool
	// Hierarchical reports kit/hierarchical.
	Hierarchical bool
	// Passthrough reports kit/passthrough.
	Passthrough bool

	// Group is the cobra command group the command belongs to,
	// empty when ungrouped.
	Group string
}

// OutputMeta describes a command's structured output.
type OutputMeta struct {
	// Schema is the adopter-declared JSON Schema bytes, nil when
	// none was declared.
	Schema []byte
	// SchemaVersion is the MAJOR.MINOR version paired with Schema.
	SchemaVersion string
	// SchemaMalformed reports that a schema was declared but did
	// not parse as JSON. The descriptor is non-invocable with
	// ReasonMalformedSchema when this is set.
	SchemaMalformed bool
	// Examples are the adopter-declared agent-facing examples.
	Examples []Example
	// NextSteps are the declared post-invocation suggestions.
	NextSteps []NextStep
}

// Example mirrors cli.Example.
type Example struct {
	Title   string
	Command string
	Output  string
}

// NextStep mirrors cli.NextStep.
type NextStep struct {
	When    string
	Suggest string
	Reason  string
}
