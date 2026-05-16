package osnotifysink

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// fakeRunner records the (name, args) of each Run call and returns
// the configured err. Safe for concurrent use; tests are sequential
// but the mutex is cheap insurance against future parallelism.
type fakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
	err   error
}

type fakeCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// copy args to detach from caller-owned slice
	cp := make([]string, len(args))
	copy(cp, args)
	f.calls = append(f.calls, fakeCall{name: name, args: cp})
	return f.err
}

func (f *fakeRunner) lastCall(t *testing.T) fakeCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "fakeRunner: no calls recorded")
	return f.calls[len(f.calls)-1]
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newSink builds a *Sink directly with a fixed platform, bypassing
// New's runtime.GOOS probe. Used by every test that wants to assert
// against both darwin and linux command construction regardless of
// where the test happens to run.
func newSink(plat platform, opts ...Option) *Sink {
	o := defaultOpts()
	for _, opt := range opts {
		opt(&o)
	}
	if o.runner == nil {
		o.runner = execRunner{}
	}
	return &Sink{
		plat:      plat,
		runner:    o.runner,
		titleTmpl: o.title,
		textTmpl:  o.text,
		redactor:  o.redactor,
		breaker:   o.breaker,
	}
}

func sampleEvent() bus.Event {
	return bus.Event{
		Topic:     "kit.notify.test",
		Source:    "test",
		Timestamp: time.Unix(0, 0).UTC(),
		Payload:   map[string]any{"msg": "hello"},
	}
}

// TestNew_Darwin_ReturnsSink only runs on darwin: New must succeed
// without probing osascript.
func TestNew_Darwin_ReturnsSink(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	sink, err := New(WithTitle(LiteralTemplate("t")), WithText(LiteralTemplate("x")))
	require.NoError(t, err)
	require.NotNil(t, sink)
}

// TestNew_Linux_NotifySendMissing_Errors stubs PATH so notify-send
// cannot be located. linux-only because that's where New probes.
func TestNew_Linux_NotifySendMissing_Errors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	t.Setenv("PATH", "")
	sink, err := New()
	assert.Nil(t, sink)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notify-send")
}

// TestNew_Linux_NotifySendPresent_ReturnsSink confirms construction
// succeeds on linux when notify-send (or a stub) is on PATH.
func TestNew_Linux_NotifySendPresent_ReturnsSink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}
	dir := t.TempDir()
	stub := dir + "/notify-send"
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", dir)

	sink, err := New(WithTitle(LiteralTemplate("t")), WithText(LiteralTemplate("x")))
	require.NoError(t, err)
	require.NotNil(t, sink)
}

// TestNew_Windows_Errors only runs on windows. Other platforms can't
// directly assert this branch; coverage is best-effort.
func TestNew_Windows_Errors(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only")
	}
	sink, err := New()
	assert.Nil(t, sink)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows")
}

// TestCommand_Darwin asserts the osascript invocation shape. command
// takes (title, text); AppleScript's `display notification` syntax
// puts the body text first and the title last.
func TestCommand_Darwin(t *testing.T) {
	s := newSink(platformDarwin)
	name, args := s.command("the title", "the body")
	assert.Equal(t, "osascript", name)
	require.Len(t, args, 2)
	assert.Equal(t, "-e", args[0])
	assert.Equal(t, `display notification "the body" with title "the title"`, args[1])
}

// TestCommand_Linux asserts the notify-send invocation shape.
func TestCommand_Linux(t *testing.T) {
	s := newSink(platformLinux)
	name, args := s.command("hello", "world")
	assert.Equal(t, "notify-send", name)
	assert.Equal(t, []string{"hello", "world"}, args)
}

// TestDrain_Darwin_RunsCommand uses a fake runner; the captured call
// must have osascript + the AppleScript-escaped script.
func TestDrain_Darwin_RunsCommand(t *testing.T) {
	r := &fakeRunner{}
	s := newSink(platformDarwin,
		WithTitle(LiteralTemplate(`a "title" \ here`)),
		WithText(LiteralTemplate(`body`)),
		withRunner(r),
	)
	require.NoError(t, s.Drain(context.Background(), sampleEvent()))

	got := r.lastCall(t)
	assert.Equal(t, "osascript", got.name)
	require.Len(t, got.args, 2)
	assert.Equal(t, "-e", got.args[0])
	// title is the second AppleScript literal in `display notification … with title …`.
	expected := `display notification "body" with title "a \"title\" \\ here"`
	assert.Equal(t, expected, got.args[1])
}

// TestDrain_Linux_RunsCommand uses a fake runner; the captured call
// must be notify-send <title> <text>.
func TestDrain_Linux_RunsCommand(t *testing.T) {
	r := &fakeRunner{}
	s := newSink(platformLinux,
		WithTitle(LiteralTemplate("kit alert")),
		WithText(LiteralTemplate("queue depth high")),
		withRunner(r),
	)
	require.NoError(t, s.Drain(context.Background(), sampleEvent()))

	got := r.lastCall(t)
	assert.Equal(t, "notify-send", got.name)
	assert.Equal(t, []string{"kit alert", "queue depth high"}, got.args)
}

// TestDrain_NoTitle_Errors guards the runtime check: missing
// templates must surface as a Drain-time error, not a panic or nil
// command.
func TestDrain_NoTitle_Errors(t *testing.T) {
	r := &fakeRunner{}
	s := newSink(platformDarwin,
		WithText(LiteralTemplate("x")),
		withRunner(r),
	)
	err := s.Drain(context.Background(), sampleEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "templates required")
	assert.Equal(t, 0, r.callCount(), "runner must not be invoked when templates missing")
}

// TestDrain_NoText_Errors mirrors TestDrain_NoTitle_Errors for the
// text template.
func TestDrain_NoText_Errors(t *testing.T) {
	r := &fakeRunner{}
	s := newSink(platformDarwin,
		WithTitle(LiteralTemplate("x")),
		withRunner(r),
	)
	err := s.Drain(context.Background(), sampleEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "templates required")
}

// TestDrain_RunnerError_Propagated wraps the fake's error in the
// sink's "osnotify: run …" prefix.
func TestDrain_RunnerError_Propagated(t *testing.T) {
	want := errors.New("boom")
	r := &fakeRunner{err: want}
	s := newSink(platformLinux,
		WithTitle(LiteralTemplate("t")),
		WithText(LiteralTemplate("x")),
		withRunner(r),
	)
	err := s.Drain(context.Background(), sampleEvent())
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
	assert.Contains(t, err.Error(), "osnotify: run notify-send")
}

// TestDrain_Redactor_RunsOnTitleAndText proves redaction happens
// before the runner sees the strings.
func TestDrain_Redactor_RunsOnTitleAndText(t *testing.T) {
	red, err := redact.New().AddRule("secret", `topsecret`, "***")
	require.NoError(t, err)

	r := &fakeRunner{}
	s := newSink(platformLinux,
		WithTitle(LiteralTemplate("title topsecret")),
		WithText(LiteralTemplate("body topsecret end")),
		WithRedactor(red),
		withRunner(r),
	)
	require.NoError(t, s.Drain(context.Background(), sampleEvent()))

	got := r.lastCall(t)
	require.Len(t, got.args, 2)
	assert.NotContains(t, got.args[0], "topsecret")
	assert.NotContains(t, got.args[1], "topsecret")
}

// TestDrain_Breaker_OpenCircuit_PropagatesErrBrokenCircuit confirms a
// tripped breaker short-circuits before the runner.
func TestDrain_Breaker_OpenCircuit_PropagatesErrBrokenCircuit(t *testing.T) {
	const name = "osnotify-test-open"
	t.Cleanup(func() { breaker.Unregister(name) })
	b := breaker.New(name)
	b.Trip("test")

	r := &fakeRunner{}
	s := newSink(platformLinux,
		WithTitle(LiteralTemplate("t")),
		WithText(LiteralTemplate("x")),
		WithBreaker(b),
		withRunner(r),
	)
	err := s.Drain(context.Background(), sampleEvent())
	assert.ErrorIs(t, err, breaker.ErrBrokenCircuit)
	assert.Equal(t, 0, r.callCount(), "runner must not be invoked when breaker is open")
}

// TestDrain_Breaker_ClosedCircuit_AllowsCall confirms the happy path
// when the breaker is configured but closed.
func TestDrain_Breaker_ClosedCircuit_AllowsCall(t *testing.T) {
	const name = "osnotify-test-closed"
	t.Cleanup(func() { breaker.Unregister(name) })
	b := breaker.New(name)

	r := &fakeRunner{}
	s := newSink(platformLinux,
		WithTitle(LiteralTemplate("t")),
		WithText(LiteralTemplate("x")),
		WithBreaker(b),
		withRunner(r),
	)
	require.NoError(t, s.Drain(context.Background(), sampleEvent()))
	assert.Equal(t, 1, r.callCount())
}

// TestEscapeAppleScript covers the empty / plain / quoted / backslash
// cases. The output is always wrapped in surrounding double-quotes.
func TestEscapeAppleScript(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", `""`},
		{"plain", `"plain"`},
		{`with "quotes"`, `"with \"quotes\""`},
		{`back\slash`, `"back\\slash"`},
		{`mix \"both\"`, `"mix \\\"both\\\""`},
		{"unicode 你好", `"unicode 你好"`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, escapeAppleScript(tc.in))
		})
	}
}

// TestTextTemplate_RendersBusEvent covers a representative render
// against an event so future changes to bus.Event field names surface
// here as test failures.
func TestTextTemplate_RendersBusEvent(t *testing.T) {
	tmpl, err := TextTemplate(`{{.Source}}/{{.Topic}}`)
	require.NoError(t, err)
	got, err := tmpl.Render(sampleEvent())
	require.NoError(t, err)
	assert.Equal(t, "test/kit.notify.test", got)
}

// TestTextTemplate_ParseError surfaces template syntax errors at
// construction time, not Drain time.
func TestTextTemplate_ParseError(t *testing.T) {
	_, err := TextTemplate(`{{.Bad`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse template")
}

// TestLiteralTemplate confirms it ignores the event entirely.
func TestLiteralTemplate(t *testing.T) {
	tmpl := LiteralTemplate("constant")
	got, err := tmpl.Render(bus.Event{})
	require.NoError(t, err)
	assert.Equal(t, "constant", got)

	got2, err := tmpl.Render(sampleEvent())
	require.NoError(t, err)
	assert.Equal(t, "constant", got2)
}

// TestSink_Close is a sanity check on the no-op Close.
func TestSink_Close(t *testing.T) {
	s := newSink(platformLinux)
	assert.NoError(t, s.Close())
}
