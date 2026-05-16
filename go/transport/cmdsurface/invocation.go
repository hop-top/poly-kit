package cmdsurface

import (
	"fmt"
	"strings"
	"time"
)

// Invocation is a single addressable command call, transport-agnostic.
// Surface implementations decode their wire format into this shape and
// hand it to a Runner via the Bridge.
type Invocation struct {
	// Path is the cobra command path from root to leaf, e.g.
	// ["widget","add"]. Empty Path selects the root.
	Path []string `json:"path"`
	// Args are positional arguments passed after the path.
	Args []string `json:"args,omitempty"`
	// Flags is the parsed flag set keyed by long-name. Values are
	// typed as the surface produced them; the Runner normalises to
	// cobra string-flag form at apply time.
	Flags map[string]any `json:"flags,omitempty"`
	// Meta carries caller identity, originating surface, and trace
	// context. Always populate Meta.Surface — the policy gate keys
	// on it.
	Meta Meta `json:"meta"`
}

// Meta carries provenance for an Invocation. Surfaces fill the
// fields they have evidence for; the policy gate and audit sinks
// read what is present.
type Meta struct {
	// Caller is a stable identifier for the originating principal
	// (user id, service account, webhook source). Format is
	// surface-defined; the bridge does not parse it.
	Caller string `json:"caller,omitempty"`
	// Surface is the transport that produced the Invocation. The
	// policy gate refuses Invocations whose Surface is not enabled
	// for the resolved leaf.
	Surface Surface `json:"surface"`
	// TraceID propagates a request/trace identifier across
	// surfaces and sinks. Empty when the surface did not provide
	// one.
	TraceID string `json:"trace_id,omitempty"`
	// RequestedAt records when the surface received the request.
	// Zero value means "unknown / not provided".
	RequestedAt time.Time `json:"requested_at,omitempty"`
	// Extra is a free-form bag for surface-specific context that
	// downstream sinks may consume (HTTP headers, bus message
	// headers, FaaS request id).
	Extra map[string]string `json:"extra,omitempty"`
}

// Result is the unified return value of a Runner.Run. Surfaces map
// the fields onto their wire format (REST status, MCP content, RPC
// response).
type Result struct {
	// ExitCode is the process-style exit status. Zero on success.
	ExitCode int `json:"exit_code"`
	// Stdout is the captured standard-output text.
	Stdout string `json:"stdout,omitempty"`
	// Stderr is the captured standard-error text.
	Stderr string `json:"stderr,omitempty"`
	// Data is an optional structured payload the command produced
	// (when the command writes typed output, e.g. via output.JSON).
	// Surfaces that prefer typed responses prefer Data over Stdout.
	Data any `json:"data,omitempty"`
}

// Event is one frame of a streaming Runner.Stream call. Kind is
// the channel; Data carries the line / payload; At is the wall-clock
// time the event was produced.
//
// Reserved Kind values:
//
//   - "stdout"  — one line written to stdout
//   - "stderr"  — one line written to stderr
//   - "progress" — a structured progress update (Data is payload)
//   - "done"    — terminal event; Data is *Result (or nil on err)
type Event struct {
	Kind string    `json:"kind"`
	Data any       `json:"data,omitempty"`
	At   time.Time `json:"at"`
}

// String returns a single-line representation of inv suitable for
// log entries. Format:
//
//	<surface> <path...> args=[..] flags={..} caller=<id> trace=<id>
//
// Flag values are rendered with %v; secrets are the caller's
// responsibility — the bridge does not redact.
func (inv Invocation) String() string {
	var b strings.Builder
	if inv.Meta.Surface != "" {
		b.WriteString(string(inv.Meta.Surface))
		b.WriteByte(' ')
	}
	if len(inv.Path) == 0 {
		b.WriteString("(root)")
	} else {
		b.WriteString(strings.Join(inv.Path, " "))
	}
	if len(inv.Args) > 0 {
		fmt.Fprintf(&b, " args=%v", inv.Args)
	}
	if len(inv.Flags) > 0 {
		b.WriteString(" flags={")
		first := true
		// Stable order: alphabetical by key.
		keys := sortedKeys(inv.Flags)
		for _, k := range keys {
			if !first {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%s=%v", k, inv.Flags[k])
			first = false
		}
		b.WriteByte('}')
	}
	if inv.Meta.Caller != "" {
		fmt.Fprintf(&b, " caller=%s", inv.Meta.Caller)
	}
	if inv.Meta.TraceID != "" {
		fmt.Fprintf(&b, " trace=%s", inv.Meta.TraceID)
	}
	return b.String()
}

// sortedKeys returns the keys of m sorted lexicographically.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Small maps; insertion sort keeps the cost negligible.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
