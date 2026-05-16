// mcp_enforce.go implements the policy-gate building block ADR-0019
// expects MCP hosts to invoke when they dispatch a tool-call request
// against a kit-powered binary. The existing mcp.go FormatAdapter
// emits the schema envelope; mcp_enforce.go covers the *runtime*
// side — given a Manifest + the requested command path, look up the
// command's tier+network metadata and resolve via the policy table.
//
// Hosts integrate this in two lines:
//
//	envelope := adapters.EnforceMCPRequest(manifest, path, policy.Default())
//	switch envelope.Decision.Action {
//	case policy.ActionAutoAllow:
//	    // proceed silently
//	case policy.ActionPrompt:
//	    // ask the user; envelope.Decision.Reason is the rationale
//	case policy.ActionDeny:
//	    // refuse; surface envelope.Error to the caller
//	}
//
// The function is intentionally pure: no I/O, no global state. The
// host decides how to render the prompt or carry the deny back over
// the wire (the MCP error envelope below is provided as a
// convenience for hosts that emit JSON-RPC errors verbatim).
//
// # Stub for kit-toolspec-safety-ladder
//
// Today the manifest carries a single side-effect string per
// command and no network axis. Until the safety-ladder track lands
// the kit/network annotation, EnforceMCPRequest treats every
// command as network=none and resolves on side_effect alone. The
// resolved Decision.Reason includes a "(network axis: stub)"
// suffix so hosts can surface the limitation. Rich
// Safety.Permissions string vocabulary will plug in here once the
// ladder track populates the field.

package adapters

import (
	"fmt"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/policy"
)

// EnforcementEnvelope is the decision envelope a host returns from
// the gate. Action mirrors policy.Action; Error is populated only
// when Action == policy.ActionDeny so JSON-RPC consumers can
// marshal it verbatim into an MCP error.
type EnforcementEnvelope struct {
	// Decision is the resolved policy decision (action + reason +
	// source-rule attribution).
	Decision policy.Decision
	// Path is the command path the gate evaluated, echoed back so
	// audit logs can correlate the decision with the request.
	Path []string
	// Error is the MCP error envelope to return when the action is
	// deny. Nil for auto-allow / prompt.
	Error *MCPError
}

// MCPError is the JSON-RPC-shaped error envelope the host marshals
// back to the harness when a tool call is denied. Code is a stable
// kit-side error code; Message is the user-visible reason; Data
// includes the structured policy decision for richer client UIs.
type MCPError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// MCPErrorCodePolicyDeny is the JSON-RPC error code kit emits when
// the policy table denies a tool call. Inside the JSON-RPC
// "application defined" range (-32099..-32000); 32099 is reserved
// for kit/policy/deny. Hosts that already use 32099 should
// translate.
const MCPErrorCodePolicyDeny = -32099

// EnforceMCPRequest resolves the policy decision for a tool-call
// request against the supplied manifest and policy table. The
// command path MUST match a Manifest.Commands[i].Path slice
// (segment-equal); a missing path resolves to ActionDeny with a
// "command not in manifest" reason — refusing to execute commands
// the manifest doesn't advertise is the safe default.
//
// Resolution:
//
//  1. Walk manifest.Commands looking for a path-equal entry.
//     Path comparison is case-sensitive and segment-strict.
//  2. Read the leaf's SideEffect; default network is NetworkNone
//     until kit-toolspec-safety-ladder lands kit/network.
//  3. Call table.Resolve(side_effect, network).
//  4. Wrap the Decision into an EnforcementEnvelope; populate
//     MCPError when the action is deny.
//
// Pure: no I/O, no global state. Safe to call from any goroutine.
func EnforceMCPRequest(manifest toolspec.Manifest, path []string, table policy.Table) EnforcementEnvelope {
	leaf := findLeafByPath(manifest, path)
	if leaf == nil {
		return EnforcementEnvelope{
			Path: path,
			Decision: policy.Decision{
				Action: policy.ActionDeny,
				Reason: fmt.Sprintf("command path %v not advertised in manifest", path),
				Source: "mcp-enforce/missing",
			},
			Error: &MCPError{
				Code:    MCPErrorCodePolicyDeny,
				Message: fmt.Sprintf("command %v not advertised in manifest", path),
				Data: map[string]any{
					"reason": "command-not-advertised",
					"path":   path,
				},
			},
		}
	}
	se := policy.SideEffect(leaf.SideEffect)
	if se == "" {
		// Manifest entry without a kit/side-effect annotation: treat
		// as the most-restrictive class (destructive) so unannotated
		// commands fail safe rather than auto-allow.
		se = policy.SideEffectDestructive
	}
	net := networkAxisFor(leaf)

	d := table.Resolve(se, net)
	// Suffix the reason with a note when the network axis is stubbed
	// (kit/network annotation not yet populated by the safety-ladder
	// track). This makes the limitation visible in audit logs.
	if net == policy.NetworkNone && !leafHasNetworkAnnotation(leaf) {
		d.Reason = fmt.Sprintf("%s (network axis: stub, defaulting to none until kit-toolspec-safety-ladder lands)", d.Reason)
	}

	env := EnforcementEnvelope{Decision: d, Path: path}
	if d.Action == policy.ActionDeny {
		env.Error = &MCPError{
			Code:    MCPErrorCodePolicyDeny,
			Message: d.Reason,
			Data: map[string]any{
				"reason":      "policy-deny",
				"path":        path,
				"side_effect": string(se),
				"network":     string(net),
				"rule_source": d.Source,
			},
		}
	}
	return env
}

// findLeafByPath returns the manifest command with a segment-equal
// Path slice, or nil if no entry matches. The MCP host is expected
// to pass the full path including the binary name as segment 0
// (matches the manifest's Path encoding).
func findLeafByPath(m toolspec.Manifest, path []string) *toolspec.ManifestCommand {
	for i := range m.Commands {
		if pathsEqual(m.Commands[i].Path, path) {
			return &m.Commands[i]
		}
	}
	return nil
}

// pathsEqual is segment-strict equality on the manifest path
// representation.
func pathsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// networkAxisFor reads the network axis for a manifest command.
// Today the field doesn't exist on ManifestCommand (kit-toolspec-
// safety-ladder will add it as ManifestCommand.Network or via the
// Safety.Permissions block); this helper returns NetworkNone
// pending that work, so the policy resolver has a usable input.
//
// When the safety-ladder track lands, replace the body with the
// real annotation read; the function signature stays.
func networkAxisFor(_ *toolspec.ManifestCommand) policy.Network {
	// Stub: see ADR-0019 §4 ("Integration with kit-toolspec-safety-
	// ladder"). Until kit/network is populated, every command
	// resolves at NetworkNone. This is documented behavior, NOT a
	// silent default — the EnforceMCPRequest reason field calls it
	// out per call.
	return policy.NetworkNone
}

// leafHasNetworkAnnotation reports whether the manifest command
// already carries a network-axis annotation. Returns false today;
// flips on naturally once the safety-ladder track adds the field
// to ManifestCommand and we read it in networkAxisFor.
func leafHasNetworkAnnotation(_ *toolspec.ManifestCommand) bool {
	return false
}
