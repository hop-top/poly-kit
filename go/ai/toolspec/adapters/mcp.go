// mcp implements the FormatAdapter for the Model Context Protocol
// tool definition shape. MCP is the convention LLM clients (Claude
// Desktop, Cursor, MCP-compatible IDEs) consume to learn what tools
// they can call.
//
// # Shape: one tool per leaf command
//
// The adapter emits a `tools` array — one MCP tool descriptor per
// leaf command in the tree — matching byte-for-byte the shape the
// live MCP server emits from `tools/list`
// (go/transport/cmdsurface, buildToolEnvelope):
//
//	{
//	  "tools": [
//	    {
//	      "name": "<tool>.<sub>.<leaf>",
//	      "description": "<one-line>",
//	      "inputSchema": {
//	        "type": "object",
//	        "properties": { ... },
//	        "required": [ ... ]
//	      }
//	    }
//	  ]
//	}
//
// Leaf names are the dotted command path (`widget add` →
// `widget.add`), the description is the command's one-line summary,
// and inputSchema carries that leaf's own flags plus the tool's
// persistent flags. A leaf's `required` list holds the flags cobra
// marks required (MarkFlagRequired), so a client can tell which
// arguments it must supply.
//
// Rendering the same shape both statically and live is the point:
// a client that reads `<tool> spec --format mcp` and a client that
// connects to the same tool's MCP server see the same tool surface.
// The two projections live in different packages (transport cannot
// depend on toolspec, nor the reverse) but are pinned to each other
// by a parity test.
//
// # Back-compat: the single-envelope "action" enum
//
// Before per-leaf, this adapter emitted ONE envelope for the whole
// tool, with top-level commands collapsed into a single "action"
// enum and every flag a sibling property. That shape is still
// reachable — set CustomKeyMCPShape to MCPShapeActionEnum — for
// consumers built against it, including tools that publish
// `<tool> spec --format prompt` via the "prompt" alias, which
// tlc's earlier `tlc prompt` UX established.
//
// It is not the default because the enum shape cannot express a
// per-verb required list: "action" is the only required field by
// construction, so every real argument is necessarily optional, and
// engines that drop unevidenced optional fields silently drop
// stated values. Measured over one CLI's surface with a small
// tool-calling model, both shapes picked the right verb equally
// often (22/30) while per-leaf doubled full-call exactness
// (0.600 vs 0.300).

package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

const (
	// mcpName is the canonical adapter name.
	mcpName = "mcp"
	// CustomKeyMCPDescription is the RenderConfig.Custom key the
	// MCP adapter consults to override the auto-derived
	// description. Under the per-leaf shape it overrides the
	// description of a leaf whose own summary is empty (default:
	// "<tool> CLI tool"); under the action-enum shape it overrides
	// the single envelope's description. Adopters set it to their
	// tool's one-line description for cleaner MCP listings.
	CustomKeyMCPDescription = "mcp:description"
	// CustomKeyMCPRequiredFlags is the RenderConfig.Custom key the
	// MCP adapter consults for additional required-flag names.
	// Value: []string. Tools that have always-required flags can
	// declare them here so MCP clients know they must be provided.
	// Under the per-leaf shape the names are added to every leaf
	// that declares a matching flag; under the action-enum shape
	// they are appended after "action".
	CustomKeyMCPRequiredFlags = "mcp:required-flags"
	// CustomKeyMCPShape is the RenderConfig.Custom key that selects
	// the output shape. Value: an MCPShape. Unset (or
	// MCPShapePerLeaf) emits the per-leaf `tools` array that the
	// live MCP server also emits. MCPShapeActionEnum restores the
	// pre-per-leaf single envelope with an "action" enum, for
	// consumers built against it.
	CustomKeyMCPShape = "mcp:shape"
)

// MCPShape selects which MCP rendering the adapter emits.
type MCPShape string

const (
	// MCPShapePerLeaf emits one tool descriptor per leaf command,
	// wrapped in a {"tools": [...]} array — the same shape the live
	// MCP server returns from tools/list. The default.
	MCPShapePerLeaf MCPShape = "per-leaf"
	// MCPShapeActionEnum emits the historical single envelope whose
	// top-level commands collapse into one "action" enum property.
	// Retained for back-compat; see the package comment for why it
	// is no longer the default.
	MCPShapeActionEnum MCPShape = "action-enum"
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
// prompt`). The alias selects this adapter, not a shape: a consumer
// that needs the old action-enum bytes sets CustomKeyMCPShape to
// MCPShapeActionEnum.
func (mcpAdapter) Aliases() []string { return []string{"prompt"} }

// Description implements FormatAdapter.
func (mcpAdapter) Description() string {
	return "Model Context Protocol tool definitions, one per command (for Claude Desktop, Cursor, MCP IDEs)"
}

// ContentType implements FormatAdapter.
func (mcpAdapter) ContentType() string { return "application/json" }

// Render emits the MCP tool definitions as JSON. Always emits
// indented JSON when cfg.Pretty (the default); compact otherwise.
func (a mcpAdapter) Render(w io.Writer, spec *toolspec.ToolSpec, opts ...RenderOption) error {
	if spec == nil {
		return fmt.Errorf("mcp: nil spec")
	}
	cfg := ResolveRenderOptions(opts)

	var payload any
	switch mcpShape(cfg) {
	case MCPShapeActionEnum:
		payload = buildMCPActionEnumEnvelope(spec, cfg)
	default:
		payload = buildMCPToolList(spec, cfg)
	}

	enc := json.NewEncoder(w)
	if cfg.Pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(payload)
}

// mcpShape resolves the requested output shape, defaulting to
// per-leaf. An unrecognized value also falls through to per-leaf
// rather than erroring: the adapter contract says unknown options
// are ignored, and per-leaf is the shape that agrees with the live
// server.
func mcpShape(cfg *RenderConfig) MCPShape {
	switch v := cfg.Custom[CustomKeyMCPShape].(type) {
	case MCPShape:
		if v == MCPShapeActionEnum {
			return MCPShapeActionEnum
		}
	case string:
		if MCPShape(v) == MCPShapeActionEnum {
			return MCPShapeActionEnum
		}
	}
	return MCPShapePerLeaf
}

// --- Per-leaf shape (default) -------------------------------------

// buildMCPToolList builds the {"tools": [...]} payload: one
// descriptor per leaf command, in command-tree order.
//
// Tool names are the command path BELOW the root — `widget add`
// becomes "widget.add", not "<tool>.widget.add". The root name is
// omitted because the live server omits it (its Leaf paths start at
// the root's children), and an MCP tool name is already scoped by
// the server the client connected to. Prefixing here would publish
// names no live server answers to.
//
// Deprecated commands are excluded unless cfg.IncludeDeprecated;
// deprecated flags are likewise filtered.
func buildMCPToolList(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	tools := make([]map[string]any, 0, len(spec.Commands))
	for _, c := range spec.Commands {
		appendMCPLeafTools(&tools, nil, c, spec, cfg)
	}
	return map[string]any{"tools": tools}
}

// appendMCPLeafTools walks one command and its descendants,
// appending a tool descriptor for every leaf (a command with no
// visible children). parentPath is the path from the root down to
// but not including c.
//
// A command whose children are all filtered out (every child
// deprecated, with cfg.IncludeDeprecated false) becomes a leaf
// itself rather than vanishing: the surface it names is still
// reachable, and dropping it silently would hide it from clients.
func appendMCPLeafTools(out *[]map[string]any, parentPath []string, c toolspec.Command, spec *toolspec.ToolSpec, cfg *RenderConfig) {
	if c.Deprecated && !cfg.IncludeDeprecated {
		return
	}
	path := append(append([]string(nil), parentPath...), c.Name)

	visibleChildren := 0
	for _, child := range c.Children {
		if child.Deprecated && !cfg.IncludeDeprecated {
			continue
		}
		visibleChildren++
	}
	if visibleChildren == 0 {
		*out = append(*out, buildMCPLeafTool(path, c, spec, cfg))
		return
	}
	for _, child := range c.Children {
		appendMCPLeafTools(out, path, child, spec, cfg)
	}
}

// buildMCPLeafTool renders one leaf command as an MCP tool
// descriptor. The name is the dotted path; the inputSchema carries
// the leaf's own flags plus the tool's persistent (root) flags,
// with the leaf's declaration winning on a name collision — the
// same local-over-inherited precedence the live server applies.
func buildMCPLeafTool(path []string, c toolspec.Command, spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	props, required := mcpLeafSchemaFields(c, spec, cfg)

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{
		"name":        mcpToolName(path),
		"description": mcpLeafDescription(c, cfg),
		"inputSchema": schema,
	}
}

// mcpLeafSchemaFields collects the JSON Schema properties and the
// required-name list for one leaf. Local flags are visited first so
// a leaf-local declaration overrides an inherited one of the same
// name; the required list is sorted for stable output.
func mcpLeafSchemaFields(c toolspec.Command, spec *toolspec.ToolSpec, cfg *RenderConfig) (map[string]any, []string) {
	props := make(map[string]any)
	requiredSet := make(map[string]bool)
	seen := make(map[string]bool)

	visit := func(f toolspec.Flag) {
		if f.Deprecated && !cfg.IncludeDeprecated {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		props[f.Name] = mcpFlagProperty(f)
		if f.Required {
			requiredSet[f.Name] = true
		}
	}

	// Local first so locally-overridden flag declarations win.
	for _, f := range c.Flags {
		visit(f)
	}
	for _, f := range spec.Flags {
		visit(f)
	}

	// Adopter-declared always-required flags apply to whichever
	// leaves actually carry the flag; naming a flag a leaf does not
	// declare would publish a requirement the leaf cannot satisfy.
	for _, name := range mcpExtraRequiredFlags(cfg) {
		if seen[name] {
			requiredSet[name] = true
		}
	}

	required := make([]string, 0, len(requiredSet))
	for name := range requiredSet {
		required = append(required, name)
	}
	sort.Strings(required)
	return props, required
}

// mcpLeafDescription returns the description for one leaf: its own
// one-line summary (the live server uses cobra's Short here), or the
// adapter's configured description when the leaf declares none. An
// unannotated leaf with no configured fallback renders an empty
// description rather than a synthesized one — the live server does
// the same, and inventing text would misdescribe the command.
func mcpLeafDescription(c toolspec.Command, cfg *RenderConfig) string {
	if c.Short != "" {
		return c.Short
	}
	if v, ok := cfg.Custom[CustomKeyMCPDescription].(string); ok && v != "" {
		return v
	}
	return ""
}

// mcpToolName renders a leaf path as a dotted MCP tool name,
// matching the live server's toolName ([]string{"widget","add"} →
// "widget.add").
func mcpToolName(path []string) string { return strings.Join(path, ".") }

// --- Action-enum shape (back-compat opt-in) -----------------------

// buildMCPActionEnumEnvelope builds the historical single MCP tool
// envelope: the whole command tree collapsed into one "action" enum
// with flags as sibling properties. Reachable only via
// CustomKeyMCPShape = MCPShapeActionEnum.
func buildMCPActionEnumEnvelope(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	return map[string]any{
		"name":        spec.Name,
		"description": mcpEnvelopeDescription(spec, cfg),
		"inputSchema": buildMCPActionEnumInputSchema(spec, cfg),
	}
}

// mcpEnvelopeDescription returns the description string for the
// action-enum envelope. Priority: Custom["mcp:description"] override
// → "<tool> CLI tool" fallback.
func mcpEnvelopeDescription(spec *toolspec.ToolSpec, cfg *RenderConfig) string {
	if v, ok := cfg.Custom[CustomKeyMCPDescription].(string); ok && v != "" {
		return v
	}
	return spec.Name + " CLI tool"
}

// buildMCPActionEnumInputSchema builds the JSON Schema "object"
// envelope for the action-enum inputSchema: a single "action"
// property (enum of visible top-level command names) plus one
// property per flag declared at the root.
func buildMCPActionEnumInputSchema(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	props := buildMCPActionEnumProperties(spec, cfg)
	required := []string{"action"}
	required = append(required, mcpExtraRequiredFlags(cfg)...)
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// buildMCPActionEnumProperties builds the JSON Schema properties
// map for the action-enum shape. Includes an "action" property
// derived from spec.Commands and one property per flag in
// spec.Flags.
//
// Deprecated commands are excluded from the action enum unless
// cfg.IncludeDeprecated; deprecated flags are likewise filtered.
func buildMCPActionEnumProperties(spec *toolspec.ToolSpec, cfg *RenderConfig) map[string]any {
	props := make(map[string]any)

	action := map[string]any{
		"type":        "string",
		"description": "The action to perform.",
	}
	if actions := visibleCommandNames(spec.Commands, cfg); len(actions) > 0 {
		action["enum"] = actions
	}
	props["action"] = action

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

// --- Shared helpers -----------------------------------------------

// mcpExtraRequiredFlags reads the adopter-declared always-required
// flag names from cfg. Returns nil when the key is absent or holds
// a value of another type.
func mcpExtraRequiredFlags(cfg *RenderConfig) []string {
	extra, _ := cfg.Custom[CustomKeyMCPRequiredFlags].([]string)
	return extra
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
//
// Mirrored by cmdsurface's mcpJSONType (go/transport/cmdsurface):
// transport cannot import toolspec, so the table is duplicated and
// pinned by a parity test rather than shared.
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
