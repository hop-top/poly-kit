package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// isolateHome points every per-user directory at a fresh temp dir so
// a served command cannot read or write the developer's config.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, home+"/"+strings.ToLower(k))
	}
}

// streamFrame is one parsed server-sent event.
type streamFrame struct {
	event string
	data  string
}

func readStream(t *testing.T, body io.Reader) []streamFrame {
	t.Helper()
	var out []streamFrame
	var cur streamFrame
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur != (streamFrame{}) {
				out = append(out, cur)
			}
			cur = streamFrame{}
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func doStream(t *testing.T, method, url, body string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, b
}

// addTail adds a read command that prints each of lines.
func addTail(r *Root, lines ...string) {
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "tail",
		Short: "print lines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, l := range lines {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), l)
			}
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
}

func TestAPIStreamRunsTheCommandOverSSE(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
	addTail(r, "one", "two")
	base, stop := serveAPI(t, r)
	defer stop()

	resp, body := doStream(t, http.MethodGet, base+"/v1/commands/tail/stream", "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	frames := readStream(t, strings.NewReader(string(body)))
	require.Len(t, frames, 3, string(body))
	var lines []string
	for _, f := range frames[:2] {
		require.Equal(t, "event", f.event)
		var ev api.CommandEvent
		require.NoError(t, json.Unmarshal([]byte(f.data), &ev))
		assert.Equal(t, "stdout", ev.Kind)
		lines = append(lines, fmt.Sprint(ev.Data))
	}
	assert.Equal(t, []string{"one", "two"}, lines)

	require.Equal(t, "result", frames[2].event)
	var res api.StreamResult
	require.NoError(t, json.Unmarshal([]byte(frames[2].data), &res))
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, http.StatusOK, res.Status)
	assert.Equal(t, "one\ntwo\n", res.Stdout)

	// Audited like any remote execution.
	inv, ares, err := rec.last(t)
	assert.NoError(t, err)
	assert.Equal(t, 0, ares.ExitCode)
	assert.Equal(t, []string{"tail"}, inv.Path)
	assert.Equal(t, cmdsurface.SurfaceREST, inv.Meta.Surface)
	assert.NotEmpty(t, inv.Meta.RequestID)
}

func TestAPIStreamAppliesTheSameGates(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	r := authRoot(
		t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0", Auth: bearer(map[string][]string{
			"alice": {"admin"}, "bob": {"widgets:read"},
		})}),
		WithPermission(scopeGate),
		WithAuditSinks(rec.spec()),
	)
	base, stop := serveAPI(t, r)
	defer stop()
	url := base + "/v1/commands/admin/reset/stream"

	// Unauthenticated: 401 before anything streams, audited against
	// the command the URL addresses, not a command named "stream".
	resp, body := doStream(t, http.MethodPost, url, `{}`, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
	inv, _, err := rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)
	assert.Equal(t, []string{"admin", "reset"}, inv.Path)

	// Authenticated, not permitted: 403 with the gate's reason.
	resp, body = doStream(t, http.MethodPost, url, `{}`, map[string]string{"Authorization": "Bearer bob"})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	var ae api.APIError
	require.NoError(t, json.Unmarshal(body, &ae), string(body))
	assert.Equal(t, api.CodePermissionDenied, ae.Code)
	assert.Contains(t, ae.Message, "missing scope admin")
	inv, _, err = rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrPermissionDenied)
	assert.Equal(t, "bob", inv.Meta.Caller)

	// Permitted: streams.
	resp, body = doStream(t, http.MethodPost, url, `{}`, map[string]string{"Authorization": "Bearer alice"})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	frames := readStream(t, strings.NewReader(string(body)))
	require.NotEmpty(t, frames)
	assert.Equal(t, "result", frames[len(frames)-1].event)
	assert.Contains(t, frames[len(frames)-1].data, "reset")

	// A write streams on POST only, like its request/reply route.
	resp, _ = doStream(t, http.MethodGet, url, "", map[string]string{"Authorization": "Bearer alice"})
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestAPIStreamWithholdsWhatRequestReplyWithholds(t *testing.T) {
	isolateHome(t)
	// Default policy: destructive commands are withheld from REST,
	// so neither route exists.
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	base, stop := serveAPI(t, r)
	defer stop()

	resp, _ := doStream(t, http.MethodPost, base+"/v1/commands/purge/stream", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = doStream(t, http.MethodPost, base+"/v1/commands/purge", `{}`, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAPIStreamCarriesTheConfirmationVerdict(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	policy := cmdsurface.Policy{AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST}}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0", Policy: policy}), WithAuditSinks(rec.spec()))
	base, stop := serveAPI(t, r)
	defer stop()

	// Unconfirmed: the command's own gate refuses. Its verdict is an
	// exit code, known once the command has run, so the stream
	// reports it in the terminal frame with the status the
	// request/reply route answers for the same call.
	rrResp, rrBody := doStream(t, http.MethodPost, base+"/v1/commands/purge", `{}`, nil)
	require.Equal(t, http.StatusForbidden, rrResp.StatusCode, string(rrBody))
	var want api.CommandResult
	require.NoError(t, json.Unmarshal(rrBody, &want))

	resp, body := doStream(t, http.MethodPost, base+"/v1/commands/purge/stream", `{}`, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	frames := readStream(t, strings.NewReader(string(body)))
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	require.Equal(t, "result", last.event, string(body))
	var got api.StreamResult
	require.NoError(t, json.Unmarshal([]byte(last.data), &got))
	assert.Equal(t, rrResp.StatusCode, got.Status)
	assert.Equal(t, want.ExitCode, got.ExitCode)
	assert.Contains(t, got.Stderr, "confirm")
	_, ares, _ := rec.last(t)
	assert.Equal(t, want.ExitCode, ares.ExitCode, "the refused run is audited with its exit code")

	// Confirmed: it runs.
	resp, body = doStream(t, http.MethodPost, base+"/v1/commands/purge/stream",
		`{"flags":{"confirm":"yes"}}`, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	frames = readStream(t, strings.NewReader(string(body)))
	require.NotEmpty(t, frames)
	last = frames[len(frames)-1]
	assert.Equal(t, "result", last.event)
	require.NoError(t, json.Unmarshal([]byte(last.data), &got))
	assert.Equal(t, http.StatusOK, got.Status)
	assert.Contains(t, got.Stdout, "purged")
}

func TestAPIStreamClientDisconnectCancelsTheCommand(t *testing.T) {
	isolateHome(t)
	rec := &auditRecorder{}
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithAuditSinks(rec.spec()))
	observed := make(chan error, 1)
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "watch",
		Short: "watch until canceled",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "watching")
			<-cmd.Context().Done()
			observed <- cmd.Context().Err()
			return cmd.Context().Err()
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	base, stop := serveAPI(t, r)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/commands/watch/stream", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: event\n", line)
	line, err = br.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, line, "watching")

	cancel()
	_ = resp.Body.Close()

	select {
	case err := <-observed:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the disconnect never reached the command")
	}

	// The canceled run is audited as a cancellation.
	require.Eventually(t, func() bool { return rec.count() > 0 }, 5*time.Second, 10*time.Millisecond)
	inv, _, aerr := rec.last(t)
	assert.Equal(t, []string{"watch"}, inv.Path)
	assert.ErrorIs(t, aerr, context.Canceled)
}

func TestAPIStreamDiscoveryAndSpecAdvertiseStreams(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	base, stop := serveAPI(t, r)
	defer stop()

	resp, body := get(t, base+"/v1/commands", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(body, &doc))
	for _, e := range doc.Commands {
		if e.Invocable {
			assert.Equal(t, e.Route+"/stream", e.StreamRoute, e.Name)
		} else {
			assert.Empty(t, e.StreamRoute, e.Name)
		}
	}

	resp, body = get(t, base+"/openapi.json", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var spec map[string]any
	require.NoError(t, json.Unmarshal(body, &spec))
	assert.Contains(t, spec["paths"], "/v1/commands/list/stream")
}

func TestAPIStreamOutlivesTheServerTimeouts(t *testing.T) {
	isolateHome(t)
	// The api service's server deadlines are sized for request/reply.
	// Served through the service's real handler — its full middleware
	// chain — on a server whose deadlines are far shorter than the
	// command, the stream must still run to its terminal frame.
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "slow",
		Short: "print slowly",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for i := range 5 {
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(80 * time.Millisecond):
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), i)
			}
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	h, err := apiSvc(t, r).buildHandler(t.Context())
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, body := doStream(t, http.MethodGet, srv.URL+"/v1/commands/slow/stream", "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	frames := readStream(t, strings.NewReader(string(body)))
	require.Len(t, frames, 6, string(body))
	last := frames[len(frames)-1]
	require.Equal(t, "result", last.event, string(body))
	var res api.StreamResult
	require.NoError(t, json.Unmarshal([]byte(last.data), &res))
	assert.Equal(t, 0, res.ExitCode)
}

func TestAPIStreamDoesNotHoldShutdown(t *testing.T) {
	isolateHome(t)
	r := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	observed := make(chan error, 1)
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "watch",
		Short: "watch until canceled",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "watching")
			<-cmd.Context().Done()
			observed <- cmd.Context().Err()
			return cmd.Context().Err()
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	base, stop := serveAPI(t, r)

	resp, err := http.Get(base + "/v1/commands/watch/stream")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: event\n", line)

	// Stopping the service ends the stream rather than waiting out
	// the stop timeout on a command that has no end.
	start := time.Now()
	stop()
	assert.Less(t, time.Since(start), 5*time.Second)
	select {
	case err := <-observed:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("stopping never reached the command")
	}
	rest, _ := io.ReadAll(br)
	assert.Contains(t, string(rest), "event: error")
	assert.Contains(t, string(rest), api.CodeShuttingDown)
}
