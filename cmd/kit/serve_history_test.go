package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestServe_HistoryAndRevert is a smoke test for GET /:type/:id/history
// and POST /:type/:id/revert wired on top of VersionedDocumentStore
// (T-0353). End-to-end durability across a process restart is
// covered separately by T-0352.
func TestServe_HistoryAndRevert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	info, cleanup := startServer(t, bin)
	defer cleanup()

	base := baseURL(info)
	client := &http.Client{Timeout: 5 * time.Second}

	authedDo := func(method, url, contentType string, body io.Reader) (*http.Response, error) {
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Authorization", "Bearer "+info.Token)
		return client.Do(req)
	}

	// Create a doc, then update twice → 3 versions (seq 1, 2, 3).
	createBody := []byte(`{"id":"hh","title":"v1"}`)
	resp, err := authedDo("POST", base+"/notes/", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create: expected 201, got %d", resp.StatusCode)
	}

	for i, body := range []string{`{"id":"hh","title":"v2"}`, `{"id":"hh","title":"v3"}`} {
		r, err := authedDo("PUT", base+"/notes/hh", "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("update %d: expected 200, got %d", i, r.StatusCode)
		}
	}

	// History: newest first per spec, three entries.
	hresp, err := client.Get(base + "/notes/hh/history")
	if err != nil {
		t.Fatal(err)
	}
	defer hresp.Body.Close()
	if hresp.StatusCode != 200 {
		t.Fatalf("history: expected 200, got %d", hresp.StatusCode)
	}
	var histPayload struct {
		Versions []struct {
			Version   int             `json:"version"`
			Data      json.RawMessage `json:"data"`
			Timestamp string          `json:"timestamp"`
			Operation string          `json:"operation"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(hresp.Body).Decode(&histPayload); err != nil {
		t.Fatal(err)
	}
	if len(histPayload.Versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(histPayload.Versions))
	}
	// Newest first.
	if histPayload.Versions[0].Version != 3 {
		t.Fatalf("expected newest-first ordering: versions[0].version=3, got %d", histPayload.Versions[0].Version)
	}
	if histPayload.Versions[2].Version != 1 {
		t.Fatalf("expected versions[2].version=1, got %d", histPayload.Versions[2].Version)
	}
	if histPayload.Versions[2].Operation != "create" {
		t.Fatalf("expected versions[2].operation=create, got %q", histPayload.Versions[2].Operation)
	}
	if histPayload.Versions[0].Operation != "update" {
		t.Fatalf("expected versions[0].operation=update, got %q", histPayload.Versions[0].Operation)
	}

	// Revert to seq=1 (the original).
	rresp, err := authedDo("POST", base+"/notes/hh/revert", "application/json",
		bytes.NewReader([]byte(`{"version":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer rresp.Body.Close()
	if rresp.StatusCode != 200 {
		t.Fatalf("revert: expected 200, got %d", rresp.StatusCode)
	}
	var revertedDoc struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rresp.Body).Decode(&revertedDoc); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(revertedDoc.Data, []byte(`"v1"`)) {
		t.Fatalf("revert: expected data to contain v1, got %s", revertedDoc.Data)
	}

	// Revert to a version that does not exist → 409.
	r409, err := authedDo("POST", base+"/notes/hh/revert", "application/json",
		bytes.NewReader([]byte(`{"version":999}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer r409.Body.Close()
	if r409.StatusCode != 409 {
		t.Fatalf("revert(missing): expected 409, got %d", r409.StatusCode)
	}

	// History on unknown doc → 404.
	r404, err := client.Get(base + "/notes/missing/history")
	if err != nil {
		t.Fatal(err)
	}
	defer r404.Body.Close()
	if r404.StatusCode != 404 {
		t.Fatalf("history(missing): expected 404, got %d", r404.StatusCode)
	}
}
