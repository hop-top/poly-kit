package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// startServerAt runs `kit serve` against the supplied data dir
// instead of the t.TempDir() that startServer in serve_test.go
// hard-codes. T-0352 needs to point a fresh server at the SAME
// on-disk SQLite file written by a previous run, so the data dir
// must outlive a single startServer call. The two helpers diverge
// only on this; the rest of the boot flow is byte-identical.
func startServerAt(t *testing.T, bin, dataDir string) (serverInfo, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	cmd := exec.CommandContext(ctx, bin, "serve",
		"--port", "0",
		"--data", dataDir,
		"--no-peer",
		"--no-sync",
	)
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}

	cleanup := func() {
		cancel()
		_ = cmd.Wait()
	}

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		cleanup()
		t.Fatal("no startup JSON from server")
	}

	var info serverInfo
	if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
		cleanup()
		t.Fatalf("parse startup JSON: %s (got: %q)", err, scanner.Text())
	}

	return info, cleanup
}

// TestServe_RestartPreservesHistory is the end-to-end durability
// proof for the SQLite-backed VersionedDocumentStore wired in
// T-0353. It writes versions through the HTTP API, kills the
// process, restarts a fresh server pointed at the SAME --data dir,
// fetches history through HTTP, and asserts the histories from the
// pre- and post-restart runs are byte-identical (same wire payload
// per version slot).
//
// Two distinct (type, id) pairs and >=3 mutations each are exercised
// so the test catches per-document state leaks between restarts.
func TestServe_RestartPreservesHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()

	type versionWire struct {
		Version   int             `json:"version"`
		Data      json.RawMessage `json:"data"`
		Timestamp string          `json:"timestamp"`
		Operation string          `json:"operation"`
	}

	type pair struct {
		docType string
		id      string
		muts    []string
	}
	pairs := []pair{
		{
			docType: "notes",
			id:      "alpha",
			muts: []string{
				`{"id":"alpha","title":"a-1"}`,
				`{"id":"alpha","title":"a-2"}`,
				`{"id":"alpha","title":"a-3"}`,
			},
		},
		{
			docType: "tasks",
			id:      "beta",
			muts: []string{
				`{"id":"beta","status":"todo"}`,
				`{"id":"beta","status":"doing"}`,
				`{"id":"beta","status":"done"}`,
				`{"id":"beta","status":"archived"}`,
			},
		},
	}

	authedDo := func(token, method, url, contentType string, body io.Reader) (*http.Response, error) {
		client := &http.Client{Timeout: 5 * time.Second}
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return client.Do(req)
	}

	fetchHistory := func(base, docType, id string) []versionWire {
		t.Helper()
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("%s/%s/%s/history", base, docType, id))
		if err != nil {
			t.Fatalf("history(%s/%s): %v", docType, id, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("history(%s/%s): status %d", docType, id, resp.StatusCode)
		}
		var payload struct {
			Versions []versionWire `json:"versions"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("history decode(%s/%s): %v", docType, id, err)
		}
		return payload.Versions
	}

	// --- Run 1: write data, capture histories ---
	preHist := make(map[string][]versionWire, len(pairs))
	{
		info, cleanup := startServerAt(t, bin, dataDir)
		base := baseURL(info)

		for _, p := range pairs {
			// First mutation goes through POST (Create); subsequent
			// through PUT (Update). Yields seq=1..N for each pair.
			for i, body := range p.muts {
				method, url := "PUT", fmt.Sprintf("%s/%s/%s", base, p.docType, p.id)
				if i == 0 {
					method, url = "POST", fmt.Sprintf("%s/%s/", base, p.docType)
				}
				resp, err := authedDo(info.Token, method, url, "application/json",
					bytes.NewReader([]byte(body)))
				if err != nil {
					cleanup()
					t.Fatalf("mutate %s/%s [%d]: %v", p.docType, p.id, i, err)
				}
				resp.Body.Close()
				wantStatus := http.StatusOK
				if i == 0 {
					wantStatus = http.StatusCreated
				}
				if resp.StatusCode != wantStatus {
					cleanup()
					t.Fatalf("mutate %s/%s [%d]: expected %d, got %d", p.docType, p.id, i, wantStatus, resp.StatusCode)
				}
			}
		}

		for _, p := range pairs {
			h := fetchHistory(base, p.docType, p.id)
			if len(h) != len(p.muts) {
				cleanup()
				t.Fatalf("pre-restart history(%s/%s) length: want %d got %d", p.docType, p.id, len(p.muts), len(h))
			}
			preHist[p.docType+":"+p.id] = h
		}

		// Cleanly shut the server down so the SQLite file is fully
		// flushed before the next run reopens it. /shutdown returns
		// even if the connection drops mid-write.
		req, _ := http.NewRequest("POST", base+"/shutdown", nil)
		req.Header.Set("Authorization", "Bearer "+info.ShutdownToken)
		client := &http.Client{Timeout: 2 * time.Second}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
		cleanup()
	}

	// Sanity: data dir still has the SQLite file at the expected
	// location. If --data semantics ever drift this test starts
	// catching it before it ships.
	if _, err := os.Stat(filepath.Join(dataDir, "documents.db")); err != nil {
		t.Fatalf("expected SQLite file under --data: %v", err)
	}

	// --- Run 2: fresh server, same dir, fetch + compare ---
	{
		info, cleanup := startServerAt(t, bin, dataDir)
		defer cleanup()
		base := baseURL(info)

		for _, p := range pairs {
			got := fetchHistory(base, p.docType, p.id)
			want := preHist[p.docType+":"+p.id]

			if len(got) != len(want) {
				t.Fatalf("post-restart history(%s/%s) length: want %d got %d", p.docType, p.id, len(want), len(got))
			}
			for i := range got {
				// Wire-shape equality: version, data bytes, timestamp,
				// operation must all round-trip across restart. We
				// compare data via JSON-equivalence (RawMessage equality
				// is byte-equality which is OK here because we control
				// both sides) and the rest via field-equality.
				if got[i].Version != want[i].Version {
					t.Errorf("history(%s/%s)[%d].version: want %d got %d",
						p.docType, p.id, i, want[i].Version, got[i].Version)
				}
				if got[i].Operation != want[i].Operation {
					t.Errorf("history(%s/%s)[%d].operation: want %q got %q",
						p.docType, p.id, i, want[i].Operation, got[i].Operation)
				}
				if got[i].Timestamp != want[i].Timestamp {
					t.Errorf("history(%s/%s)[%d].timestamp: want %q got %q",
						p.docType, p.id, i, want[i].Timestamp, got[i].Timestamp)
				}
				if !reflect.DeepEqual([]byte(got[i].Data), []byte(want[i].Data)) {
					t.Errorf("history(%s/%s)[%d].data: want %s got %s",
						p.docType, p.id, i, string(want[i].Data), string(got[i].Data))
				}
			}
		}
	}
}
