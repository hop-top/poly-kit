package llm

import "fmt"

// ToolChoiceMode says whether the model must call a tool.
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide (the providers' default).
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceAny makes the model call at least one tool, its pick.
	ToolChoiceAny ToolChoiceMode = "any"
	// ToolChoiceNone forbids tool calls although tools are defined.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceTool makes the model call the tool [ToolChoice.Name].
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice constrains tool use on a tool-calling request. Name is the
// tool's [ToolDef.Name] as defined (before any provider name mapping),
// set with Mode [ToolChoiceTool] only.
//
// A nil [Request.ToolChoice] sends none, leaving the provider default.
// Adapters reject an invalid choice, see [CheckToolChoice], before
// sending, and so does a call without tools (Complete, Stream).
type ToolChoice struct {
	Mode ToolChoiceMode
	Name string
}

// CheckToolChoice reports why c cannot go with tools: a mode that is
// not one of the [ToolChoiceMode] constants, no tools, a Name missing
// with [ToolChoiceTool] or set with another mode, or naming no tool in
// tools. A nil c is valid.
func CheckToolChoice(c *ToolChoice, tools []ToolDef) error {
	if c == nil {
		return nil
	}
	switch c.Mode {
	case ToolChoiceAuto, ToolChoiceAny, ToolChoiceNone, ToolChoiceTool:
	default:
		return fmt.Errorf("unknown tool_choice mode %q", c.Mode)
	}
	if len(tools) == 0 {
		return fmt.Errorf("tool_choice %q needs tools", c.Mode)
	}
	if c.Mode != ToolChoiceTool {
		if c.Name != "" {
			return fmt.Errorf("tool_choice %q takes no tool name", c.Mode)
		}
		return nil
	}
	if c.Name == "" {
		return fmt.Errorf("tool_choice %q needs a tool name", c.Mode)
	}
	for _, t := range tools {
		if t.Name == c.Name {
			return nil
		}
	}
	return fmt.Errorf("tool_choice names unknown tool %q", c.Name)
}
