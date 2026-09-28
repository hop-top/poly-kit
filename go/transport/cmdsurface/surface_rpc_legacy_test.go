package cmdsurface_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/cmdsurface"
)

// Clients written before the published proto send JSON and decode it
// into the Go types. These tests hold the generated handler to that
// wire: same keys, zero exit code present, done result under data.

func legacyUnaryClient(url string) *connect.Client[cmdsurface.Invocation, cmdsurface.Result] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Result](
		http.DefaultClient, url+cmdsurface.RPCInvokeProcedure,
		cmdsurface.RPCClientOptions()..., //nolint:staticcheck // exercising the deprecated JSON client path
	)
}

func legacyStreamClient(url string) *connect.Client[cmdsurface.Invocation, cmdsurface.Event] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Event](
		http.DefaultClient, url+cmdsurface.RPCInvokeStreamProcedure,
		cmdsurface.RPCClientOptions()..., //nolint:staticcheck // exercising the deprecated JSON client path
	)
}

func TestRPCLegacyJSONClient_Invoke(t *testing.T) {
	f := newFixture(t)
	f.runner.RunFn = func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
		return cmdsurface.Result{ExitCode: 2, Stdout: "out", Data: map[string]any{"n": json.Number("7")}}, nil
	}
	f.start()

	req := connect.NewRequest(&cmdsurface.Invocation{
		Path: []string{"echo"},
		Meta: cmdsurface.Meta{Caller: "u1", RequestID: "r1"},
	})
	resp, err := legacyUnaryClient(f.ts.URL).CallUnary(context.Background(), req)
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if resp.Msg.ExitCode != 2 || resp.Msg.Stdout != "out" {
		t.Errorf("Result=%+v want exit 2 stdout out", resp.Msg)
	}
	if m, _ := resp.Msg.Data.(map[string]any); m["n"] != float64(7) {
		t.Errorf("Data=%#v want n=7", resp.Msg.Data)
	}
	if got := f.runner.LastInvocation.Meta; got.Caller != "u1" || got.RequestID != "r1" {
		t.Errorf("runner Meta=%+v want caller u1 request r1", got)
	}

	_, err = legacyUnaryClient(f.ts.URL).CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"bogus"}}))
	if got := connect.CodeOf(err); err == nil || got != connect.CodeNotFound {
		t.Errorf("code=%v want=%v (err=%v)", got, connect.CodeNotFound, err)
	}
}

func TestRPCLegacyJSONClient_Stream(t *testing.T) {
	f := newFixture(t)
	f.runner.StreamFn = func(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
		out <- cmdsurface.Event{Kind: "stdout", Data: "line", At: time.Now()}
		out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "line\n"}, At: time.Now()}
		return nil
	}
	f.start()

	stream, err := legacyStreamClient(f.ts.URL).CallServerStream(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"lines"}}))
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	var got []cmdsurface.Event
	for stream.Receive() {
		got = append(got, *stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(got) != 2 || got[0].Data != "line" || got[0].At.IsZero() {
		t.Fatalf("events=%+v want stdout line then done", got)
	}
	done, _ := got[1].Data.(map[string]any)
	if got[1].Kind != "done" || done["exit_code"] != float64(0) || done["stdout"] != "line\n" {
		t.Errorf("done=%+v want data {exit_code:0 stdout:line}", got[1])
	}
}

// TestRPC_RawJSONWire posts hand-written JSON, as curl or a script
// would, and reads the response keys verbatim.
func TestRPC_RawJSONWire(t *testing.T) {
	f := newFixture(t)
	f.start()

	body := `{"path":["echo"],"args":["a"],"flags":{"n":1},"meta":{"caller":"u1","request_id":"r1","trace_id":"t1","requested_at":"0001-01-01T00:00:00Z","unknown":"ignored"}}`
	resp, err := http.Post(f.ts.URL+cmdsurface.RPCInvokeProcedure, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	// protojson varies whitespace on purpose; compare decoded keys.
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}
	if want := map[string]any{"exit_code": float64(0), "stdout": "ok"}; !reflect.DeepEqual(got, want) {
		t.Errorf("body=%s want keys %v", raw, want)
	}
	inv := f.runner.LastInvocation
	if inv.Meta.RequestID != "r1" || inv.Meta.TraceID != "t1" || inv.Flags["n"] != float64(1) || inv.Args[0] != "a" {
		t.Errorf("runner saw %+v", inv)
	}
}
