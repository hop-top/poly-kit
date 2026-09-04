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

// Side-effect classes recognised by the projection. They mirror the
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

// MethodFor returns the HTTP method a command of class c is projected
// onto.
//
// Read commands become GET; everything else becomes POST.
//
// The rule is deliberately coarse. A finer mapping — PUT for
// idempotent writes, DELETE for destructive ones — reads better in
// isolation but cannot be honoured here: kit's declared vocabulary
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

	// RequiresConfirmation reports that the command is gated on a
	// confirmation token.
	RequiresConfirmation bool `json:"requires_confirmation,omitempty"`
	// AuthRequired reports that the command declares
	// kit/auth-required.
	AuthRequired bool `json:"auth_required,omitempty"`
}

// Method returns the HTTP method this descriptor projects onto.
func (d CommandDescriptor) Method() string { return MethodFor(d.SideEffect) }

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
