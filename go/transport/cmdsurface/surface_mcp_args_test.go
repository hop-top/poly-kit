package cmdsurface

// Positional arguments over MCP (surface_mcp_args.go), on both
// protocol revisions MountMCP serves: the "args" property tools/list
// publishes and the mapping of a tools/call "args" array back onto
// positional arguments.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argsTestTree builds:
//
//	root
//	└── item
//	    ├── add      kit/args "name,note?"; --count
//	    ├── label    usage names <name>, no kit/args
//	    ├── pin      kit/args "id"; --args flag holds the name
//	    └── approve  kit/args "name"; kit/requires-confirmation
func argsTestTree() *cobra.Command {
	echo := func(verb string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			extra := ""
			if f := cmd.Flags().Lookup("count"); f != nil && f.Changed {
				extra = " x" + f.Value.String()
			}
			if f := cmd.Flags().Lookup("args"); f != nil && f.Changed {
				extra = " --args=" + f.Value.String()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %q%s\n", verb, args, extra)
			return nil
		}
	}
	root := &cobra.Command{Use: "root"}
	item := &cobra.Command{Use: "item"}

	add := &cobra.Command{
		Use:   "add <name> [note]",
		Short: "Add an item",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  echo("added"),
		Annotations: map[string]string{
			"kit/side-effect": "write",
			"kit/args":        "name,note?",
		},
	}
	add.Flags().Int("count", 1, "how many")

	label := &cobra.Command{
		Use:         "label <name>",
		Short:       "Label an item",
		Args:        cobra.ExactArgs(1),
		RunE:        echo("labeled"),
		Annotations: map[string]string{"kit/side-effect": "write"},
	}

	pin := &cobra.Command{
		Use:   "pin <id>",
		Short: "Pin an item",
		RunE:  echo("pinned"),
		Annotations: map[string]string{
			"kit/side-effect": "write",
			"kit/args":        "id",
		},
	}
	pin.Flags().String("args", "", "extra arguments")

	approve := &cobra.Command{
		Use:   "approve <name>",
		Short: "Approve an item",
		Args:  cobra.ExactArgs(1),
		RunE:  echo("approved"),
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/args":                  "name",
			"kit/requires-confirmation": "true",
		},
	}

	item.AddCommand(add, label, pin, approve)
	root.AddCommand(item)
	return root
}

// argsEra drives one protocol revision of MountMCP.
type argsEra struct {
	name string
	list func(t *testing.T) map[string]map[string]any
	call func(t *testing.T, tool string, arguments map[string]any) (status int, text string, isError bool)
}

func argsEras(t *testing.T) []argsEra {
	t.Helper()
	srv := modernServerFor(t, New(argsTestTree()))

	decodeTools := func(t *testing.T, res map[string]any) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		tools, _ := res["tools"].([]any)
		for _, x := range tools {
			tool, _ := x.(map[string]any)
			name, _ := tool["name"].(string)
			out[name] = tool
		}
		return out
	}
	decodeCall := func(t *testing.T, m map[string]any) (string, bool) {
		t.Helper()
		res, ok := m["result"].(map[string]any)
		require.True(t, ok, "tools/call answered with a result: %v", m)
		var b strings.Builder
		content, _ := res["content"].([]any)
		for _, c := range content {
			block, _ := c.(map[string]any)
			text, _ := block["text"].(string)
			b.WriteString(text)
		}
		isErr, _ := res["isError"].(bool)
		return b.String(), isErr
	}
	legacyBody := func(t *testing.T, method string, params map[string]any) string {
		t.Helper()
		enc, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		require.NoError(t, err)
		return string(enc)
	}

	return []argsEra{
		{
			name: "2024-11-05",
			list: func(t *testing.T) map[string]map[string]any {
				_, m := postJSON(t, srv, "/mcp", nil, legacyBody(t, "tools/list", map[string]any{}))
				res, _ := m["result"].(map[string]any)
				return decodeTools(t, res)
			},
			call: func(t *testing.T, tool string, arguments map[string]any) (int, string, bool) {
				status, m := postJSON(t, srv, "/mcp", nil,
					legacyBody(t, "tools/call", map[string]any{"name": tool, "arguments": arguments}))
				text, isErr := decodeCall(t, m)
				return status, text, isErr
			},
		},
		{
			name: "2026-07-28",
			list: func(t *testing.T) map[string]map[string]any {
				_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
					modernBody(t, "tools/list", map[string]any{}))
				res, _ := m["result"].(map[string]any)
				return decodeTools(t, res)
			},
			call: func(t *testing.T, tool string, arguments map[string]any) (int, string, bool) {
				status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", tool),
					callBody(t, tool, arguments))
				text, isErr := decodeCall(t, m)
				return status, text, isErr
			},
		},
	}
}

func TestMCPArgs_ToolsListPublishesDeclaredPositionals(t *testing.T) {
	for _, era := range argsEras(t) {
		t.Run(era.name, func(t *testing.T) {
			tools := era.list(t)

			add := tools["item.add"]
			require.NotNil(t, add)
			assert.Equal(t, "Add an item", add["description"])
			schema, _ := add["inputSchema"].(map[string]any)
			props, _ := schema["properties"].(map[string]any)
			assert.Equal(t, map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Positional arguments in order: name, note?",
				"minItems":    float64(1),
			}, props["args"])
			assert.Contains(t, props, "count", "flags stay beside the args array")
			assert.Equal(t, []any{"args"}, schema["required"])

			label := tools["item.label"]
			require.NotNil(t, label)
			lschema, _ := label["inputSchema"].(map[string]any)
			assert.NotContains(t, lschema["properties"], "args", "undeclared positionals are not guessed")
			assert.Equal(t,
				"Label an item Takes positional arguments it does not declare (kit/args), so they cannot be passed over MCP.",
				label["description"])

			pin := tools["item.pin"]
			require.NotNil(t, pin)
			pschema, _ := pin["inputSchema"].(map[string]any)
			pprops, _ := pschema["properties"].(map[string]any)
			pargs, _ := pprops["args"].(map[string]any)
			assert.Equal(t, "string", pargs["type"], `the --args flag keeps the "args" property`)
			assert.Nil(t, pschema["required"])
			assert.Equal(t,
				`Pin an item Takes positional arguments that cannot be passed over MCP: its --args flag holds the "args" property.`,
				pin["description"])
		})
	}
}

func TestMCPArgs_CallMapsArgsOntoPositionals(t *testing.T) {
	for _, era := range argsEras(t) {
		t.Run(era.name, func(t *testing.T) {
			status, text, isErr := era.call(t, "item.add", map[string]any{
				"args": []any{"bolt", "spare"}, "count": 2,
			})
			assert.Equal(t, http.StatusOK, status)
			assert.False(t, isErr, text)
			assert.Equal(t, "added [\"bolt\" \"spare\"] x2\n", text)

			_, text, isErr = era.call(t, "item.add", map[string]any{"args": []any{"nut"}})
			assert.False(t, isErr, text)
			assert.Equal(t, "added [\"nut\"]\n", text, "an optional argument may be left out")
		})
	}
}

func TestMCPArgs_CallRefusesMissingOrMalformedArgs(t *testing.T) {
	for _, era := range argsEras(t) {
		t.Run(era.name, func(t *testing.T) {
			for label, arguments := range map[string]map[string]any{
				"absent": {"count": 2},
				"empty":  {"args": []any{}},
			} {
				status, text, isErr := era.call(t, "item.add", arguments)
				assert.Equal(t, http.StatusOK, status, label)
				assert.True(t, isErr, label)
				assert.Equal(t, "missing required argument: name", text, label)
			}
			for label, arguments := range map[string]map[string]any{
				"string": {"args": "bolt"},
				"number": {"args": []any{1}},
			} {
				_, text, isErr := era.call(t, "item.add", arguments)
				assert.True(t, isErr, label)
				assert.Equal(t, `"args" must be an array of strings`, text, label)
			}
		})
	}
}

func TestMCPArgs_ArgumentsAreCheckedBeforeConfirmation(t *testing.T) {
	for _, era := range argsEras(t) {
		t.Run(era.name, func(t *testing.T) {
			status, text, isErr := era.call(t, "item.approve", map[string]any{})
			assert.Equal(t, http.StatusOK, status, "not the confirmation gate's 428")
			assert.True(t, isErr)
			assert.Equal(t, "missing required argument: name", text)

			status, text, isErr = era.call(t, "item.approve", map[string]any{"args": []any{"bolt"}})
			assert.Equal(t, http.StatusPreconditionRequired, status)
			assert.True(t, isErr)
			assert.Contains(t, text, "confirmation required")
		})
	}
}

func TestMCPArgs_UnpublishedArgsStayFlags(t *testing.T) {
	for _, era := range argsEras(t) {
		t.Run(era.name, func(t *testing.T) {
			// A flag holding the name receives the value as before.
			_, text, isErr := era.call(t, "item.pin", map[string]any{"args": "x"})
			assert.False(t, isErr, text)
			assert.Equal(t, "pinned [] --args=x\n", text)

			// An undeclared leaf gets no positionals: "args" is just an
			// unknown flag, which the command refuses.
			_, text, isErr = era.call(t, "item.label", map[string]any{"args": []any{"bolt"}})
			assert.True(t, isErr)
			assert.NotContains(t, text, "labeled")
		})
	}
}
