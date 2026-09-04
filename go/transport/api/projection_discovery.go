package api

import (
	"net/http"
	"sort"
)

// DiscoveryDocument is the body served at the projection prefix. It
// lists EVERY command in the tree, mounted or not, so a caller can
// tell "no such command" from "that command exists and is withheld
// here" without probing routes and interpreting 404s.
type DiscoveryDocument struct {
	// Tool is the adopter's tool name.
	Tool string `json:"tool,omitempty"`
	// Version is the adopter's tool version.
	Version string `json:"version,omitempty"`
	// Prefix is the versioned mount point every route sits under.
	Prefix string `json:"prefix"`
	// Commands is every reflected command, invocable first, then
	// non-invocable, each group in path order.
	Commands []DiscoveryEntry `json:"commands"`
	// Reasons is the closed vocabulary of non-invocable reasons
	// that appear in this document, so a client can render a
	// legend without hard-coding the set.
	Reasons []string `json:"reasons,omitempty"`
	// ExitStatus is the exit-code-to-HTTP-status mapping the
	// projection applies, published so a caller can predict a
	// status without reading the documentation.
	ExitStatus []ExitStatusPair `json:"exit_status"`
}

// DiscoveryEntry is one command in the discovery listing.
type DiscoveryEntry struct {
	// Path is the command path below the root.
	Path []string `json:"path"`
	// Name is the space-joined path, the form a human types.
	Name string `json:"name"`
	// Summary is the one-line description.
	Summary string `json:"summary,omitempty"`
	// SideEffect is the resolved side-effect tier.
	SideEffect SideEffectClass `json:"side_effect"`

	// Invocable reports whether the command is mounted.
	Invocable bool `json:"invocable"`
	// Reason names why it is not, empty when it is.
	Reason string `json:"reason,omitempty"`

	// Method is the HTTP method, empty for a non-invocable
	// command: publishing a method for a route that does not exist
	// would invite a call that can only 404.
	Method string `json:"method,omitempty"`
	// Route is the projected path, empty for a non-invocable
	// command.
	Route string `json:"route,omitempty"`

	// Flags are the command's declared flags.
	Flags []CommandFlag `json:"flags,omitempty"`
	// Args are the command's declared positional arguments.
	Args []CommandArg `json:"args,omitempty"`

	// RequiresConfirmation reports that a call must carry the
	// confirmation header.
	RequiresConfirmation bool `json:"requires_confirmation,omitempty"`
	// AuthRequired reports that the command declares
	// kit/auth-required.
	AuthRequired bool `json:"auth_required,omitempty"`
}

// BuildDiscoveryDocument assembles the discovery body from cfg. It is
// exported so a test, or an adopter serving the listing somewhere
// else, can build the same document the endpoint serves.
func BuildDiscoveryDocument(cfg ProjectionConfig) DiscoveryDocument {
	doc := DiscoveryDocument{
		Tool:       cfg.ToolName,
		Version:    cfg.ToolVersion,
		Prefix:     CommandProjectionPrefix,
		Commands:   make([]DiscoveryEntry, 0, len(cfg.Descriptors)),
		ExitStatus: ExitStatusTable(),
	}

	seen := map[string]bool{}
	var reasons []string
	for _, d := range cfg.Descriptors {
		e := DiscoveryEntry{
			Path:                 d.Path,
			Name:                 d.PathKey(),
			Summary:              d.Summary,
			SideEffect:           d.SideEffect,
			Invocable:            d.Invocable,
			Reason:               d.Reason,
			Flags:                d.sortedFlags(),
			Args:                 d.Args,
			RequiresConfirmation: d.RequiresConfirmation,
			AuthRequired:         d.AuthRequired,
		}
		// Method and Route describe a route that exists. A
		// non-invocable command has none, and saying otherwise
		// would advertise a call that can only fail.
		if d.Invocable {
			e.Method = d.Method()
			e.Route = d.Route()
		} else if d.Reason != "" && !seen[d.Reason] {
			seen[d.Reason] = true
			reasons = append(reasons, d.Reason)
		}
		doc.Commands = append(doc.Commands, e)
	}

	// Invocable first: the common case is "what can I call", and a
	// caller scanning the list should reach the answer before the
	// exclusions. Within each group, path order is preserved so the
	// listing mirrors the command tree.
	sort.SliceStable(doc.Commands, func(i, j int) bool {
		return doc.Commands[i].Invocable && !doc.Commands[j].Invocable
	})

	sort.Strings(reasons)
	doc.Reasons = reasons
	return doc
}

// discoveryHandler serves the discovery document.
func discoveryHandler(cfg ProjectionConfig) http.HandlerFunc {
	doc := BuildDiscoveryDocument(cfg)
	return func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, doc)
	}
}
