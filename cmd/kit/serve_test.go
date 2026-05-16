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
	"testing"
	"time"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kit-test")
	cmd := exec.Command("go", "build", "-mod=mod", "-buildvcs=false", "-o", bin, "./cmd/kit/")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %s\n%s", err, out)
	}
	return bin
}

type serverInfo struct {
	Port          int    `json:"port"`
	PID           int    `json:"pid"`
	Token         string `json:"token"`
	ShutdownToken string `json:"shutdown_token"`
}

func startServer(t *testing.T, bin string) (serverInfo, context.CancelFunc) {
	t.Helper()
	dataDir := t.TempDir()
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

func baseURL(info serverInfo) string {
	return fmt.Sprintf("http://127.0.0.1:%d", info.Port)
}

func TestServeE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}

	bin := buildBinary(t)
	info, cleanup := startServer(t, bin)
	defer cleanup()

	base := baseURL(info)
	client := &http.Client{Timeout: 5 * time.Second}
	authPost := func(url, contentType string, body io.Reader) (*http.Response, error) {
		req, _ := http.NewRequest("POST", url, body)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+info.Token)
		return client.Do(req)
	}
	authPut := func(url string, body io.Reader) (*http.Response, error) {
		req, _ := http.NewRequest("PUT", url, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+info.Token)
		return client.Do(req)
	}
	authDelete := func(url string) (*http.Response, error) {
		req, _ := http.NewRequest("DELETE", url, nil)
		req.Header.Set("Authorization", "Bearer "+info.Token)
		return client.Do(req)
	}

	t.Run("health", func(t *testing.T) {
		resp, err := client.Get(base + "/health")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	var docID string
	t.Run("create", func(t *testing.T) {
		body := []byte(`{"title":"hello","body":"world"}`)
		resp, err := authPost(base+"/notes/", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
		var doc map[string]any
		json.NewDecoder(resp.Body).Decode(&doc)
		id, ok := doc["id"].(string)
		if !ok || id == "" {
			t.Fatal("missing id in response")
		}
		docID = id
	})

	t.Run("get", func(t *testing.T) {
		resp, err := client.Get(base + "/notes/" + docID)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var doc map[string]any
		json.NewDecoder(resp.Body).Decode(&doc)
		if doc["id"] != docID {
			t.Fatalf("expected id %s, got %v", docID, doc["id"])
		}
	})

	t.Run("list", func(t *testing.T) {
		resp, err := client.Get(base + "/notes/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var docs []map[string]any
		json.NewDecoder(resp.Body).Decode(&docs)
		if len(docs) < 1 {
			t.Fatal("expected at least one document")
		}
	})

	t.Run("update", func(t *testing.T) {
		body := []byte(`{"title":"updated","body":"content"}`)
		resp, err := authPut(base+"/notes/"+docID, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("delete", func(t *testing.T) {
		resp, err := authDelete(base + "/notes/" + docID)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			t.Fatalf("expected 204, got %d", resp.StatusCode)
		}
	})

	t.Run("get_after_delete", func(t *testing.T) {
		resp, err := client.Get(base + "/notes/" + docID)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("capabilities", func(t *testing.T) {
		resp, err := client.Get(base + "/capabilities")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("post_no_token_returns_401", func(t *testing.T) {
		body := []byte(`{"title":"unauth","body":"test"}`)
		req, _ := http.NewRequest("POST", base+"/notes/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer wrong-token")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("expected 401 for POST with wrong token, got %d", resp.StatusCode)
		}
	})

	t.Run("post_with_valid_token", func(t *testing.T) {
		body := []byte(`{"title":"authed","body":"ok"}`)
		req, _ := http.NewRequest("POST", base+"/notes/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+info.Token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Fatalf("expected 201 for POST with valid token, got %d", resp.StatusCode)
		}
	})

	t.Run("shutdown_no_token", func(t *testing.T) {
		resp, err := client.Post(base+"/shutdown", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Shutdown — server may close connection before response is fully read.
	t.Run("shutdown", func(t *testing.T) {
		req, _ := http.NewRequest("POST", base+"/shutdown", nil)
		req.Header.Set("Authorization", "Bearer "+info.ShutdownToken)
		resp, err := client.Do(req)
		if err != nil {
			// Connection reset/EOF is acceptable — server shut down.
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", resp.StatusCode)
		}
	})
}
