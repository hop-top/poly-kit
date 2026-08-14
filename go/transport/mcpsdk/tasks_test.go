package mcpsdk

// Tests for the SEP-2663 task binding. Wire conformance (statuses,
// error codes, input rounds, TTL, no-oracle shape) is pinned in the
// extension module's own suite; these tests pin what KIT adds on top:
// mount-time eligibility validation, safety gates enforced at task
// creation (destructive ceiling, auth, MRTR confirmation), principal
// binding via the Authorization header, detached execution through
// Bridge.Invoke, and the server-directed inline fallback. Requests
// are raw JSON-RPC at protocol 2026-07-28 because the SDK client
// cannot surface CreateTaskResult fields (see the module README).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// tasksTree builds the tree used by the task tests. release gates the
// slow leaf; counter counts every leaf execution.
func tasksTree(counter *atomic.Int32, release <-chan struct{}) *cobra.Command {
	root := &cobra.Command{Use: "root"}
	add := func(use, short, sideEffect string, ann map[string]string, run func(cmd *cobra.Command) error) {
		c := &cobra.Command{
			Use:   use,
			Short: short,
			RunE: func(cmd *cobra.Command, _ []string) error {
				counter.Add(1)
				return run(cmd)
			},
			Annotations: map[string]string{},
		}
		if sideEffect != "" {
			c.Annotations["kit/side-effect"] = sideEffect
		}
		for k, v := range ann {
			c.Annotations[k] = v
		}
		root.AddCommand(c)
	}
	add("slow", "Slow op", "write", nil, func(cmd *cobra.Command) error {
		if release != nil {
			<-release
		}
		cmd.Println("slow done")
		return nil
	})
	add("nuke", "Destroy", "destructive", nil, func(cmd *cobra.Command) error {
		cmd.Println("nuked")
		return nil
	})
	add("secret", "Locked", "", map[string]string{"kit/auth-required": "true"}, func(cmd *cobra.Command) error {
		cmd.Println("unlocked")
		return nil
	})
	add("deploy", "Deploy", "", map[string]string{"kit/requires-confirmation": "true"}, func(cmd *cobra.Command) error {
		cmd.Println("deployed")
		return nil
	})
	add("ping", "Ping", "read", nil, func(cmd *cobra.Command) error {
		cmd.Println("pong")
		return nil
	})
	return root
}

// newTasksHarness mounts a stateless surface with tasks enabled for
// the given tools.
func newTasksHarness(t *testing.T, root *cobra.Command, tcfg TasksConfig, extra ...Option) *httptest.Server {
	t.Helper()
	b := cmdsurface.New(root)
	opts := append([]Option{WithStateless(), WithJSONResponse(), WithTasks(tcfg)}, extra...)
	s, err := New(b, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := api.NewRouter()
	if err := s.Mount(r); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

type taskEnv struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// taskPost sends one raw JSON-RPC request with headers.
func taskPost(t *testing.T, url string, hdr map[string]string, body string) *taskEnv {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var env taskEnv
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode (status %d): %v", resp.StatusCode, err)
	}
	return &env
}

const tasksCapMeta = `"io.modelcontextprotocol/clientCapabilities":{"extensions":{"io.modelcontextprotocol/tasks":{}}}`

// taskCallBody builds a declaring 2026-07-28 tools/call. extraParams
// is spliced in verbatim when non-empty.
func taskCallBody(id int, tool string, declare bool, extraParams string) string {
	caps := `"io.modelcontextprotocol/clientCapabilities":{}`
	if declare {
		caps = tasksCapMeta
	}
	if extraParams != "" {
		extraParams = "," + extraParams
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",%s,"io.modelcontextprotocol/clientInfo":{"name":"kit-task-probe","version":"0"}}%s}}`,
		id, tool, caps, extraParams)
}

func taskCallHeaders(tool string, extra map[string]string) map[string]string {
	hdr := map[string]string{
		"Mcp-Protocol-Version": "2026-07-28",
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             tool,
	}
	for k, v := range extra {
		hdr[k] = v
	}
	return hdr
}

func taskMethodBody(id int, method, taskID string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{"taskId":%q,"_meta":{%s}}}`,
		id, method, taskID, tasksCapMeta)
}

func taskMethodHeaders(method, taskID string, extra map[string]string) map[string]string {
	hdr := map[string]string{
		"Mcp-Protocol-Version": "2026-07-28",
		"Mcp-Method":           method,
		"Mcp-Name":             taskID,
	}
	for k, v := range extra {
		hdr[k] = v
	}
	return hdr
}

func taskResult(t *testing.T, env *taskEnv) map[string]any {
	t.Helper()
	if env.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", env.Error)
	}
	var m map[string]any
	if err := json.Unmarshal(env.Result, &m); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return m
}

// taskGet polls tasks/get once with optional extra headers.
func taskGet(t *testing.T, srv *httptest.Server, taskID string, hdr map[string]string) *taskEnv {
	t.Helper()
	return taskPost(t, srv.URL+"/mcp", taskMethodHeaders("tasks/get", taskID, hdr),
		taskMethodBody(50, "tasks/get", taskID))
}

// taskPollUntil polls until status want.
func taskPollUntil(t *testing.T, srv *httptest.Server, taskID string, hdr map[string]string, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := taskResult(t, taskGet(t, srv, taskID, hdr))
		if res["status"] == want {
			return res
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s to reach %s", taskID, want)
	return nil
}

// contentText joins the text blocks of a marshaled CallToolResult.
func contentText(result map[string]any) string {
	var b strings.Builder
	blocks, _ := result["content"].([]any)
	for _, blk := range blocks {
		if m, ok := blk.(map[string]any); ok {
			if s, ok := m["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// TestWithTasksValidatesTools pins mount-time eligibility validation.
func TestWithTasksValidatesTools(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	if _, err := New(b, WithTasks(TasksConfig{Tools: []string{"widget.add", "bogus"}})); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown tool: err = %v, want error naming bogus", err)
	}
	if _, err := New(b, WithTasks(TasksConfig{})); err == nil {
		t.Error("empty Tools accepted, want error")
	}
	if _, err := New(b, WithTasks(TasksConfig{Tools: []string{"widget.add", "ping"}})); err != nil {
		t.Errorf("valid tools rejected: %v", err)
	}
}

// TestTaskLifecycleOverBridge pins the end-to-end kit path: eligible
// leaf + declaring client → durable task, detached Runner execution,
// completed result carrying the leaf's stdout.
func TestTaskLifecycleOverBridge(t *testing.T) {
	var counter atomic.Int32
	release := make(chan struct{})
	srv := newTasksHarness(t, tasksTree(&counter, release),
		TasksConfig{Tools: []string{"slow"}, TTL: time.Minute, PollInterval: 50 * time.Millisecond})

	env := taskPost(t, srv.URL+"/mcp", taskCallHeaders("slow", nil), taskCallBody(1, "slow", true, ""))
	created := taskResult(t, env)
	if created["resultType"] != "task" || created["status"] != "working" {
		t.Fatalf("create = %v, want working CreateTaskResult", created)
	}
	if v, _ := created["ttlMs"].(float64); v != 60000 {
		t.Errorf("ttlMs = %v, want 60000", created["ttlMs"])
	}
	taskID := created["taskId"].(string)

	// Durable before respond: resolvable while the leaf is blocked.
	if res := taskResult(t, taskGet(t, srv, taskID, nil)); res["status"] != "working" {
		t.Fatalf("immediate get = %v, want working", res)
	}

	close(release)
	final := taskPollUntil(t, srv, taskID, nil, "completed")
	result, _ := final["result"].(map[string]any)
	if !strings.Contains(contentText(result), "slow done") {
		t.Errorf("result = %v, want the leaf stdout", result)
	}
	if got := counter.Load(); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}
}

// TestTaskNonDeclaringInline pins the server-directed rule on the kit
// surface: without the declaration the same eligible leaf answers
// inline (and the SDK client, which negotiates a pre-2026 protocol,
// is inherently non-declaring).
func TestTaskNonDeclaringInline(t *testing.T) {
	var counter atomic.Int32
	srv := newTasksHarness(t, tasksTree(&counter, nil), TasksConfig{Tools: []string{"ping"}})

	env := taskPost(t, srv.URL+"/mcp", taskCallHeaders("ping", nil), taskCallBody(1, "ping", false, ""))
	res := taskResult(t, env)
	if res["resultType"] == "task" {
		t.Fatal("non-declaring client received CreateTaskResult")
	}
	if _, ok := res["taskId"]; ok {
		t.Fatalf("taskId leaked into inline result: %v", res)
	}
	if !strings.Contains(contentText(res), "pong") {
		t.Errorf("inline result = %v, want pong", res)
	}
}

// TestTaskDestructiveBlockedAtCreation pins safety at creation: the
// policy ceiling refuses before any task exists — no task ID, no
// execution, an in-band isError exactly like the synchronous path.
func TestTaskDestructiveBlockedAtCreation(t *testing.T) {
	var counter atomic.Int32
	srv := newTasksHarness(t, tasksTree(&counter, nil), TasksConfig{Tools: []string{"nuke"}})

	env := taskPost(t, srv.URL+"/mcp", taskCallHeaders("nuke", nil), taskCallBody(1, "nuke", true, ""))
	res := taskResult(t, env)
	if res["resultType"] == "task" {
		t.Fatal("destructive leaf produced a task")
	}
	if _, ok := res["taskId"]; ok {
		t.Fatalf("taskId present on refusal: %v", res)
	}
	if res["isError"] != true || !strings.Contains(contentText(res), "destructive command blocked") {
		t.Errorf("refusal = %v, want isError destructive block", res)
	}
	if got := counter.Load(); got != 0 {
		t.Errorf("executions = %d, want 0", got)
	}
}

// TestTaskPrincipalBinding pins the Authorization-derived principal:
// the creator polls its task; another credential (or none) gets the
// unknown-task -32602 with no oracle.
func TestTaskPrincipalBinding(t *testing.T) {
	var counter atomic.Int32
	release := make(chan struct{})
	defer close(release)
	srv := newTasksHarness(t, tasksTree(&counter, release), TasksConfig{Tools: []string{"slow", "secret"}})

	alice := map[string]string{"Authorization": "Bearer alice"}
	bob := map[string]string{"Authorization": "Bearer bob"}

	// Auth gate still precedes the task path.
	env := taskPost(t, srv.URL+"/mcp", taskCallHeaders("secret", nil), taskCallBody(1, "secret", true, ""))
	if res := taskResult(t, env); res["isError"] != true || !strings.Contains(contentText(res), "authentication required") {
		t.Fatalf("unauthenticated eligible call = %v, want auth refusal", res)
	}

	env = taskPost(t, srv.URL+"/mcp", taskCallHeaders("slow", alice), taskCallBody(2, "slow", true, ""))
	taskID := taskResult(t, env)["taskId"].(string)

	if res := taskResult(t, taskGet(t, srv, taskID, alice)); res["status"] != "working" {
		t.Fatalf("owner get = %v, want working", res)
	}
	for name, hdr := range map[string]map[string]string{"foreign": bob, "anonymous": nil} {
		env := taskGet(t, srv, taskID, hdr)
		if env.Error == nil || env.Error.Code != -32602 {
			t.Errorf("%s get: error = %+v, want -32602", name, env.Error)
		}
	}
}

// TestTaskConfirmMRTRComposition pins the MRTR-then-task flow:
// confirmation resolves synchronously BEFORE any task is created
// (accept proceeds, decline and forged state refuse with zero
// executions), and the X-Confirm-Token header bypasses the exchange
// exactly like the synchronous path.
func TestTaskConfirmMRTRComposition(t *testing.T) {
	var counter atomic.Int32
	srv := newTasksHarness(t, tasksTree(&counter, nil), TasksConfig{Tools: []string{"deploy"}})
	url := srv.URL + "/mcp"

	// Phase 1: the exchange, not a task.
	res := taskResult(t, taskPost(t, url, taskCallHeaders("deploy", nil), taskCallBody(1, "deploy", true, "")))
	if res["resultType"] != "input_required" {
		t.Fatalf("first call = %v, want input_required", res)
	}
	if _, ok := res["taskId"]; ok {
		t.Fatal("task created before confirmation resolved")
	}
	reqs, _ := res["inputRequests"].(map[string]any)
	var confirmKey string
	for k := range reqs {
		if strings.HasPrefix(k, "confirm/") {
			confirmKey = k
		}
	}
	if confirmKey == "" {
		t.Fatalf("inputRequests = %v, want a confirm/ elicitation", reqs)
	}
	state, _ := res["requestState"].(string)
	if state == "" {
		t.Fatal("no requestState on the confirmation exchange")
	}
	if got := counter.Load(); got != 0 {
		t.Fatalf("executions after phase 1 = %d, want 0", got)
	}

	retry := func(id int, action, st string) *taskEnv {
		extra := fmt.Sprintf(`"inputResponses":{%q:{"action":%q}},"requestState":%q`, confirmKey, action, st)
		return taskPost(t, url, taskCallHeaders("deploy", nil), taskCallBody(id, "deploy", true, extra))
	}

	// Decline: refused, nothing created or executed.
	if res := taskResult(t, retry(2, "decline", state)); res["isError"] != true || !strings.Contains(contentText(res), "declined") {
		t.Errorf("decline = %v, want isError declined", res)
	}
	// Forged state: fail closed.
	if res := taskResult(t, retry(3, "accept", state+"tamper")); res["isError"] != true || !strings.Contains(contentText(res), "confirmation required") {
		t.Errorf("forged state = %v, want confirmation required", res)
	}
	if got := counter.Load(); got != 0 {
		t.Fatalf("executions after refusals = %d, want 0", got)
	}

	// Accept: only now the task exists.
	created := taskResult(t, retry(4, "accept", state))
	if created["resultType"] != "task" {
		t.Fatalf("accept = %v, want CreateTaskResult", created)
	}
	final := taskPollUntil(t, srv, created["taskId"].(string), nil, "completed")
	if result, _ := final["result"].(map[string]any); !strings.Contains(contentText(result), "deployed") {
		t.Errorf("final result = %v, want deployed", final)
	}
	if got := counter.Load(); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}

	// Header path: token present skips the exchange entirely.
	hdr := taskCallHeaders("deploy", map[string]string{"X-Confirm-Token": "yes"})
	if res := taskResult(t, taskPost(t, url, hdr, taskCallBody(5, "deploy", true, ""))); res["resultType"] != "task" {
		t.Errorf("token-confirmed call = %v, want immediate CreateTaskResult", res)
	}
}

// TestTasksSurfaceCannotExecute pins the no-amplification property:
// once a task completed, any volume of tasks/get, junk tasks/update,
// and tasks/cancel never reaches the Runner again.
func TestTasksSurfaceCannotExecute(t *testing.T) {
	var counter atomic.Int32
	srv := newTasksHarness(t, tasksTree(&counter, nil), TasksConfig{Tools: []string{"ping"}})
	url := srv.URL + "/mcp"

	env := taskPost(t, url, taskCallHeaders("ping", nil), taskCallBody(1, "ping", true, ""))
	taskID := taskResult(t, env)["taskId"].(string)
	taskPollUntil(t, srv, taskID, nil, "completed")
	if got := counter.Load(); got != 1 {
		t.Fatalf("executions after completion = %d, want 1", got)
	}

	for i := 0; i < 3; i++ {
		taskGet(t, srv, taskID, nil)
	}
	updBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":60,"method":"tasks/update","params":{"taskId":%q,"inputResponses":{"x":{"action":"accept"}},"_meta":{%s}}}`, taskID, tasksCapMeta)
	taskPost(t, url, taskMethodHeaders("tasks/update", taskID, nil), updBody)
	taskPost(t, url, taskMethodHeaders("tasks/cancel", taskID, nil), taskMethodBody(61, "tasks/cancel", taskID))
	taskPost(t, url, taskMethodHeaders("tasks/cancel", taskID, nil), taskMethodBody(62, "tasks/cancel", taskID))

	if res := taskResult(t, taskGet(t, srv, taskID, nil)); res["status"] != "completed" {
		t.Errorf("status after the storm = %v, want completed", res["status"])
	}
	if got := counter.Load(); got != 1 {
		t.Errorf("executions after tasks/* storm = %d, want 1 — the tasks surface executed a leaf", got)
	}
}
