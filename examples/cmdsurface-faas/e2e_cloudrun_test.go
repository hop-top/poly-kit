//go:build e2e && !windows

package faas_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestCloudRunBinaryServesEverySurface runs the built Cloud Run binary
// the way Cloud Run does — $PORT set, SIGTERM to stop — and drives each
// surface it mounts over the wire: the REST projection, SSE, and MCP
// through the CloudRunConfig.Mounts seam. The process must exit 0 on
// SIGTERM.
func TestCloudRunBinaryServesEverySurface(t *testing.T) {
	bin := t.TempDir() + "/cloudrun"
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin,
		"hop.top/kit/examples/cmdsurface-faas/cmd/cloudrun")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	port := freePort(t)
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	exited := make(chan error, 1)
	// Kill is a no-op error once the process has exited.
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stderr)
		signaled := false
		for sc.Scan() {
			if !signaled && strings.Contains(sc.Text(), "ready on") {
				close(ready)
				signaled = true
			}
		}
		exited <- cmd.Wait()
	}()
	select {
	case <-ready:
	case err := <-exited:
		t.Fatalf("binary exited before ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("binary not ready within 15s")
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// REST projection: ping declares no side effect, so it is a POST.
	resp, err := http.Post(base+"/v1/commands/ping", "application/json", nil)
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pong") {
		t.Errorf("POST /v1/commands/ping: status=%d body=%s", resp.StatusCode, body)
	}

	// SSE.
	resp, err = http.Get(base + "/cmd/ping/stream")
	if err != nil {
		t.Fatalf("sse: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "event: result") {
		t.Errorf("SSE stream lacks the result frame: %s", body)
	}

	// MCP, mounted through CloudRunConfig.Mounts.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "faas-e2e", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("mcp tools/call ping: %v", err)
	}
	raw, _ := json.Marshal(res.Content)
	if res.IsError || !strings.Contains(string(raw), "pong") {
		t.Errorf("mcp ping: isError=%v content=%s", res.IsError, raw)
	}
	_ = sess.Close()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v, want 0", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("binary did not exit within 15s of SIGTERM")
	}
}

// freePort asks the OS for a free loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
