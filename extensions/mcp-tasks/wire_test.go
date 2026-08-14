// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpext.example/tasks"
)

// TestServerCapabilityAdvertised pins the server-side declaration:
// capabilities.extensions carries the extension ID on the legacy
// initialize handshake (and DeclareServerCapability preserves the
// SDK's default logging capability).
func TestServerCapabilityAdvertised(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	}))

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`
	env := post(t, ts.URL, nil, body)
	res := resultMap(t, env)
	caps, _ := res["capabilities"].(map[string]any)
	exts, _ := caps["extensions"].(map[string]any)
	if _, ok := exts[tasks.ExtensionID]; !ok {
		t.Errorf("capabilities.extensions = %v, want %s declared", caps["extensions"], tasks.ExtensionID)
	}
	if _, ok := caps["logging"]; !ok {
		t.Errorf("capabilities = %v, want SDK default logging preserved", caps)
	}
}

// TestDeclareServerCapabilityMergesExisting pins that host-provided
// capabilities survive the declaration.
func TestDeclareServerCapabilityMergesExisting(t *testing.T) {
	so := &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{
		Completions: &mcp.CompletionCapabilities{},
	}}
	tasks.DeclareServerCapability(so)
	if so.Capabilities.Completions == nil {
		t.Error("existing completions capability dropped")
	}
	if _, ok := so.Capabilities.Extensions[tasks.ExtensionID]; !ok {
		t.Error("extension not declared")
	}
}

// TestMissingCapabilityOnTasksMethods pins the -32003 gate: every
// tasks/* method refuses non-declaring requests with the SEP's code
// and names the required extension in error data.
func TestMissingCapabilityOnTasksMethods(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	}))

	for _, method := range []string{tasks.MethodGet, tasks.MethodUpdate, tasks.MethodCancel} {
		env := post(t, ts.URL, tasksHeaders(method, "task_x"), tasksBody(1, method, "task_x", false, ""))
		if env.Error == nil || env.Error.Code != tasks.CodeMissingClientCapability {
			t.Errorf("%s undeclared: error = %+v, want -32003", method, env.Error)
			continue
		}
		var data struct {
			RequiredCapabilities struct {
				Extensions map[string]json.RawMessage `json:"extensions"`
			} `json:"requiredCapabilities"`
		}
		if err := json.Unmarshal(env.Error.Data, &data); err != nil {
			t.Errorf("%s: error data %s: %v", method, env.Error.Data, err)
			continue
		}
		if _, ok := data.RequiredCapabilities.Extensions[tasks.ExtensionID]; !ok {
			t.Errorf("%s: error data = %s, want required extension named", method, env.Error.Data)
		}
	}
}

// TestNoExistenceOracle pins principal binding: an unknown task ID
// and a foreign principal's task ID produce byte-identical -32602
// errors on every tasks/* method.
func TestNoExistenceOracle(t *testing.T) {
	release := make(chan struct{})
	var ext *tasks.Extension
	ts, ext := newHarness(t,
		&tasks.Options{Principal: func(h http.Header) string { return h.Get("Authorization") }},
		taskToolHandlerVar(&ext, func(ctx context.Context, _ *tasks.Handle) (*mcp.CallToolResult, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return textResult("x"), nil
		}))
	defer close(release)

	alice := map[string]string{"Authorization": "Bearer alice"}
	bob := map[string]string{"Authorization": "Bearer bob"}
	taskID := createTask(t, ts, alice)["taskId"].(string)

	// Owner sees it.
	if res := resultMap(t, getTask(t, ts, taskID, alice)); res["status"] != "working" {
		t.Fatalf("owner tasks/get = %v, want working", res)
	}

	errBody := func(method, id string, hdr map[string]string) string {
		h := tasksHeaders(method, id)
		for k, v := range hdr {
			h[k] = v
		}
		env := post(t, ts.URL, h, tasksBody(9, method, id, true, ""))
		if env.Error == nil || env.Error.Code != -32602 {
			t.Fatalf("%s(%s): error = %+v, want -32602", method, id, env.Error)
		}
		b, _ := json.Marshal(env.Error)
		return string(b)
	}

	for _, method := range []string{tasks.MethodGet, tasks.MethodUpdate, tasks.MethodCancel} {
		unknown := errBody(method, "task_doesnotexist", alice)
		foreign := errBody(method, taskID, bob)
		if !bytes.Equal([]byte(unknown), []byte(foreign)) {
			t.Errorf("%s: unknown-id and foreign-principal errors differ:\n  unknown: %s\n  foreign: %s",
				method, unknown, foreign)
		}
	}

	// The foreign probes must not have affected the task.
	if res := resultMap(t, getTask(t, ts, taskID, alice)); res["status"] != "working" {
		t.Errorf("owner tasks/get after foreign probes = %v, want working", res)
	}
}

// TestTasksListAndResultAbsent pins the SEP's reservations:
// tasks/list and tasks/result do not exist and answer -32601.
func TestTasksListAndResultAbsent(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	}))

	for _, method := range []string{"tasks/list", "tasks/result"} {
		env := post(t, ts.URL, tasksHeaders(method, "task_x"), tasksBody(1, method, "task_x", true, ""))
		if env.Error == nil || env.Error.Code != -32601 {
			t.Errorf("%s: error = %+v, want -32601", method, env.Error)
		}
	}
}

// TestMcpNameHeaderTolerated pins SEP-2243 routing-header tolerance:
// tasks/get works with the mandated Mcp-Name=taskId, with a
// mismatched value, and with the headers absent entirely (body
// sniffing takes over when Mcp-Method is missing).
func TestMcpNameHeaderTolerated(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	}))

	taskID := createTask(t, ts, nil)["taskId"].(string)
	pollUntil(t, ts, taskID, nil, "completed")

	cases := []struct {
		name string
		hdr  map[string]string
	}{
		{"mandated headers", tasksHeaders(tasks.MethodGet, taskID)},
		{"mismatched Mcp-Name", tasksHeaders(tasks.MethodGet, "task_other")},
		{"no routing headers", nil},
	}
	for _, tc := range cases {
		env := post(t, ts.URL, tc.hdr, tasksBody(2, tasks.MethodGet, taskID, true, ""))
		if env.Error != nil {
			t.Errorf("%s: error = %+v, want served", tc.name, env.Error)
			continue
		}
		if res := resultMap(t, env); res["status"] != "completed" {
			t.Errorf("%s: status = %v, want completed", tc.name, res["status"])
		}
	}
}

// TestTaskIDsUnguessable pins the entropy contract at the observable
// level: IDs are unique and never ordered or derived from a counter.
func TestTaskIDsUnguessable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		id := tasks.NewTaskID()
		if !strings.HasPrefix(id, "task_") || len(id) != len("task_")+22 {
			t.Fatalf("id %q: want task_ prefix + 22 base64url chars (128 bits)", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

// TestTasksRequireRequestID pins that a tasks/* notification (no id)
// is rejected as an invalid request rather than silently handled.
func TestTasksRequireRequestID(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	}))

	body := `{"jsonrpc":"2.0","method":"tasks/get","params":{"taskId":"task_x","_meta":` + capsMeta(true) + `}}`
	env := post(t, ts.URL, tasksHeaders(tasks.MethodGet, "task_x"), body)
	if env.Error == nil || env.Error.Code != -32600 {
		t.Errorf("id-less tasks/get: error = %+v, want -32600", env.Error)
	}
}
