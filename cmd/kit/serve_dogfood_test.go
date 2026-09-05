package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kit dogfoods the serve capability it ships: the document engine is
// the api service, the socket service is registered, and kit's own
// command tree is projected under the same gates every adopter gets.
// These tests drive the built binary the way the SDK sidecars and an
// operator do.

func TestKitServe_ListsKitShippedServices(t *testing.T) {
	if testing.Short() {
		t.Skip("requires building the kit binary")
	}
	bin := buildBinary(t)

	cmd := exec.Command(bin, "serve", "--list")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Regexp(t, `(?m)^api\s`, string(out))
	assert.Regexp(t, `(?m)^socket\s`, string(out))
}

func TestKitServe_ProjectsKitCommandsBehindTheGates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}
	bin := buildBinary(t)
	info, cleanup := startServer(t, bin)
	defer cleanup()
	base := baseURL(info)
	client := &http.Client{Timeout: 5 * time.Second}

	// Discovery describes kit's own tree, and the reflector withholds
	// what must never run inside a served invocation.
	resp, err := client.Get(base + "/v1/commands")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var doc struct {
		Tool     string `json:"tool"`
		Commands []struct {
			Name      string `json:"name"`
			Invocable bool   `json:"invocable"`
			Reason    string `json:"reason"`
		} `json:"commands"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	assert.Equal(t, "kit", doc.Tool)
	got := map[string]struct {
		Invocable bool
		Reason    string
	}{}
	for _, c := range doc.Commands {
		got[c.Name] = struct {
			Invocable bool
			Reason    string
		}{c.Invocable, c.Reason}
	}
	assert.Equal(t, "self-hosting", got["serve"].Reason, "kit's serve is the process being talked to")
	assert.Equal(t, "self-hosting", got["symlink"].Reason, "symlink relinks the binary that is serving")
	assert.Equal(t, "self-hosting", got["conformance svc serve"].Reason, "a nested serve starts a server of its own")
	for _, c := range doc.Commands {
		assert.NotEmpty(t, c.Name, "discovery describes no nameless command: %+v", c)
	}
	assert.Equal(t, "management-only", got["status"].Reason)
	assert.Equal(t, "unauthorized-destructive", got["telemetry reset"].Reason)
	assert.True(t, got["config paths"].Invocable, "a read command of kit's own is served")

	// A read command runs over REST: reads are open, as the engine's
	// rule has always had it.
	resp2, err := client.Get(base + "/v1/commands/config/paths")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	var res struct {
		ExitCode int `json:"exit_code"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&res))
	assert.Equal(t, 0, res.ExitCode)

	// Withheld commands have no route. On kit the engine's document
	// routes own the first path segment (`/{type}/...`), so a request
	// for a withheld command under /v1/commands is answered by them as
	// document type "v1" rather than by a 404 — discovery above is the
	// authority. What must hold is that no such request runs the
	// command.
	for _, path := range []string{"serve", "symlink", "telemetry/reset"} {
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/commands/"+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+info.Token)
		r, err := client.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		assert.NotEqual(t, http.StatusOK, r.StatusCode, path)
		assert.NotContains(t, string(body), "exit_code", "%s must not run: %s", path, body)
	}

	// The engine's own routes keep their paths and their capabilities
	// listing.
	resp3, err := client.Get(base + "/capabilities")
	require.NoError(t, err)
	defer func() { _ = resp3.Body.Close() }()
	require.Equal(t, http.StatusOK, resp3.StatusCode)
	var caps struct {
		Capabilities []struct {
			Path string `json:"path"`
		} `json:"capabilities"`
	}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&caps))
	var paths []string
	for _, c := range caps.Capabilities {
		paths = append(paths, c.Path)
	}
	assert.Contains(t, paths, "/{type}/")
	assert.Contains(t, paths, "/{type}/{id}/history")
	assert.Contains(t, paths, "/shutdown")
}

func TestKitServe_PortBindsLoopbackAndShutdownIsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}
	bin := buildBinary(t)
	dataDir := t.TempDir()
	cmd := exec.Command(bin, "serve", "--port", "0", "--data", dataDir, "--no-peer", "--no-sync")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataDir, "HOME="+t.TempDir())
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())

	sc := bufio.NewScanner(stdout)
	require.True(t, sc.Scan(), "startup JSON: %s", stderr.String())
	var info serverInfo
	require.NoError(t, json.Unmarshal(sc.Bytes(), &info), sc.Text())
	assert.Equal(t, cmd.Process.Pid, info.PID, "the startup line names the serving process")
	assert.NotEmpty(t, info.Token)
	assert.NotEmpty(t, info.ShutdownToken)

	// --port maps onto the api service's loopback address; the bound
	// port is the one the readiness event carried.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(info.Port)), 2*time.Second)
	require.NoError(t, err)
	_ = conn.Close()

	req, _ := http.NewRequest(http.MethodPost, baseURL(info)+"/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+info.ShutdownToken)
	r, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err == nil {
		_ = r.Body.Close()
		assert.Equal(t, http.StatusNoContent, r.StatusCode)
	}
	waitErr := cmd.Wait()
	assert.NoError(t, waitErr, "a /shutdown stop is a clean stop: %s", stderr.String())
	assert.Equal(t, 0, cmd.ProcessState.ExitCode())

	// The startup line rides on the lifecycle trace rather than a
	// scraped banner.
	assert.Contains(t, stderr.String(), "ready_reported")
	assert.Contains(t, stderr.String(), "address=127.0.0.1:"+strconv.Itoa(info.Port))
}

func TestKitServe_SocketServesKitCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E in short mode")
	}
	bin := buildBinary(t)
	dir, err := os.MkdirTemp("", "ks")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "k.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "socket", "--socket", sock)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	var stderr strings.Builder
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	var conn net.Conn
	deadline := time.Now().Add(20 * time.Second)
	for {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket never came up: %s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer func() { _ = conn.Close() }()

	require.NoError(t, json.NewEncoder(conn).Encode(map[string]any{"path": []string{"config", "paths"}}))
	var resp struct {
		Ok     bool `json:"ok"`
		Result struct {
			ExitCode int `json:"exit_code"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	assert.True(t, resp.Ok)
	assert.Equal(t, 0, resp.Result.ExitCode)

	// kit's serve is never reachable from inside a served invocation.
	require.NoError(t, json.NewEncoder(conn).Encode(map[string]any{"path": []string{"serve"}}))
	var refused struct {
		Ok    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(conn).Decode(&refused))
	assert.False(t, refused.Ok)
	assert.Equal(t, "NOT_FOUND", refused.Error.Code)
}
