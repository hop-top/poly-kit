//go:build e2e

// End-to-end tests for the cmdsurface-faas example. Gated behind the
// `e2e` build tag because they shell out to `go build` (for the
// Cloud Run smoke) and exercise the real shared.BuildBridge wiring
// end-to-end.
//
// The Cloud Run adapter (RunCloudRun) installs signal handlers in its
// outer wrapper; the inner runCloudRunCtx that takes a context is
// unexported. Rather than spawn the binary and probe its port (which
// is what real adopters do, but cumbersome inside `go test`), these
// tests assert the binary compiles cleanly and then exercise the
// Lambda handler — which DOES take a context — directly. The Lambda
// path covers the integration with shared.BuildBridge() and the
// adapter's eager validation, which is the part of the example that
// is unique to FaaS deployments.
//
// Run with:
//
//	go test -tags=e2e -race -count=1 ./examples/cmdsurface-faas/...
package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"hop.top/kit/examples/cmdsurface-faas/shared"
	"hop.top/kit/go/transport/cmdsurface"
)

// TestCloudRunBinaryBuilds asserts the Cloud Run target compiles. The
// build step is otherwise covered by `go build ./...` at the repo
// level, but having it here keeps the e2e suite self-contained:
// running `go test -tags=e2e ./examples/cmdsurface-faas/...` is
// enough to know the deployable shape is intact.
func TestCloudRunBinaryBuilds(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	out := tmp + "/cloudrun"
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out,
		"hop.top/kit/examples/cmdsurface-faas/cmd/cloudrun")
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, combined)
	}
}

// TestLambdaBinaryBuilds asserts the Lambda target compiles. Same
// rationale as TestCloudRunBinaryBuilds.
func TestLambdaBinaryBuilds(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	out := tmp + "/lambda"
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out,
		"hop.top/kit/examples/cmdsurface-faas/cmd/lambda")
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, combined)
	}
}

// TestLambda_APIGWv2_PingHappyPath exercises the shared bridge under
// the API Gateway V2 event shape. Body is empty (ping takes no
// flags); we just assert the leaf executed and "pong" came back.
func TestLambda_APIGWv2_PingHappyPath(t *testing.T) {
	t.Parallel()
	bridge := shared.BuildBridge()

	h, err := cmdsurface.LambdaHandler(bridge, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path: []string{"ping"},
		},
	})
	if err != nil {
		t.Fatalf("LambdaHandler: %v", err)
	}

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := h(context.Background(), payload)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var got events.APIGatewayV2HTTPResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.StatusCode != 200 {
		t.Fatalf("status=%d want 200; body=%s", got.StatusCode, got.Body)
	}

	var res cmdsurface.Result
	if err := json.Unmarshal([]byte(got.Body), &res); err != nil {
		t.Fatalf("decode body: %v body=%s", err, got.Body)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want 0; stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pong") {
		t.Errorf("stdout=%q want substring pong", res.Stdout)
	}
}

// TestLambda_APIGWv2_EchoWithBodyTemplate covers the FlagMap →
// template render → Invocation pipeline. The bridge's `echo` leaf
// reads its message from positional args, but the example wires a
// `message` flag template so the body payload reaches the runner via
// Invocation.Flags. We assert echo's "hi" appears in stdout when the
// flag is set.
//
// Note: shared.BuildBridge wires echo as `echo <message>`, taking the
// message as a positional arg. To exercise the template path we use
// the example's `--who` flag against `stamp` instead, then assert the
// stamp output names the right user.
func TestLambda_APIGWv2_StampWithFlagTemplate(t *testing.T) {
	t.Parallel()
	bridge := shared.BuildBridge()

	h, err := cmdsurface.LambdaHandler(bridge, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path:    []string{"stamp"},
			FlagMap: map[string]string{"who": "{{ .body.who }}"},
		},
	})
	if err != nil {
		t.Fatalf("LambdaHandler: %v", err)
	}

	req := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		Body:    `{"who":"alice"}`,
	}
	payload, _ := json.Marshal(req)
	resp, err := h(context.Background(), payload)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var got events.APIGatewayV2HTTPResponse
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.StatusCode != 200 {
		t.Fatalf("status=%d want 200; body=%s", got.StatusCode, got.Body)
	}

	var res cmdsurface.Result
	if err := json.Unmarshal([]byte(got.Body), &res); err != nil {
		t.Fatalf("decode body: %v body=%s", err, got.Body)
	}
	if !strings.Contains(res.Stdout, "stamped by alice") {
		t.Errorf("stdout=%q want substring 'stamped by alice'", res.Stdout)
	}
}

// TestLambda_EventDirect_PingHappyPath exercises the EventDirect
// event type, where the inbound event JSON IS the Invocation. No
// template, no mapping. The shared bridge has SurfaceFaaS enabled by
// policy, so the call should succeed.
func TestLambda_EventDirect_PingHappyPath(t *testing.T) {
	t.Parallel()
	bridge := shared.BuildBridge()

	h, err := cmdsurface.LambdaHandler(bridge, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventDirect,
	})
	if err != nil {
		t.Fatalf("LambdaHandler: %v", err)
	}

	inv := cmdsurface.Invocation{Path: []string{"ping"}}
	payload, _ := json.Marshal(inv)
	resp, err := h(context.Background(), payload)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var res cmdsurface.Result
	if err := json.Unmarshal(resp, &res); err != nil {
		t.Fatalf("decode result: %v raw=%s", err, resp)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d want 0; stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pong") {
		t.Errorf("stdout=%q want substring pong", res.Stdout)
	}
}

// TestLambda_UnknownLeafReturnsError asserts the adapter validates
// the mapping eagerly: an unknown leaf path makes LambdaHandler
// return an error at construction, not at first invoke. This is the
// "fail the cold start, not the request" guarantee adopters rely on.
func TestLambda_UnknownLeafReturnsError(t *testing.T) {
	t.Parallel()
	bridge := shared.BuildBridge()

	_, err := cmdsurface.LambdaHandler(bridge, cmdsurface.LambdaConfig{
		Event: cmdsurface.EventAPIGatewayV2,
		Mapping: cmdsurface.LambdaMapping{
			Path: []string{"nonexistent"},
		},
	})
	if err == nil {
		t.Fatal("LambdaHandler returned nil error for unknown leaf")
	}
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Errorf("err=%v want errors.Is ErrUnknownCommand", err)
	}
}

// TestLambda_MissingEventReturnsError asserts an empty Event field is
// rejected at construction. The example main reads CMDSURF_EVENT
// directly and defaults to EventAPIGatewayV2, so this exercises the
// adapter's contract rather than the example's plumbing.
func TestLambda_MissingEventReturnsError(t *testing.T) {
	t.Parallel()
	bridge := shared.BuildBridge()

	_, err := cmdsurface.LambdaHandler(bridge, cmdsurface.LambdaConfig{
		Mapping: cmdsurface.LambdaMapping{
			Path: []string{"ping"},
		},
	})
	if err == nil {
		t.Fatal("LambdaHandler returned nil error for empty Event")
	}
}
