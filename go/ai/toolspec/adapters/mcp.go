// mcp implements the FormatAdapter for the Model Context Protocol
// tool definition shape. MCP is the convention LLM clients (Claude
// Desktop, Cursor, MCP-compatible IDEs) consume to learn what tools
// they can call. Each kit-built CLI registered as an MCP tool gets
// exactly one MCP envelope; the cobra command tree projects into
// the envelope's inputSchema.
//
// MCP envelope shape (from the official spec):
//
//	{
//	  "name": "<tool>",
//	  "description": "<one-line>",
//	  "inputSchema": {
//	    "type": "object",
//	    "properties": { ... },
//	    "required": [ ... ]
//	  }
//	}
//
// We map the tool's top-level commands to a single "action" enum
// property whose values are the visible command leaves; flags become
// additional inputSchema properties. This matches the convention
// tlc's `tlc prompt` already established and what existing MCP
// tooling expects.
//
// Aliased as "prompt" so tools migrating from tlc's prior
// `tlc prompt` UX can publish `tlc spec --format prompt` and have
// it resolve here transparently.

package adapters

import (
	"encoding/json"
	"fmt"
	"io"

	"hop.top/kit/go/ai/toolspec"
)

const (
	// mcpName is the canonical adapter name.
	mcpName = "mcp"
	// CustomKeyMCPDescription is the RenderConfig.Custom key the
	// MCP adapter consults to override the auto-derived
	// description (default: "<tool> CLI tool"). Adopters set it to
	// their tool's one-line description for cleaner MCP listings.
	CustomKeyMCPDescription = "mcp:description"
	// CustomKeyMCPRequiredFlags is the RenderConfig.Custom key the
	// MCP adapter consults for additional required-flag names
	// beyond "action". Value: []string. Tools that have
	// always-required flags can declare them here so MCP clients
	// know they must be provided.
	CustomKeyMCPRequiredFlags = "mcp:required-flags"
)

// mcpAdapter implements FormatAdapter for MCP tool definitions.
type mcpAdapter struct{}

// MCP returns the MCP adapter. Stateless; safe to share across
// goroutines.
func MCP() FormatAdapter {
	return mcpAdapter{}
}

// Name implements FormatAdapter.
func (mcpAdapter) Name() string { return mcpName }

// Aliases implements FormatAdapter. "prompt" is the back-compat
// alias for tlc-derived workflows (`tlc prompt` → `tlc spec --format
// prompt`).
func (mcpAdapter) Aliases() []string { return []string{"prompt"} }

// Description implements FormatAdapter.
func (mcpAdapter) Description() string {
	return "Model Context Protocol tool definition (for Claude Desktop, Cursor, MCP IDEs)"
}

// ContentType implements FormatAdapter.
func (mcpAdapter) ContentType() string { return "application/json" }

// Render emits an MCP tool envelope as JSON. Always emits indented
// JSON when cfg.Pretty (the default); compact otherwise.
func (a mcpAdapter) Render(w io.Writer, spec *toolspec.ToolSpec, opts ...RenderOption) error {
	if spec == nil {
		return fmt.Errorf("mcp: nil spec")
	}
	cfg := ResolveRenderOptions(opts)

	envelope := buildMCPEnvelope(spec, cfg)

	enc := json.NewEncoder(w)
	if cfg.Pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(envelope)
}

// buildMCPEnvelope builds the MCP tool envelope from a curated spec.
// Generic — no tool-specific enums, examples, or hardcoded
// descriptions; everything that varies per tool comes from the spec
// itself or from RenderConfig.Custom.
func buildMCPEnvelope(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	desc := mcpDescription(spec, cfg)
	return map[string]any{
		"name":        spec.Name,
		"description": desc,
		"inputSchema": buildMCPInputSchema(spec, cfg),
	}
}

// mcpDescription returns the description string for the MCP envelope.
// Priority: Custom["mcp:description"] override → "<tool> CLI tool"
// fallback. Adopters always set the Custom key for their
// one-line description; the fallback exists so an unregistered tool
// still produces a valid envelope.
func mcpDescription(spec *toolspec.ToolSpec, cfg *RenderConfig) string {
	if v, ok := cfg.Custom[CustomKeyMCPDescription].(string); ok && v != "" {
		return v
	}
	return spec.Name + " CLI tool"
}

// buildMCPInputSchema builds the JSON Schema "object" envelope for
// the inputSchema field. The schema has a single "action" property
// (enum of visible top-level command names) plus one property per
// flag declared at the root.
func buildMCPInputSchema(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	props := buildMCPProperties(spec, cfg)
	required := []string{"action"}
	if extra, ok := cfg.Custom[CustomKeyMCPRequiredFlags].([]string); ok {
		required = append(required, extra...)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// buildMCPProperties builds the JSON Schema properties map. Includes
// an "action" property derived from spec.Commands and one property
// per flag in spec.Flags.
//
// Deprecated commands are excluded from the action enum unless
// cfg.IncludeDeprecated; deprecated flags are likewise filtered.
func buildMCPProperties(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	props := make(map[string]any)

	actions := visibleCommandNames(spec.Commands, cfg)
	if len(actions) > 0 {
		props["action"] = map[string]any{
			"type":        "string",
			"description": "The action to perform.",
			"enum":        actions,
		}
	} else {
		props["action"] = map[string]any{
			"type":        "string",
			"description": "The action to perform.",
		}
	}

	for _, f := range spec.Flags {
		if f.Deprecated && !cfg.IncludeDeprecated {
			continue
		}
		props[f.Name] = mcpFlagProperty(f)
	}
	return props
}

// visibleCommandNames returns the leaf-name list of every top-level
// command, filtered by deprecation per cfg. Deprecated commands
// drop unless cfg.IncludeDeprecated.
func visibleCommandNames(cmds []toolspec.Command, cfg *RenderConfig) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if c.Deprecated && !cfg.IncludeDeprecated {
			continue
		}
		out = append(out, c.Name)
	}
	return out
}

// mcpFlagProperty maps one toolspec.Flag to a JSON Schema property
// object. Maps Go-side type strings (pflag's f.Value.Type()) to
// JSON Schema types as best we can; unknown types fall through as
// "string" since that's the safest MCP-side default.
func mcpFlagProperty(f toolspec.Flag) map[string]any {
	prop := map[string]any{
		"type":        mcpJSONType(f.Type),
		"description": f.Description,
	}
	// Array types: declare the items type so MCP clients that
	// validate against the schema accept lists of strings.
	if mcpJSONType(f.Type) == "array" {
		prop["items"] = map[string]string{"type": "string"}
	}
	return prop
}

// mcpJSONType maps a pflag type string to the corresponding JSON
// Schema primitive. The mapping is deliberately conservative:
// unknown types collapse to "string" since MCP clients tolerate
// stringly-typed args universally; collapsing to "string" is far
// safer than asserting an unknown type the validator would reject.
func mcpJSONType(pflagType string) string {
	switch pflagType {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"count":
		return "integer"
	case "float32", "float64":
		return "number"
	case "stringArray", "stringSlice", "intSlice", "boolSlice":
		return "array"
	default:
		return "string"
	}
}

// Compile-time interface assertion.
var _ FormatAdapter = mcpAdapter{}
