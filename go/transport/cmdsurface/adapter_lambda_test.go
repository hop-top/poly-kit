package cmdsurface_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// lambdaFakeRunner records every Invocation and dispatches RunFn if
// set. Stream is not used by the Lambda adapter.
type lambdaFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (f *lambdaFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, inv)
	f.mu.Unlock()
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *lambdaFakeRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("lambdaFakeRunner: Stream not supported")
}

func (f *lambdaFakeRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// lambdaTestTree mirrors the webhook test tree shape with a few extra
// leaves so we can exercise every construction-time gate.
func lambdaTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		Short:       "Add a widget",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	del := &cobra.Command{
		Use:         "delete",
		Short:       "Delete a widget",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	confirm := &cobra.Command{
		Use:   "confirm",
		Short: "Two-step operation",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}
	widget.AddCommand(add, del, confirm)
	root.AddCommand(widget)

	notify := &cobra.Command{Use: "notify"}
	message := &cobra.Command{
		Use:         "message",
		Short:       "Send notification",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	notify.AddCommand(message)
	root.AddCommand(notify)

	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// lambdaBridge constructs a bridge with the standard test tree,
// applies Expose for each pattern, and overrides the policy if non-nil.
func lambdaBridge(t *testing.T, runner cmdsurface.Runner, exposes map[string][]cmdsurface.Surface, policy *cmdsurface.Policy) *cmdsurface.Bridge {
	t.Helper()
	opts := []cmdsurface.Option{cmdsurface.WithRunner(runner)}
	if policy != nil {
		opts = append(opts, cmdsurface.WithPolicy(*policy))
	}
	b := cmdsurface.New(lambdaTestTree(), opts...)
	for pat, sfs := range exposes {
		b.Expose(pat, sfs...)
	}
	return b
}

// lambdaInvokeHandler builds the handler, then invokes it once with
// the given event JSON. Test helpers panic-route through t.Fatal for
// brevity.
func lambdaInvokeHandler(
	t *testing.T,
	b *cmdsurface.Bridge,
	cfg cmdsurface.LambdaConfig,
	event []byte,
) (json.RawMessage, error) {
	t.Helper()
	h, err := cmdsurface.LambdaHandler(b, cfg)
	if err != nil {
		t.Fatalf("LambdaHandler: %v", err)
	}
	return h(context.Background(), event)
}

func TestLambda_APIGatewayV2_HappyPath(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{"title":"foo"}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"title": "{{ .body.title }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayV2HTTPResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.StatusCode != 200 {
		t.Errorf("status=%d want 200; body=%s", got.StatusCode, got.Body)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if v := captured[0].Flags["title"]; v != "foo" {
		t.Errorf("flags[title]=%v want foo", v)
	}
	if captured[0].Meta.Surface != cmdsurface.SurfaceFaaS {
		t.Errorf("Meta.Surface=%q want faas", captured[0].Meta.Surface)
	}
	if captured[0].Meta.Caller != "lambda" {
		t.Errorf("Meta.Caller=%q want lambda", captured[0].Meta.Caller)
	}
}

func TestLambda_APIGatewayV2_NonZeroExit(t *testing.T) {
	runner := &lambdaFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{ExitCode: 7, Stderr: "boom"}, nil
		},
	}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"notify", "message"}},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayV2HTTPResponse
	_ = json.Unmarshal(resp, &got)
	if got.StatusCode != 500 {
		t.Errorf("status=%d want 500", got.StatusCode)
	}
}

func TestLambda_APIGatewayV1_HappyPath(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	req := events.APIGatewayProxyRequest{
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"title":"bar"}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV1,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"title": "{{ .body.title }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayProxyResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.StatusCode != 200 {
		t.Errorf("status=%d want 200; body=%s", got.StatusCode, got.Body)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if v := captured[0].Flags["title"]; v != "bar" {
		t.Errorf("flags[title]=%v want bar", v)
	}
}

func TestLambda_EventBridge_HappyPath(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	ev := events.CloudWatchEvent{
		ID:         "ebr-1",
		Source:     "test",
		DetailType: "thing",
		Detail:     json.RawMessage(`{"id":42}`),
	}
	payload, _ := json.Marshal(ev)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventEventBridge,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"id": "{{ .detail.id }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var res cmdsurface.Result
	if err := json.Unmarshal(resp, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	// JSON numbers decode as float64; template renders %v which is "42".
	if v := captured[0].Flags["id"]; v != "42" {
		t.Errorf("flags[id]=%v want 42", v)
	}
	if captured[0].Meta.TraceID != "ebr-1" {
		t.Errorf("Meta.TraceID=%q want ebr-1", captured[0].Meta.TraceID)
	}
}

func TestLambda_SQS_SingleMessage(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	ev := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "m1", Body: `{"title":"one"}`},
		},
	}
	payload, _ := json.Marshal(ev)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventSQS,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"title": "{{ .body.title }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.SQSEventResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.BatchItemFailures) != 0 {
		t.Errorf("BatchItemFailures=%v want empty", got.BatchItemFailures)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if v := captured[0].Flags["title"]; v != "one" {
		t.Errorf("flags[title]=%v want one", v)
	}
}

func TestLambda_SQS_MultipleMessages(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	ev := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "m1", Body: `{"title":"one"}`},
			{MessageId: "m2", Body: `{"title":"two"}`},
			{MessageId: "m3", Body: `{"title":"three"}`},
		},
	}
	payload, _ := json.Marshal(ev)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventSQS,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"title": "{{ .body.title }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.SQSEventResponse
	_ = json.Unmarshal(resp, &got)
	if len(got.BatchItemFailures) != 0 {
		t.Errorf("BatchItemFailures=%v want empty", got.BatchItemFailures)
	}
	if n := len(runner.captured()); n != 3 {
		t.Errorf("captured=%d want 3", n)
	}
}

func TestLambda_SQS_PartialFailure(t *testing.T) {
	var seq int
	runner := &lambdaFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			seq++
			if seq == 2 {
				return cmdsurface.Result{}, errors.New("boom")
			}
			return cmdsurface.Result{}, nil
		},
	}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	ev := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "m1", Body: `{"title":"a"}`},
			{MessageId: "m2", Body: `{"title":"b"}`},
			{MessageId: "m3", Body: `{"title":"c"}`},
		},
	}
	payload, _ := json.Marshal(ev)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventSQS,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"title": "{{ .body.title }}"},
		},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.SQSEventResponse
	_ = json.Unmarshal(resp, &got)
	if len(got.BatchItemFailures) != 1 {
		t.Fatalf("BatchItemFailures=%v want 1 entry", got.BatchItemFailures)
	}
	if got.BatchItemFailures[0].ItemIdentifier != "m2" {
		t.Errorf("failed id=%q want m2", got.BatchItemFailures[0].ItemIdentifier)
	}
}

func TestLambda_Direct_HappyPath(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"ping": {cmdsurface.SurfaceFaaS},
	}, nil)

	inv := cmdsurface.Invocation{Path: []string{"ping"}}
	payload, _ := json.Marshal(inv)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventDirect,
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var res cmdsurface.Result
	if err := json.Unmarshal(resp, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if len(captured[0].Path) != 1 || captured[0].Path[0] != "ping" {
		t.Errorf("Path=%v want [ping]", captured[0].Path)
	}
}

func TestLambda_Direct_SurfaceForced(t *testing.T) {
	var assertSurface cmdsurface.Surface
	runner := &lambdaFakeRunner{
		RunFn: func(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
			assertSurface = inv.Meta.Surface
			return cmdsurface.Result{}, nil
		},
	}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"ping": {cmdsurface.SurfaceFaaS},
	}, nil)

	// Caller LIES about the surface — adapter must overwrite.
	inv := cmdsurface.Invocation{
		Path: []string{"ping"},
		Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceCLI},
	}
	payload, _ := json.Marshal(inv)
	if _, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventDirect,
	}, payload); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if assertSurface != cmdsurface.SurfaceFaaS {
		t.Errorf("Meta.Surface=%q want faas", assertSurface)
	}
}

func TestLambda_Construction_UnknownLeaf(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, nil, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"does", "not", "exist"}},
	})
	if err == nil || !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Fatalf("err=%v want ErrUnknownCommand", err)
	}
}

func TestLambda_Construction_SurfaceNotEnabled(t *testing.T) {
	// Default policy enables CLI/Lib/MCP only; FaaS is NOT default.
	b := lambdaBridge(t, &lambdaFakeRunner{}, nil, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"notify", "message"}},
	})
	if err == nil || !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Fatalf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

func TestLambda_Construction_DestructiveWithoutOptIn(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget delete": {cmdsurface.SurfaceFaaS},
	}, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"widget", "delete"}},
	})
	if err == nil || !errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Fatalf("err=%v want ErrDestructiveBlocked", err)
	}
}

func TestLambda_Construction_DestructiveWithOptIn(t *testing.T) {
	policy := cmdsurface.Policy{
		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceFaaS},
		DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
	}
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget delete": {cmdsurface.SurfaceFaaS},
	}, &policy)

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"widget", "delete"}},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayV2HTTPResponse
	_ = json.Unmarshal(resp, &got)
	if got.StatusCode != 200 {
		t.Errorf("status=%d want 200; body=%s", got.StatusCode, got.Body)
	}
}

func TestLambda_Construction_ConfirmationRequired(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget confirm": {cmdsurface.SurfaceFaaS},
	}, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"widget", "confirm"}},
	})
	if err == nil {
		t.Fatal("LambdaHandler succeeded; want confirmation-required error")
	}
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("err=%q want contains 'confirmation'", err)
	}
}

func TestLambda_Construction_InvalidTemplate(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"x": "{{ .bogus.foo }}"},
		},
	})
	if err == nil {
		t.Fatal("LambdaHandler succeeded; want disallowed-root error")
	}
	if !strings.Contains(err.Error(), "disallowed template root") {
		t.Errorf("err=%q want contains 'disallowed template root'", err)
	}
}

func TestLambda_ResultLogCalled(t *testing.T) {
	runner := &lambdaFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "logged"}, nil
		},
	}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	var (
		mu      sync.Mutex
		calls   int
		lastRes cmdsurface.Result
		lastCfg cmdsurface.LambdaConfig
		lastErr error
	)
	cfg := cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"notify", "message"}},
		ResultLog: func(c cmdsurface.LambdaConfig, r cmdsurface.Result, err error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			lastCfg = c
			lastRes = r
			lastErr = err
		},
	}
	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{}`,
	}
	payload, _ := json.Marshal(req)
	if _, err := lambdaInvokeHandler(t, b, cfg, payload); err != nil {
		t.Fatalf("handler error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("calls=%d want 1", calls)
	}
	if lastErr != nil {
		t.Errorf("lastErr=%v want nil", lastErr)
	}
	if lastRes.Stdout != "logged" {
		t.Errorf("lastRes.Stdout=%q want logged", lastRes.Stdout)
	}
	if lastCfg.Event != cmdsurface.EventAPIGatewayV2 {
		t.Errorf("lastCfg.Event=%q want %q", lastCfg.Event, cmdsurface.EventAPIGatewayV2)
	}
}

func TestLambda_APIGatewayV2_UnknownLeafAtRuntime(t *testing.T) {
	// Construct a bridge that exposes one leaf, then build a handler
	// against THAT leaf; afterwards, swap in a runner that errors with
	// ErrUnknownCommand to exercise the runtime sentinel mapping. The
	// adapter routes via the bridge so the bridge's resolveLeaf
	// returns success; we simulate the sentinel via the runner.
	runner := &lambdaFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{}, cmdsurface.ErrUnknownCommand
		},
	}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"notify", "message"}},
	}, payload)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayV2HTTPResponse
	_ = json.Unmarshal(resp, &got)
	if got.StatusCode != 500 {
		t.Errorf("status=%d want 500", got.StatusCode)
	}
	if !strings.Contains(got.Body, "unknown_command") {
		t.Errorf("body=%q want contains unknown_command", got.Body)
	}
}

func TestLambda_APIGatewayV2_BadJSON(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	resp, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event:   cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{Path: []string{"notify", "message"}},
	}, []byte(`not json at all`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var got events.APIGatewayV2HTTPResponse
	_ = json.Unmarshal(resp, &got)
	if got.StatusCode != 400 {
		t.Errorf("status=%d want 400", got.StatusCode)
	}
	if !strings.Contains(got.Body, "bad_request") {
		t.Errorf("body=%q want contains bad_request", got.Body)
	}
}

func TestLambda_SQS_MessageAttributesAsHeaders(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	val := "alpha"
	ev := events.SQSEvent{
		Records: []events.SQSMessage{{
			MessageId: "m1",
			Body:      `{}`,
			MessageAttributes: map[string]events.SQSMessageAttribute{
				"X-Source": {DataType: "String", StringValue: &val},
			},
		}},
	}
	payload, _ := json.Marshal(ev)
	if _, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventSQS,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"notify", "message"},
			FlagMap: map[string]string{"src": `{{ index .headers "X-Source" }}`},
		},
	}, payload); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if v := captured[0].Flags["src"]; v != "alpha" {
		t.Errorf("flags[src]=%v want alpha", v)
	}
}

func TestLambda_ArgsTemplate(t *testing.T) {
	runner := &lambdaFakeRunner{}
	b := lambdaBridge(t, runner, map[string][]cmdsurface.Surface{
		"notify message": {cmdsurface.SurfaceFaaS},
	}, nil)

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{"a":"x","b":"y"}`,
	}
	payload, _ := json.Marshal(req)
	if _, err := lambdaInvokeHandler(t, b, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path:         []string{"notify", "message"},
			ArgsTemplate: "{{ .body.a }} {{ .body.b }}",
		},
	}, payload); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("captured=%d want 1", len(captured))
	}
	if got := captured[0].Args; len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("Args=%v want [x y]", got)
	}
}

func TestLambda_NilBridge(t *testing.T) {
	_, err := cmdsurface.LambdaHandler(nil, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventDirect,
	})
	if err == nil {
		t.Fatal("LambdaHandler(nil, ...) succeeded; want error")
	}
}

func TestLambda_MissingEvent(t *testing.T) {
	b := lambdaBridge(t, &lambdaFakeRunner{}, nil, nil)
	_, err := cmdsurface.LambdaHandler(b, cmdsurface.LambdaConfig{})
	if err == nil {
		t.Fatal("LambdaHandler with empty Event succeeded; want error")
	}
}
