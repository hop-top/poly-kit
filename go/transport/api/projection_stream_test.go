package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// streamExecutor is a stubExecutor that also streams. Each call to
// OpenStream returns the configured refusal, or a stream whose Run
// is the configured function.
type streamExecutor struct {
	stubExecutor

	mu       sync.Mutex
	openErr  error
	run      func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error)
	opened   int
	ran      int
	gotReq   api.CommandRequest
	runCtxCh chan context.Context
}

func (s *streamExecutor) OpenStream(_ context.Context, req api.CommandRequest) (api.CommandStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened++
	s.gotReq = req
	if s.openErr != nil {
		return nil, s.openErr
	}
	return streamFunc(func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		s.mu.Lock()
		s.ran++
		ch := s.runCtxCh
		s.mu.Unlock()
		if ch != nil {
			ch <- ctx
		}
		return s.run(ctx, events)
	}), nil
}

func (s *streamExecutor) counts() (opened, ran int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened, s.ran
}

type streamFunc func(context.Context, chan<- api.CommandEvent) (api.CommandResult, error)

func (f streamFunc) Run(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
	return f(ctx, events)
}

// emitLines returns a Run that sends each line as a stdout event and
// then exits with code.
func emitLines(code int, lines ...string) func(context.Context, chan<- api.CommandEvent) (api.CommandResult, error) {
	return func(_ context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		for _, l := range lines {
			events <- api.CommandEvent{Kind: "stdout", Data: l, At: time.Now()}
		}
		return api.CommandResult{ExitCode: code, Stdout: strings.Join(lines, "\n")}, nil
	}
}

func streamRouter(t *testing.T, ex api.CommandExecutor, hb time.Duration, mw ...api.Middleware) *api.Router {
	t.Helper()
	var opts []api.RouterOption
	if len(mw) > 0 {
		opts = append(opts, api.WithMiddleware(mw...))
	}
	r := api.NewRouter(opts...)
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors:     fixtureDescriptors(),
		Executor:        ex,
		ToolName:        "fix",
		StreamHeartbeat: hb,
	})
	return r
}

// sseFrame is one parsed server-sent event, or a comment.
type sseFrame struct {
	Event   string
	Data    string
	Comment bool
}

// readFrames parses every frame until EOF.
func readFrames(t *testing.T, body io.Reader) []sseFrame {
	t.Helper()
	var out []sseFrame
	sc := bufio.NewScanner(body)
	var cur sseFrame
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur != (sseFrame{}) {
				out = append(out, cur)
			}
			cur = sseFrame{}
		case strings.HasPrefix(line, ":"):
			cur.Comment = true
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.Data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func terminal(t *testing.T, frames []sseFrame) (string, map[string]any) {
	t.Helper()
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(last.Data), &body), last.Data)
	return last.Event, body
}

func TestStreamReadCommandIsGETWithQueryParams(t *testing.T) {
	ex := &streamExecutor{run: emitLines(0, "one", "two")}
	srv := httptest.NewServer(streamRouter(t, ex, time.Hour))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/commands/list/stream?filter=x&limit=2&arg=a")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))

	frames := readFrames(t, resp.Body)
	require.Len(t, frames, 3, "%+v", frames)
	for i, want := range []string{"one", "two"} {
		assert.Equal(t, "event", frames[i].Event)
		var ev api.CommandEvent
		require.NoError(t, json.Unmarshal([]byte(frames[i].Data), &ev))
		assert.Equal(t, "stdout", ev.Kind)
		assert.Equal(t, want, ev.Data)
	}
	name, res := terminal(t, frames)
	assert.Equal(t, "result", name)
	assert.EqualValues(t, 0, res["exit_code"])
	assert.EqualValues(t, http.StatusOK, res["status"])

	// The request decodes exactly as the request/reply route does.
	assert.Equal(t, int64(2), ex.gotReq.Flags["limit"])
	assert.Equal(t, "x", ex.gotReq.Flags["filter"])
	assert.Equal(t, []string{"a"}, ex.gotReq.Args)
	assert.Equal(t, []string{"list"}, ex.gotReq.Path)
}

func TestStreamWriteCommandIsPOSTWithBody(t *testing.T) {
	ex := &streamExecutor{run: emitLines(0, "added")}
	h := streamRouter(t, ex, time.Hour)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/widget/add/stream", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"a write streams on POST, like its request/reply route")

	req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/add/stream",
		strings.NewReader(`{"flags":{"force":true},"args":["gadget"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, ex.gotReq.Flags["force"])
	assert.Equal(t, []string{"gadget"}, ex.gotReq.Args)
	name, res := terminal(t, readFrames(t, rec.Body))
	assert.Equal(t, "result", name)
	assert.EqualValues(t, 0, res["exit_code"])
}

func TestStreamGateRefusalsAreStatusesNotStreams(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"permission", fmt.Errorf("%w: missing scope admin", api.ErrPermissionDenied), http.StatusForbidden, api.CodePermissionDenied},
		{"destructive", fmt.Errorf("%w: widget add on rest", api.ErrDestructiveBlocked), http.StatusForbidden, api.CodeDestructiveBlocked},
		{"not invocable", api.ErrCommandNotInvocable, http.StatusNotFound, api.CodeNotInvocable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := &streamExecutor{openErr: c.err, run: emitLines(0, "never")}
			rec := httptest.NewRecorder()
			streamRouter(t, ex, time.Hour).ServeHTTP(rec,
				httptest.NewRequest(http.MethodGet, "/v1/commands/list/stream?filter=x", nil))

			assert.Equal(t, c.status, rec.Code)
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			var ae api.APIError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ae), rec.Body.String())
			assert.Equal(t, c.code, ae.Code)
			_, ran := ex.counts()
			assert.Zero(t, ran, "a refused stream must not run")
		})
	}
}

func TestStreamMalformedRequestIs400BeforeTheGate(t *testing.T) {
	ex := &streamExecutor{run: emitLines(0)}
	req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/add/stream",
		strings.NewReader(`{"flags":{"bogus":1},"args":["g"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	streamRouter(t, ex, time.Hour).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	opened, _ := ex.counts()
	assert.Zero(t, opened)
}

func TestStreamCommandRefusalIsTheTerminalFrame(t *testing.T) {
	// The confirmation gate is the command's own: it refuses by
	// exiting, so its verdict exists only once the command has run.
	// The stream carries it in the terminal frame with the status the
	// request/reply route answers.
	ex := &streamExecutor{run: func(_ context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		events <- api.CommandEvent{Kind: "stderr", Data: "confirmation required"}
		return api.CommandResult{ExitCode: 7, Stderr: "confirmation required\n"}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/commands/widget/delete/stream", nil)
	rec := httptest.NewRecorder()
	streamRouter(t, ex, time.Hour).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	name, res := terminal(t, readFrames(t, rec.Body))
	assert.Equal(t, "result", name)
	assert.EqualValues(t, 7, res["exit_code"])
	assert.EqualValues(t, api.StatusForExitCode(7), res["status"])
	assert.EqualValues(t, http.StatusForbidden, res["status"])
}

func TestStreamRunnerErrorWithoutOutputIsAnErrorFrame(t *testing.T) {
	// Admitted means committed: a run that fails before writing
	// anything still ends the stream with a terminal frame, mapped
	// as the request/reply route maps the same error.
	ex := &streamExecutor{run: func(context.Context, chan<- api.CommandEvent) (api.CommandResult, error) {
		return api.CommandResult{}, fmt.Errorf("%w: raced", api.ErrCommandNotInvocable)
	}}
	rec := httptest.NewRecorder()
	streamRouter(t, ex, time.Hour).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/commands/list/stream?filter=x", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	name, body := terminal(t, readFrames(t, rec.Body))
	assert.Equal(t, "error", name)
	assert.EqualValues(t, api.StatusNotInvocable, body["status"])
	assert.Equal(t, api.CodeNotInvocable, body["code"])
}

func TestStreamFailureAfterOutputIsTheTerminalFrame(t *testing.T) {
	ex := &streamExecutor{run: emitLines(1, "partial")}
	rec := httptest.NewRecorder()
	streamRouter(t, ex, time.Hour).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/commands/list/stream?filter=x", nil))

	require.Equal(t, http.StatusOK, rec.Code, "the stream had begun")
	name, res := terminal(t, readFrames(t, rec.Body))
	assert.Equal(t, "result", name)
	assert.EqualValues(t, 1, res["exit_code"])
	assert.EqualValues(t, http.StatusInternalServerError, res["status"],
		"the terminal frame maps the exit code as the request/reply route would")
}

func TestStreamRunnerErrorAfterOutputIsAnErrorFrame(t *testing.T) {
	ex := &streamExecutor{run: func(_ context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		events <- api.CommandEvent{Kind: "stdout", Data: "x"}
		return api.CommandResult{}, errors.New("runner exploded")
	}}
	rec := httptest.NewRecorder()
	streamRouter(t, ex, time.Hour).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v1/commands/list/stream?filter=x", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	name, body := terminal(t, readFrames(t, rec.Body))
	assert.Equal(t, "error", name)
	assert.EqualValues(t, http.StatusInternalServerError, body["status"])
	// Mapped as the request/reply route maps it: an unclassified
	// error does not leak its text.
	assert.Equal(t, api.MapError(errors.New("runner exploded")).Message, body["message"])
}

func TestStreamClientDisconnectCancelsTheCommand(t *testing.T) {
	// Both methods: a POST's body must not keep the server from
	// noticing the client left.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/commands/list/stream?filter=x", ""},
		{http.MethodPost, "/v1/commands/widget/add/stream", `{"args":["g"]}`},
	} {
		t.Run(c.method, func(t *testing.T) {
			canceled := make(chan error, 1)
			ex := &streamExecutor{
				run: func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
					events <- api.CommandEvent{Kind: "stdout", Data: "started"}
					<-ctx.Done()
					canceled <- ctx.Err()
					return api.CommandResult{ExitCode: 1}, ctx.Err()
				},
			}
			srv := httptest.NewServer(streamRouter(t, ex, time.Hour))
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			var body io.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			}
			req, err := http.NewRequestWithContext(ctx, c.method, srv.URL+c.path, body)
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			// Read the first frame so the command is known to be running.
			line, err := bufio.NewReader(resp.Body).ReadString('\n')
			require.NoError(t, err)
			assert.Equal(t, "event: event\n", line)

			cancel()
			_ = resp.Body.Close()

			select {
			case err := <-canceled:
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("disconnect never reached the command")
			}
		})
	}
}

func TestStreamHeartbeatKeepsAnIdleStreamAlive(t *testing.T) {
	release := make(chan struct{})
	ex := &streamExecutor{run: func(ctx context.Context, _ chan<- api.CommandEvent) (api.CommandResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return api.CommandResult{ExitCode: 0}, nil
	}}
	srv := httptest.NewServer(streamRouter(t, ex, 20*time.Millisecond))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/commands/list/stream?filter=x")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	// Headers arrive while the command is still silent, and the
	// keep-alive is the first thing on the wire.
	require.Equal(t, http.StatusOK, resp.StatusCode)
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, ": ping\n", line)

	close(release)
	frames := readFrames(t, br)
	name, res := terminal(t, frames)
	assert.Equal(t, "result", name)
	assert.EqualValues(t, http.StatusOK, res["status"])
}

func TestStreamOutlivesServerReadAndWriteTimeouts(t *testing.T) {
	// A stream is long-lived by definition; the server's per-request
	// deadlines, sized for request/reply, must not cut it or cancel
	// the command.
	ex := &streamExecutor{run: func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		for i := range 6 {
			select {
			case <-ctx.Done():
				return api.CommandResult{ExitCode: 1}, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			events <- api.CommandEvent{Kind: "stdout", Data: fmt.Sprint(i)}
		}
		return api.CommandResult{}, nil
	}}
	// Behind the logger, whose wrapper must pass the deadline
	// controls through.
	srv := httptest.NewUnstartedServer(streamRouter(t, ex, time.Hour,
		api.Logger(func(any, ...any) {})))
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/commands/list/stream?filter=x")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	frames := readFrames(t, resp.Body)
	require.Len(t, frames, 7, "%+v", frames)
	name, res := terminal(t, frames)
	assert.Equal(t, "result", name)
	assert.EqualValues(t, 0, res["exit_code"])

	// The request/reply route keeps working beside it.
	resp2, err := http.Get(srv.URL + "/v1/commands/list?filter=x")
	require.NoError(t, err)
	_ = resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestStreamFlushesThroughLoggerMiddleware(t *testing.T) {
	// The logger wraps the ResponseWriter; streaming must still
	// reach the client frame by frame.
	var logged []any
	var mu sync.Mutex
	logger := api.Logger(func(msg any, kv ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, msg)
	})
	ex := &streamExecutor{
		runCtxCh: make(chan context.Context, 1),
		run: func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
			events <- api.CommandEvent{Kind: "stdout", Data: "first"}
			<-ctx.Done()
			return api.CommandResult{}, ctx.Err()
		},
	}
	srv := httptest.NewServer(streamRouter(t, ex, time.Hour, logger))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/commands/list/stream?filter=x", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err, "the first frame must be flushed while the command still runs")
	assert.Equal(t, "event: event\n", line)
}

func TestStreamRoutesNeedAStreamingExecutor(t *testing.T) {
	// A request/reply-only executor gets no stream routes, and
	// discovery does not advertise any.
	h := newProjectedRouter(t, &stubExecutor{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list/stream?filter=x", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	doc := api.BuildDiscoveryDocument(api.ProjectionConfig{
		Descriptors: fixtureDescriptors(), Executor: &stubExecutor{},
	})
	for _, e := range doc.Commands {
		assert.Empty(t, e.StreamRoute, e.Name)
	}
}

func TestDiscoveryAdvertisesStreamRouteForInvocableCommands(t *testing.T) {
	doc := api.BuildDiscoveryDocument(api.ProjectionConfig{
		Descriptors: fixtureDescriptors(), Executor: &streamExecutor{},
	})
	byName := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		byName[e.Name] = e
	}
	assert.Equal(t, "/v1/commands/list/stream", byName["list"].StreamRoute)
	assert.Equal(t, "/v1/commands/widget/add/stream", byName["widget add"].StreamRoute)
	assert.Empty(t, byName["shell"].StreamRoute, "a withheld command has no stream either")
}

func TestStreamRouteAndOperationID(t *testing.T) {
	assert.Equal(t, "/v1/commands/widget/add/stream", api.StreamRouteFor([]string{"widget", "add"}))
	assert.Equal(t, "stream_commands_widget_add", api.StreamOperationIDFor([]string{"widget", "add"}))
	// No command operation starts with "stream_", so a command named
	// add-stream cannot collide with add's stream operation.
	assert.NotEqual(t, api.OperationIDFor([]string{"widget", "add-stream"}),
		api.StreamOperationIDFor([]string{"widget", "add"}))
}

func TestOpenAPIDescribesStreamOperations(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "Fixture", Version: "1.0.0"}))
	cfg := api.ProjectionConfig{
		Descriptors: fixtureDescriptors(), Executor: &streamExecutor{}, ToolName: "fix",
	}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg)
	paths := fetchSpec(t, r)["paths"].(map[string]any)

	for path, method := range map[string]string{
		"/v1/commands/list/stream":       "get",
		"/v1/commands/widget/add/stream": "post",
	} {
		entry, ok := paths[path].(map[string]any)
		require.True(t, ok, "spec must describe %s", path)
		op, ok := entry[method].(map[string]any)
		require.True(t, ok, "%s must be %s", path, method)
		ok200 := op["responses"].(map[string]any)["200"].(map[string]any)
		assert.Contains(t, ok200["content"], "text/event-stream", path)
	}
	assert.NotContains(t, paths, "/v1/commands/shell/stream")

	// The minimal spec lists them too.
	min := api.NewRouter()
	api.MountCommandProjection(min, cfg)
	api.MountMinimalProjectionSpec(min, cfg)
	mpaths := fetchSpec(t, min)["paths"].(map[string]any)
	require.Contains(t, mpaths, "/v1/commands/list/stream")
	assert.Contains(t, mpaths["/v1/commands/list/stream"], "get")
	require.Contains(t, mpaths, "/v1/commands/widget/add/stream")
	assert.Contains(t, mpaths["/v1/commands/widget/add/stream"], "post")
}

func TestStreamEndsWhenTheServerStops(t *testing.T) {
	// A draining server waits for in-flight requests, and a stream
	// has no end of its own. Closing Stopping ends it: the command is
	// canceled and the client is told why.
	canceled := make(chan error, 1)
	ex := &streamExecutor{run: func(ctx context.Context, events chan<- api.CommandEvent) (api.CommandResult, error) {
		events <- api.CommandEvent{Kind: "stdout", Data: "started"}
		<-ctx.Done()
		canceled <- ctx.Err()
		return api.CommandResult{ExitCode: 1}, ctx.Err()
	}}
	stopping := make(chan struct{})
	r := api.NewRouter()
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: fixtureDescriptors(), Executor: ex, Stopping: stopping,
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/commands/list/stream?filter=x")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "event: event\n", line)

	close(stopping)
	select {
	case err := <-canceled:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("stopping never reached the command")
	}
	name, body := terminal(t, readFrames(t, br))
	assert.Equal(t, "error", name)
	assert.EqualValues(t, http.StatusServiceUnavailable, body["status"])
	assert.Equal(t, api.CodeShuttingDown, body["code"])
}
