package cmdsurface

// Coverage for the modern tools/list handler
// (surface_mcp_modern_list.go): envelope shape, cache hints,
// per-level alphabetical tool ordering, surface filtering, ignored
// pagination cursor, and descriptor identity with the legacy handler.
// Fixtures come from modernTestTree (surface_mcp_modern_test.go).

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestModernList_Envelope(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
	if ttl, _ := res["ttlMs"].(float64); ttl != 0 {
		t.Errorf("ttlMs=%v want=0 (default)", res["ttlMs"])
	}
	if res["cacheScope"] != "private" {
		t.Errorf("cacheScope=%v want=private (default)", res["cacheScope"])
	}
	meta, _ := res["_meta"].(map[string]any)
	if _, ok := meta[metaKeyServerInfo].(map[string]any); !ok {
		t.Errorf("_meta serverInfo missing: %v", res)
	}
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools returned")
	}
	first, _ := tools[0].(map[string]any)
	for _, key := range []string{"name", "description", "inputSchema"} {
		if _, ok := first[key]; !ok {
			t.Errorf("tool envelope missing %q: %v", key, first)
		}
	}
}

func TestModernList_AlphabeticalPerCobraLevel(t *testing.T) {
	srv := modernServer(t)
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	res, _ := m["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	want := []string{"auth-op", "confirm-op", "nuke", "ping", "widget.add", "widget.list"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("tool order=%v want=%v", names, want)
	}
}

func TestModernList_FiltersDisabledLeaves(t *testing.T) {
	b := New(modernTestTree())
	b.Hide("ping", SurfaceMCP)
	srv := modernServerFor(t, b)
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	res, _ := m["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	for _, tl := range tools {
		if tl.(map[string]any)["name"] == "ping" {
			t.Error("hidden leaf 'ping' still listed")
		}
	}
}

func TestModernList_CursorIgnoredNoNextCursor(t *testing.T) {
	srv := modernServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", map[string]any{"cursor": "opaque-page-2"}))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if _, ok := res["nextCursor"]; ok {
		t.Errorf("nextCursor must not be returned (pagination not implemented): %v", res)
	}
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Error("cursor param must be ignored, full list expected")
	}
}

func TestModernList_CacheHintsFromOption(t *testing.T) {
	srv := modernServer(t, WithMCPCacheHints(7*time.Second, MCPCacheScopePublic))
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	res, _ := m["result"].(map[string]any)
	if ttl, _ := res["ttlMs"].(float64); ttl != 7000 {
		t.Errorf("ttlMs=%v want=7000", res["ttlMs"])
	}
	if res["cacheScope"] != "public" {
		t.Errorf("cacheScope=%v want=public", res["cacheScope"])
	}
}

func TestModernList_DescriptorsIdenticalToLegacy(t *testing.T) {
	// Both eras build descriptors through buildToolEnvelope, so the
	// tools arrays must be deep-equal on the same mount.
	srv := modernServer(t)

	_, legacyResp := postJSON(t, srv, "/mcp", nil,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	legacyRes, _ := legacyResp["result"].(map[string]any)
	legacyTools := legacyRes["tools"]

	_, modernResp := postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	modernRes, _ := modernResp["result"].(map[string]any)
	modernTools := modernRes["tools"]

	if !reflect.DeepEqual(legacyTools, modernTools) {
		t.Errorf("descriptor drift between eras:\nlegacy=%v\nmodern=%v", legacyTools, modernTools)
	}
}
