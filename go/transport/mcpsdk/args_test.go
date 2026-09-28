package mcpsdk

// Positional arguments on the SDK-backed surface: the "args"
// property it publishes (cmdsurface.MCPInputSchema) and the mapping
// of a call's "args" array onto positional arguments, on both the
// synchronous and the task path.

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// argsTree has one leaf declaring its positionals and one naming an
// operand in its usage line without declaring it.
func argsTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:   "add <name> [note]",
		Short: "Add an item",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "added %q\n", args)
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write", "kit/args": "name,note?"},
	}, &cobra.Command{
		Use:         "label <name>",
		Short:       "Label an item",
		Args:        cobra.ExactArgs(1),
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	})
	return root
}

func argsSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	r := api.NewRouter()
	if err := Mount(cmdsurface.New(argsTree()), r); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return connect(t, srv.URL+"/mcp", nil)
}

func TestArgsArePublishedAndMapped(t *testing.T) {
	sess := argsSession(t)

	list, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}
	schema, _ := byName["add"].InputSchema.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if args, _ := props["args"].(map[string]any); args["type"] != "array" || args["minItems"] != float64(1) {
		t.Errorf("add args property = %v, want a string array with minItems 1", props["args"])
	}
	if !strings.Contains(byName["label"].Description, "does not declare (kit/args)") {
		t.Errorf("label description = %q, want the undeclared-arguments note", byName["label"].Description)
	}

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "add", Arguments: map[string]any{"args": []any{"bolt", "spare"}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || textOf(res) != "added [\"bolt\" \"spare\"]\n" {
		t.Errorf("add = %q (isError %v), want both positionals", textOf(res), res.IsError)
	}

	res, err = sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "add", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || textOf(res) != "missing required argument: name" {
		t.Errorf("add without args = %q (isError %v), want the missing-argument error", textOf(res), res.IsError)
	}
}

func TestArgsAreMappedOnTheTaskPath(t *testing.T) {
	b := cmdsurface.New(argsTree())
	s, err := New(b, WithStateless(), WithJSONResponse(),
		WithTasks(TasksConfig{Tools: []string{"add"}, TTL: time.Minute, PollInterval: 10 * time.Millisecond}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := api.NewRouter()
	if err := s.Mount(r); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	body := func(arguments string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"add","arguments":%s,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",%s,"io.modelcontextprotocol/clientInfo":{"name":"kit-task-probe","version":"0"}}}}`,
			arguments, tasksCapMeta)
	}

	created := taskResult(t, taskPost(t, srv.URL+"/mcp", taskCallHeaders("add", nil), body(`{"args":["bolt"]}`)))
	taskID, _ := created["taskId"].(string)
	if taskID == "" {
		t.Fatalf("create = %v, want a task", created)
	}
	done := taskPollUntil(t, srv, taskID, nil, "completed")
	result, _ := done["result"].(map[string]any)
	if got := contentText(result); got != "added [\"bolt\"]\n" {
		t.Errorf("task result = %q (%v), want the positional to reach the command", got, done)
	}

	refused := taskResult(t, taskPost(t, srv.URL+"/mcp", taskCallHeaders("add", nil), body(`{}`)))
	if refused["isError"] != true || contentText(refused) != "missing required argument: name" {
		t.Errorf("create without args = %v, want an isError result and no task", refused)
	}
}
