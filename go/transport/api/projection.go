package api

import (
	"net/http"
	"sort"
	"strings"
)

// CommandProjectionPrefix is the versioned mount point for projected
// commands. Every projected route and the discovery endpoint live
// under it.
//
// The version segment is part of the path rather than a header or a
// media-type parameter because the projection's shape is derived from
// the adopter's command tree: a tree that gains a required flag
// changes a request schema without the adopter editing a route. A
// path version gives that churn somewhere to land, and lets a future
// /v2 projection be served beside /v1 from one process.
//
// This is a NEW prefix, not a rename. The existing cmdsurface REST
// mount (MountREST, default prefix "/cmd") keeps its shape and its
// POST-with-Invocation-envelope calling convention; it stays the
// explicit, adopter-driven path. The projection is the automatic one.
const CommandProjectionPrefix = "/v1/commands"

// SideEffectClass is the projection's view of a command's resolved
// side-effect tier. The api package does not import the reflection
// package — reflection reaches this package, not the reverse — so
// the caller translates its tier vocabulary into these values.
type SideEffectClass string

// Side-effect classes recognized by the projection. They mirror the
// canonical six-tier ladder, collapsed to the distinctions that
// change an HTTP decision.
const (
	// SideEffectRead makes no observable state change.
	SideEffectRead SideEffectClass = "read"
	// SideEffectWrite mutates state reversibly.
	SideEffectWrite SideEffectClass = "write"
	// SideEffectDestructive mutates state irreversibly.
	SideEffectDestructive SideEffectClass = "destructive"
	// SideEffectInteractive is session-bound and cannot be served
	// by a request/reply transport at all.
	SideEffectInteractive SideEffectClass = "interactive"
)

// SideEffectSource says where a descriptor's SideEffect came from.
//
// The class alone cannot carry it: kit projects a command that
// declared nothing onto write (POST), so without the source a caller
// sees `write` for a declared write and for kit's guess alike, and
// cannot tell which one to trust. Only SideEffectSourceDeclared is
// the adopter's word; every other value is kit's conservative stand-in.
type SideEffectSource string

// Side-effect sources, a closed set.
const (
	// SideEffectSourceDeclared: the adopter's kit/side-effect
	// annotation, resolved as written.
	SideEffectSourceDeclared SideEffectSource = "declared"
	// SideEffectSourceInferred: no annotation; kit's
	// destructive-name heuristic (delete, rm, purge, …) fired.
	SideEffectSourceInferred SideEffectSource = "inferred"
	// SideEffectSourceUnannotated: no annotation and no heuristic.
	// Kit projects it as write — never read.
	SideEffectSourceUnannotated SideEffectSource = "unannotated"
	// SideEffectSourceMalformed: an annotation kit could not
	// resolve. The command is withheld with malformed-schema.
	SideEffectSourceMalformed SideEffectSource = "malformed"
)

// OpenAPI operation extensions carrying the side-effect facts, so a
// client generated from the spec needs no discovery call to learn
// that a POST is kit's guess rather than the adopter's declaration.
const (
	// OpenAPIExtSideEffect carries the SideEffectClass.
	OpenAPIExtSideEffect = "x-kit-side-effect"
	// OpenAPIExtSideEffectSource carries the SideEffectSource.
	OpenAPIExtSideEffectSource = "x-kit-side-effect-source"
)

// MethodFor returns the HTTP method a command of class c is projected
// onto.
//
// Read commands become GET; everything else becomes POST.
//
// The rule is deliberately coarse. A finer mapping — PUT for
// idempotent writes, DELETE for destructive ones — reads better in
// isolation but cannot be honored here: kit's declared vocabulary
// has no notion of a resource identity, so there is no target for
// PUT/DELETE semantics, and a caller who saw DELETE would reasonably
// expect the URL to name the thing being deleted. Two methods keep
// the promise the projection can actually keep: GET is safe and
// cacheable, POST is neither.
//
// Interactive commands are never mounted (they are non-invocable), so
// their appearance here is defensive: they resolve to POST rather
// than to a method that would imply safety.
func MethodFor(c SideEffectClass) string {
	if c == SideEffectRead {
		return http.MethodGet
	}
	return http.MethodPost
}

// RouteFor returns the projected path for a command path below the
// root. Segments join with "/" under the versioned prefix:
//
//	["widget","add"] → /v1/commands/widget/add
//
// An empty path returns the discovery endpoint's own path, which is
// the prefix itself.
func RouteFor(path []string) string {
	if len(path) == 0 {
		return CommandProjectionPrefix
	}
	return CommandProjectionPrefix + "/" + strings.Join(path, "/")
}

// OperationIDFor returns the OpenAPI operationId for a command path.
//
//	["widget","add"] → "commands_widget_add"
//
// Hyphens in a command name become underscores so the id stays a
// valid identifier in generated clients.
func OperationIDFor(path []string) string {
	if len(path) == 0 {
		return "commands_discover"
	}
	joined := strings.Join(path, "_")
	return "commands_" + strings.ReplaceAll(joined, "-", "_")
}

// CommandFlag describes one projected flag. It is the api package's
// own view; the caller fills it from whatever reflection produced.
type CommandFlag struct {
	// Name is the long flag name without leading dashes.
	Name string `json:"name"`
	// Type is the pflag value type ("string", "bool", "int", …).
	Type string `json:"type"`
	// Description is the usage string.
	Description string `json:"description,omitempty"`
	// Default is the default value as pflag renders it.
	Default string `json:"default,omitempty"`
	// Required reports whether the command marks the flag required.
	Required bool `json:"required,omitempty"`
}

// CommandArg describes one projected positional argument.
type CommandArg struct {
	// Name is the declared argument name.
	Name string `json:"name"`
	// Required is false for an argument declared optional.
	Required bool `json:"required,omitempty"`
}

// CommandDescriptor is everything the projection needs about one
// command. It is a transport-neutral projection input: the api
// package defines it so that reflection can depend on api without api
// depending on reflection.
type CommandDescriptor struct {
	// Path is the command path BELOW the root ("widget", "add").
	// The root segment is dropped: a transport addresses commands
	// relative to the tool, not including its binary name.
	Path []string `json:"path"`
	// Summary is the one-line description.
	Summary string `json:"summary,omitempty"`
	// Description is the long description.
	Description string `json:"description,omitempty"`

	// SideEffect is the resolved tier, which selects the method.
	SideEffect SideEffectClass `json:"side_effect"`
	// SideEffectSource says whether SideEffect was declared or is
	// kit's stand-in. Empty is read through [CommandDescriptor.Source].
	SideEffectSource SideEffectSource `json:"side_effect_source,omitempty"`
	// Flags are the flags declared on this command.
	Flags []CommandFlag `json:"flags,omitempty"`
	// Args are the declared positional arguments.
	Args []CommandArg `json:"args,omitempty"`

	// OutputSchema is the adopter-declared JSON Schema for the
	// command's structured output, nil when none was declared.
	OutputSchema []byte `json:"-"`

	// Invocable reports whether the command may be mounted. A
	// non-invocable command is still listed by discovery.
	Invocable bool `json:"invocable"`
	// Reason names the rule that set Invocable false, empty when
	// Invocable is true. The vocabulary is the caller's; the
	// projection treats it as an opaque stable token and publishes
	// it as an enum.
	Reason string `json:"reason,omitempty"`

	// RequiresConfirmation reports that the command is gated on
	// confirmation. A caller satisfies the gate by passing the
	// command's own confirm flag; see [ConfirmFlagsFor].
	RequiresConfirmation bool `json:"requires_confirmation,omitempty"`
	// RequiresConfirmToken reports that the command needs a typed
	// confirm-token in addition to confirm.
	RequiresConfirmToken bool `json:"requires_confirm_token,omitempty"`
	// AuthRequired reports that the command declares
	// kit/auth-required.
	AuthRequired bool `json:"auth_required,omitempty"`
}

// Confirmation flag names. They are the command's OWN flags, the same
// ones the CLI takes, so confirmation means one thing everywhere: the
// projection carries them through and the command's gate decides.
const (
	// ConfirmFlag is the confirmation flag every gated command has.
	ConfirmFlag = "confirm"
	// ConfirmTokenFlag is the typed-token flag a command annotated
	// kit/destructive-token additionally requires.
	ConfirmTokenFlag = "confirm-token"
)

// ConfirmValues are the values ConfirmFlag accepts.
var ConfirmValues = []string{"yes", "no", "auto", "prompt"}

// ConfirmFlagsFor returns the confirmation flags a caller may pass to
// this command, empty when it is not gated.
//
// They are not in the command's declared flag set — kit registers them
// as policy plumbing rather than as command flags — so the projection
// adds them for gated commands only. Everything else keeps rejecting
// undeclared flags.
func ConfirmFlagsFor(d CommandDescriptor) []CommandFlag {
	if !d.RequiresConfirmation && !d.RequiresConfirmToken {
		return nil
	}
	out := []CommandFlag{{
		Name:        ConfirmFlag,
		Type:        "string",
		Description: "Confirmation for this gated command: one of yes, no, auto, prompt.",
	}}
	if d.RequiresConfirmToken {
		out = append(out, CommandFlag{
			Name: ConfirmTokenFlag,
			Type: "string",
			Description: "Typed confirmation token. The refusal message " +
				"for a call without it names the expected value.",
		})
	}
	return out
}

// Method returns the HTTP method this descriptor projects onto.
func (d CommandDescriptor) Method() string { return MethodFor(d.SideEffect) }

// Source returns where SideEffect came from. A descriptor built by
// hand with a class and no source reports declared: the caller stated
// the class, and that statement is the declaration. With no class
// either, it reports unannotated.
func (d CommandDescriptor) Source() SideEffectSource {
	switch {
	case d.SideEffectSource != "":
		return d.SideEffectSource
	case d.SideEffect != "":
		return SideEffectSourceDeclared
	}
	return SideEffectSourceUnannotated
}

// openAPIExtensions returns the operation extensions every projected
// operation carries, in the full and the minimal spec alike.
func (d CommandDescriptor) openAPIExtensions() map[string]any {
	return map[string]any{
		OpenAPIExtSideEffect:       string(d.SideEffect),
		OpenAPIExtSideEffectSource: string(d.Source()),
	}
}

// Route returns the projected path for this descriptor.
func (d CommandDescriptor) Route() string { return RouteFor(d.Path) }

// PathKey returns the space-joined command path, the form the
// cmdsurface bridge uses as a leaf key.
func (d CommandDescriptor) PathKey() string { return strings.Join(d.Path, " ") }

// sortedFlags returns d's flags ordered by name, so a generated spec
// and a discovery listing are byte-stable across runs. Reflection
// walks a map in places; without this the OpenAPI document would
// differ between two identical builds.
func (d CommandDescriptor) sortedFlags() []CommandFlag {
	out := make([]CommandFlag, len(d.Flags))
	copy(out, d.Flags)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
