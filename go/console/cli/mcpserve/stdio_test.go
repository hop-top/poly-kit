package mcpserve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// stdioPeer is the host end of a stdio session: it writes requests on
// the service's input and reads every line the service writes to its
// output, failing on any line that is not a JSON-RPC message.
type stdioPeer struct {
	t     *testing.T
	w     io.WriteCloser
	lines chan string
	errs  chan error
	root  *cli.Root
	// finished closes when Execute returns; err is what it returned.
	finished chan struct{}
	err      error
}

// stdioAudit records the audit stream of a stdio run.
type stdioAudit struct {
	mu   sync.Mutex
	recs []cmdsurface.Invocation
	errs []error
}

func (a *stdioAudit) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recs = append(a.recs, inv)
	a.errs = append(a.errs, err)
	return nil
}

// stdioCommands mounts one leaf per class stdio treats differently,
// plus one that writes past its captured streams to the process's own
// standard output and one that is still running when a host that
// does not wait for answers has already closed its end.
func stdioCommands(r *cli.Root) {
	nap := &cobra.Command{Use: "nap", Short: "Answer after a pause",
		RunE: func(cmd *cobra.Command, _ []string) error {
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(200 * time.Millisecond):
			}
			cmd.Print("rested")
			return nil
		}}
	cli.SetSideEffect(nap, cli.SideEffectRead)
	ping := &cobra.Command{Use: "ping", Short: "Answer pong",
		RunE: func(cmd *cobra.Command, _ []string) error { cmd.Print("pong"); return nil }}
	cli.SetSideEffect(ping, cli.SideEffectRead)
	leak := &cobra.Command{Use: "leak", Short: "Print past the runner's capture",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Println("LEAKED-TO-PROCESS-STDOUT")
			cmd.Print("leaked")
			return nil
		}}
	cli.SetSideEffect(leak, cli.SideEffectRead)
	secret := &cobra.Command{Use: "secret", Short: "Needs the caller's credentials",
		Annotations: map[string]string{"kit/auth-required": "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("unlocked"); return nil }}
	cli.SetSideEffect(secret, cli.SideEffectRead)
	deploy := &cobra.Command{Use: "deploy", Short: "Deploy, after a person approves",
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
		RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("deployed"); return nil }}
	cli.SetSideEffect(deploy, cli.SideEffectWriteLocal)
	r.Cmd.AddCommand(ping, leak, secret, deploy, nap)
}

// startStdio runs `serve mcp --stdio` on a root whose stdio transport
// speaks on pipes, and returns the host's end.
func startStdio(t *testing.T, opts ...func(*cli.Root)) *stdioPeer {
	t.Helper()
	svcIn, hostW := io.Pipe()
	hostR, svcOut := io.Pipe()

	streams := &stdioStreams{in: svcIn, out: svcOut}
	all := append([]func(*cli.Root){with(Config{}, streams)}, opts...)
	r := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true}, all...)
	stdioCommands(r)
	r.Cmd.SetErr(io.Discard)

	p := &stdioPeer{t: t, w: hostW, lines: make(chan string, 64), errs: make(chan error, 1),
		root: r, finished: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(hostR)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
		close(p.lines)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	r.SetArgs([]string{"serve", "mcp", "--stdio"})
	go func() {
		p.err = r.Execute(ctx)
		_ = svcOut.Close()
		close(p.finished)
	}()
	t.Cleanup(func() {
		cancel()
		_ = hostW.Close()
		select {
		case <-p.finished:
		case <-time.After(10 * time.Second):
			t.Error("serve did not return")
		}
	})
	return p
}

func (p *stdioPeer) send(msg string) {
	p.t.Helper()
	_, err := io.WriteString(p.w, msg+"\n")
	require.NoError(p.t, err)
}

// next returns the next message, failing on a line that is not a
// JSON-RPC message: stdout is the wire.
func (p *stdioPeer) next() map[string]any {
	p.t.Helper()
	select {
	case line, ok := <-p.lines:
		require.True(p.t, ok, "the service closed its output")
		var m map[string]any
		require.NoError(p.t, json.Unmarshal([]byte(line), &m), "non-JSON line on the protocol stream: %q", line)
		require.Equal(p.t, "2.0", m["jsonrpc"], "not a JSON-RPC message: %q", line)
		return m
	case <-time.After(10 * time.Second):
		p.t.Fatal("no message from the service")
		return nil
	}
}

// initialize runs the legacy handshake, declaring elicitation when
// asked to.
func (p *stdioPeer) initialize(elicitation bool) {
	p.t.Helper()
	caps := `{}`
	if elicitation {
		caps = `{"elicitation":{}}`
	}
	p.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":` +
		caps + `,"clientInfo":{"name":"stdio-host","version":"1"}}}`)
	m := p.next()
	require.Nil(p.t, m["error"], "initialize: %v", m)
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

// call sends tools/call and returns the result's text and isError.
func (p *stdioPeer) call(id int, name string) (string, bool) {
	p.t.Helper()
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{}}}`, id, name))
	return resultText(p.t, p.next())
}

func resultText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	require.True(t, ok, "not a result: %v", m)
	var b strings.Builder
	content, _ := res["content"].([]any)
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok {
			if s, ok := cm["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	isErr, _ := res["isError"].(bool)
	return b.String(), isErr
}

func TestMCPStdioSessionOverPipes(t *testing.T) {
	p := startStdio(t)
	p.initialize(false)

	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := p.next()
	raw, _ := json.Marshal(list["result"])
	for _, name := range []string{`"ping"`, `"secret"`, `"deploy"`} {
		assert.Contains(t, string(raw), name)
	}

	text, isErr := p.call(3, "ping")
	assert.False(t, isErr)
	assert.Equal(t, "pong", text)
}

func TestMCPStdioKeepsStdoutForTheProtocol(t *testing.T) {
	// Capture what reaches the process's standard error, which is
	// where a stray print is sent while stdio serves.
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	origErr := os.Stderr
	os.Stderr = errW
	var captured strings.Builder
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(&captured, errR); close(copied) }()
	restored := false
	restore := func() {
		if !restored {
			restored = true
			os.Stderr = origErr
			_ = errW.Close()
			<-copied
		}
	}
	t.Cleanup(restore)
	origOut := os.Stdout

	p := startStdio(t)
	p.initialize(false)

	// Every line the service writes must parse as JSON-RPC (next
	// fails otherwise), including around a command that prints
	// straight to the process's standard output.
	text, isErr := p.call(2, "leak")
	require.False(t, isErr)
	assert.Equal(t, "leaked", text, "the captured output is the command's own")
	text, _ = p.call(3, "ping")
	assert.Equal(t, "pong", text)

	// End of input is the host ending the session: a clean stop, and
	// the process gets its streams back.
	require.NoError(t, p.w.Close())
	select {
	case <-p.finished:
		assert.NoError(t, p.err, "end of input is a clean stop")
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return at end of input")
	}
	assert.Same(t, origOut, os.Stdout, "stdout is restored after serving")
	restore()
	assert.Contains(t, captured.String(), "LEAKED-TO-PROCESS-STDOUT",
		"a stray print reaches standard error, not the wire")
}

func TestMCPStdioAdmitsAuthRequiredOnSpawnTrust(t *testing.T) {
	audit := &stdioAudit{}
	p := startStdio(t, cli.WithAuditSinks(cmdsurface.SinkSpec{Sink: audit, OnOK: true, OnError: true}))
	p.initialize(false)

	text, isErr := p.call(2, "secret")
	assert.False(t, isErr, text)
	assert.Equal(t, "unlocked", text)

	audit.mu.Lock()
	defer audit.mu.Unlock()
	require.Len(t, audit.recs, 1)
	m := audit.recs[0].Meta
	assert.Equal(t, cmdsurface.SurfaceMCP, m.Surface)
	assert.Empty(t, m.Caller, "no principal is invented")
	assert.NotEmpty(t, m.RequestID)
	assert.Equal(t, "stdio", m.Extra["mcp_transport"])
	assert.Equal(t, fmt.Sprint(os.Getppid()), m.Extra["peer_pid"])
	assert.Equal(t, "stdio-host", m.Extra["mcp_client"])
}

func TestMCPStdioConfirmationNeedsElicitation(t *testing.T) {
	t.Run("refused without elicitation", func(t *testing.T) {
		audit := &stdioAudit{}
		p := startStdio(t, cli.WithAuditSinks(cmdsurface.SinkSpec{Sink: audit, OnOK: true, OnError: true}))
		p.initialize(false)
		text, isErr := p.call(2, "deploy")
		assert.True(t, isErr)
		assert.Contains(t, text, "confirmation required")
		assert.NotContains(t, text, "deployed")
		audit.mu.Lock()
		defer audit.mu.Unlock()
		require.Len(t, audit.errs, 1)
		assert.ErrorContains(t, audit.errs[0], "confirmation required")
	})

	for _, tc := range []struct {
		action string
		want   string
		isErr  bool
	}{
		{"accept", "deployed", false},
		{"decline", "confirmation declined", true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			p := startStdio(t)
			p.initialize(true)
			p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","arguments":{}}}`)

			ask := p.next()
			require.Equal(t, "elicitation/create", ask["method"], "the service asks the host's user: %v", ask)
			params, _ := ask["params"].(map[string]any)
			assert.Equal(t, `Approve execution of "deploy"?`, params["message"])
			id, _ := json.Marshal(ask["id"])
			p.send(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":{"action":"` + tc.action + `"}}`)

			text, isErr := resultText(t, p.next())
			assert.Equal(t, tc.isErr, isErr)
			assert.Equal(t, tc.want, text)
		})
	}

	t.Run("2026-07-28 round trip", func(t *testing.T) {
		p := startStdio(t)
		meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
			`"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}}}`
		p.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy","arguments":{},` + meta + `}}`)
		first, _ := p.next()["result"].(map[string]any)
		require.Equal(t, "input_required", first["resultType"], "%v", first)
		reqs, _ := first["inputRequests"].(map[string]any)
		require.Len(t, reqs, 1)
		var key string
		for k := range reqs {
			key = k
		}
		state, _ := first["requestState"].(string)
		p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","arguments":{},` +
			`"inputResponses":{"` + key + `":{"action":"accept"}},"requestState":"` + state + `",` + meta + `}}`)
		text, isErr := resultText(t, p.next())
		assert.False(t, isErr, text)
		assert.Equal(t, "deployed", text)
	})
}

func TestMCPStdioRefusesAnHTTPAddress(t *testing.T) {
	r := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true}, With(Config{}))
	r.Cmd.SetErr(io.Discard)
	r.SetArgs([]string{"serve", "mcp", "--stdio", "--mcp-addr", "127.0.0.1:0"})
	err := r.Execute(context.Background())
	var oe *output.Error
	require.True(t, errors.As(err, &oe), "%v", err)
	assert.Equal(t, 2, oe.ExitCode)
	assert.Contains(t, oe.Error(), "--mcp-addr")
}

// denyListeners is a policy gate that refuses anything that listens.
type denyListeners struct{}

func (denyListeners) Allow(_, network string) (bool, string) {
	if network == "listen" {
		return false, "listeners are not allowed here"
	}
	return true, ""
}

func TestMCPServiceClassFollowsTheTransport(t *testing.T) {
	// A policy that forbids listeners refuses the HTTP transport...
	r := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true},
		With(Config{}), cli.WithServicePolicy(denyListeners{}))
	r.Cmd.SetErr(io.Discard)
	r.SetArgs([]string{"serve", "mcp", "--mcp-addr", "127.0.0.1:0"})
	err := r.Execute(context.Background())
	var oe *output.Error
	require.True(t, errors.As(err, &oe), "%v", err)
	assert.Equal(t, 5, oe.ExitCode)

	// ...and still admits a stdio server, which listens on nothing.
	p := startStdio(t, cli.WithServicePolicy(denyListeners{}))
	p.initialize(false)
	text, _ := p.call(2, "ping")
	assert.Equal(t, "pong", text)
}

// TestMCPStdioAnswersEverythingReadBeforeEndOfInput is a host that
// writes its requests and closes its end at once, without waiting for
// a single answer: every call is still answered, then end of input
// stops the service cleanly.
func TestMCPStdioAnswersEverythingReadBeforeEndOfInput(t *testing.T) {
	p := startStdio(t)

	p.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"stdio-host","version":"1"}}}`)
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	p.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping","arguments":{}}}`)
	p.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nap","arguments":{}}}`)
	require.NoError(t, p.w.Close())

	got := map[float64]map[string]any{}
	for len(got) < 4 {
		m := p.next()
		id, ok := m["id"].(float64)
		require.True(t, ok, "a response: %v", m)
		got[id] = m
	}
	text, isErr := resultText(t, got[3])
	assert.False(t, isErr)
	assert.Equal(t, "pong", text)
	text, isErr = resultText(t, got[4])
	assert.False(t, isErr)
	assert.Equal(t, "rested", text, "the call still running at end of input is answered")

	select {
	case <-p.finished:
		assert.NoError(t, p.err, "end of input is a clean stop")
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return at end of input")
	}
}
