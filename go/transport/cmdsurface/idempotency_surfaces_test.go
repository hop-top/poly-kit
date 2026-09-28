package cmdsurface_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// idemSurfaceRunner answers every run with the run's ordinal, so a
// replay (the first ordinal again) is told from a second run. With
// hold set, a run waits for it to close, signaling started first.
type idemSurfaceRunner struct {
	calls   atomic.Int32
	hold    chan struct{}
	started chan struct{}
}

func (r *idemSurfaceRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	n := r.calls.Add(1)
	if r.hold != nil {
		r.started <- struct{}{}
		select {
		case <-r.hold:
		case <-ctx.Done():
			return cmdsurface.Result{}, ctx.Err()
		}
	}
	return cmdsurface.Result{Stdout: fmt.Sprintf("run %d %v\n", n, inv.Flags["name"])}, nil
}

func (r *idemSurfaceRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	defer close(out)
	res, err := r.Run(ctx, inv)
	out <- cmdsurface.Event{Kind: "stdout", Data: strings.TrimSuffix(res.Stdout, "\n")}
	out <- cmdsurface.Event{Kind: "done", Data: &res}
	return err
}

func idemSurfaceBridge(run cmdsurface.Runner) *cmdsurface.Bridge {
	root := &cobra.Command{Use: "tool"}
	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		Annotations: map[string]string{"kit/side-effect": "write"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	add.Flags().String("name", "", "widget name")
	widget.AddCommand(add)
	root.AddCommand(widget)
	b := cmdsurface.New(root, cmdsurface.WithRunner(run),
		cmdsurface.WithIdempotency(cmdsurface.NewIdempotencyLedger(idemstore.Memory()), time.Hour))
	b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceRPC)
	return b
}

// restCall posts one projected call and returns status, the replay
// header and the decoded body.
func restCall(t *testing.T, url, key, name string) (int, string, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/v1/commands/widget/add",
		strings.NewReader(`{"flags":{"name":"`+name+`"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(api.HeaderIdempotencyKey, key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, resp.Header.Get(api.HeaderIdempotentReplayed), body
}

func TestIdempotency_REST(t *testing.T) {
	run := &idemSurfaceRunner{}
	b := idemSurfaceBridge(run)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatal(err)
	}
	url := serve(t, r)

	status, marker, first := restCall(t, url, "k1", "a")
	if status != http.StatusOK || marker != "" {
		t.Fatalf("first call: status %d, marker %q", status, marker)
	}
	status, marker, second := restCall(t, url, "k1", "a")
	if status != http.StatusOK || marker != "true" {
		t.Fatalf("replay: status %d, %s = %q, want 200 and true", status, api.HeaderIdempotentReplayed, marker)
	}
	if second["stdout"] != first["stdout"] || run.calls.Load() != 1 {
		t.Fatalf("replay body %v, first %v, runs %d", second, first, run.calls.Load())
	}

	status, _, body := restCall(t, url, "k1", "b")
	if status != http.StatusUnprocessableEntity || body["code"] != api.CodeIdempotencyKeyReused {
		t.Fatalf("reused key: status %d body %v, want 422 %s", status, body, api.CodeIdempotencyKeyReused)
	}

	if status, marker, _ := restCall(t, url, "", "a"); status != http.StatusOK || marker != "" {
		t.Fatalf("a call without a key runs: status %d marker %q", status, marker)
	}
	if run.calls.Load() != 2 {
		t.Fatalf("runs = %d, want 2", run.calls.Load())
	}
}

func TestIdempotency_REST_Conflict(t *testing.T) {
	run := &idemSurfaceRunner{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	b := idemSurfaceBridge(run)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatal(err)
	}
	url := serve(t, r)

	done := make(chan int, 1)
	go func() {
		status, _, _ := restCall(t, url, "k1", "a")
		done <- status
	}()
	<-run.started
	status, _, body := restCall(t, url, "k1", "a")
	if status != http.StatusConflict || body["code"] != api.CodeIdempotencyConflict {
		t.Fatalf("key in flight: status %d body %v, want 409 %s", status, body, api.CodeIdempotencyConflict)
	}
	close(run.hold)
	if s := <-done; s != http.StatusOK {
		t.Fatalf("first call: status %d", s)
	}
}

func TestIdempotency_REST_Stream(t *testing.T) {
	run := &idemSurfaceRunner{}
	b := idemSurfaceBridge(run)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r); err != nil {
		t.Fatal(err)
	}
	url := serve(t, r)

	stream := func() (string, string) {
		req, _ := http.NewRequest(http.MethodPost, url+"/v1/commands/widget/add/stream",
			strings.NewReader(`{"flags":{"name":"a"}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(api.HeaderIdempotencyKey, "s1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.Header.Get(api.HeaderIdempotentReplayed), string(raw)
	}
	marker, first := stream()
	if marker != "" || !strings.Contains(first, "run 1 a") {
		t.Fatalf("first stream: marker %q body %s", marker, first)
	}
	marker, second := stream()
	if marker != "true" || !strings.Contains(second, "run 1 a") || !strings.Contains(second, "event: result") {
		t.Fatalf("replayed stream: marker %q body %s", marker, second)
	}
	if run.calls.Load() != 1 {
		t.Fatalf("runs = %d, want 1", run.calls.Load())
	}
	// A streamed record answers the unary route too.
	if status, marker, _ := restCall(t, url, "s1", "a"); status != http.StatusOK || marker != "true" {
		t.Fatalf("unary after stream: status %d marker %q", status, marker)
	}
}

func rpcInvocation(key, name, headerKey string) *connect.Request[cmdsurfacev1.Invocation] {
	flags, _ := structpb.NewStruct(map[string]any{"name": name})
	req := connect.NewRequest(&cmdsurfacev1.Invocation{
		Path:  []string{"widget", "add"},
		Flags: flags,
		Meta:  &cmdsurfacev1.Meta{IdempotencyKey: key},
	})
	if headerKey != "" {
		req.Header().Set(api.HeaderIdempotencyKey, headerKey)
	}
	return req
}

func TestIdempotency_RPC(t *testing.T) {
	forEachProtocol(t, func(t *testing.T, p wireProtocol) {
		run := &idemSurfaceRunner{}
		b := idemSurfaceBridge(run)
		srv := newMountServer()
		if err := cmdsurface.MountRPC(b, srv); err != nil {
			t.Fatal(err)
		}
		ts := newH2CServer(t, srv)
		client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), ts.URL, p.opts...)
		ctx := context.Background()

		first, err := client.Invoke(ctx, rpcInvocation("k1", "a", ""))
		if err != nil {
			t.Fatal(err)
		}
		if first.Header().Get(api.HeaderIdempotentReplayed) != "" {
			t.Fatal("the first call is not a replay")
		}
		// The Idempotency-Key header carries the key when the message
		// has none.
		second, err := client.Invoke(ctx, rpcInvocation("", "a", "k1"))
		if err != nil {
			t.Fatal(err)
		}
		if second.Header().Get(api.HeaderIdempotentReplayed) != "true" || second.Msg.GetStdout() != first.Msg.GetStdout() {
			t.Fatalf("replay: header %q stdout %q, want true and %q",
				second.Header().Get(api.HeaderIdempotentReplayed), second.Msg.GetStdout(), first.Msg.GetStdout())
		}
		_, err = client.Invoke(ctx, rpcInvocation("k1", "b", ""))
		if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), cmdsurface.CodeIdempotencyKeyReused) {
			t.Fatalf("reused key: %v, want InvalidArgument %s", err, cmdsurface.CodeIdempotencyKeyReused)
		}
		if run.calls.Load() != 1 {
			t.Fatalf("runs = %d, want 1", run.calls.Load())
		}

		stream, err := client.InvokeStream(ctx, rpcInvocation("k1", "a", ""))
		if err != nil {
			t.Fatal(err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatal(err)
		}
		if stream.ResponseHeader().Get(api.HeaderIdempotentReplayed) != "true" || run.calls.Load() != 1 {
			t.Fatalf("stream replay: header %q runs %d",
				stream.ResponseHeader().Get(api.HeaderIdempotentReplayed), run.calls.Load())
		}
	})
}

func TestIdempotency_RPC_Conflict(t *testing.T) {
	run := &idemSurfaceRunner{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	b := idemSurfaceBridge(run)
	srv := newMountServer()
	if err := cmdsurface.MountRPC(b, srv); err != nil {
		t.Fatal(err)
	}
	ts := newH2CServer(t, srv)
	client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), ts.URL, wireProtocols[0].opts...)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := client.Invoke(ctx, rpcInvocation("k1", "a", ""))
		done <- err
	}()
	<-run.started
	_, err := client.Invoke(ctx, rpcInvocation("k1", "a", ""))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("key in flight: %v, want Aborted", err)
	}
	close(run.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
