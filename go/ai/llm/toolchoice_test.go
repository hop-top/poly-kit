package llm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/ai/llm"
)

func TestCheckToolChoice(t *testing.T) {
	tools := []llm.ToolDef{{Name: "get_weather"}, {Name: "pane.open"}}
	for _, tc := range []struct {
		name   string
		choice *llm.ToolChoice
		tools  []llm.ToolDef
		err    string
	}{
		{"unset", nil, nil, ""},
		{"unset with tools", nil, tools, ""},
		{"auto", &llm.ToolChoice{Mode: llm.ToolChoiceAuto}, tools, ""},
		{"any", &llm.ToolChoice{Mode: llm.ToolChoiceAny}, tools, ""},
		{"none", &llm.ToolChoice{Mode: llm.ToolChoiceNone}, tools, ""},
		{"tool", &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "pane.open"}, tools, ""},
		{"no tools", &llm.ToolChoice{Mode: llm.ToolChoiceAuto}, nil, `tool_choice "auto" needs tools`},
		{"none needs tools too", &llm.ToolChoice{Mode: llm.ToolChoiceNone}, nil, `tool_choice "none" needs tools`},
		{"empty mode", &llm.ToolChoice{}, tools, `unknown tool_choice mode ""`},
		{"unknown mode", &llm.ToolChoice{Mode: "required"}, tools, `unknown tool_choice mode "required"`},
		{"tool without name", &llm.ToolChoice{Mode: llm.ToolChoiceTool}, tools, `tool_choice "tool" needs a tool name`},
		{"name on another mode", &llm.ToolChoice{Mode: llm.ToolChoiceAny, Name: "get_weather"}, tools, `tool_choice "any" takes no tool name`},
		{"unknown tool", &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "nope"}, tools, `tool_choice names unknown tool "nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := llm.CheckToolChoice(tc.choice, tc.tools)
			if tc.err == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.err)
		})
	}
}
