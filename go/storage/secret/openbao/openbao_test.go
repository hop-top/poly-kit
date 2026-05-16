package openbao_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/openbao/openbao/api/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/openbao"
	"hop.top/xrr"
	xrrhttp "hop.top/xrr/adapters/http"
)

// Compile-time interface assertions.
var (
	_ secret.Store        = (*openbao.Store)(nil)
	_ secret.MutableStore = (*openbao.Store)(nil)
)

const testToken = "test-root-token"

// xrrTransport is an http.RoundTripper that records/replays via xrr.
type xrrTransport struct {
	session *xrr.FileSession
	adapter *xrrhttp.Adapter
	base    http.RoundTripper
}

func (t *xrrTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	xrrReq := &xrrhttp.Request{
		Method: req.Method,
		URL:    req.URL.String(),
		Body:   string(body),
	}

	resp, err := t.session.Record(req.Context(), t.adapter, xrrReq, func() (xrr.Response, error) {
		// Reconstruct the request body for the real call
		req.Body = io.NopCloser(ioReader(body))
		req.ContentLength = int64(len(body))
		r, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		var respBody []byte
		if r.Body != nil {
			respBody, _ = io.ReadAll(r.Body)
			r.Body.Close()
		}
		return &xrrhttp.Response{
			Status: r.StatusCode,
			Body:   string(respBody),
		}, nil
	})
	if err != nil {
		return nil, err
	}

	// Convert xrr response back to http.Response
	switch v := resp.(type) {
	case *xrrhttp.Response:
		return &http.Response{
			StatusCode: v.Status,
			Body:       io.NopCloser(ioReader([]byte(v.Body))),
			Header:     make(http.Header),
		}, nil
	case *xrr.RawResponse:
		status := 200
		if s, ok := v.Payload["status"]; ok {
			if si, ok := s.(int); ok {
				status = si
			} else if sf, ok := s.(float64); ok {
				status = int(sf)
			}
		}
		bodyStr := ""
		if b, ok := v.Payload["body"]; ok {
			bodyStr, _ = b.(string)
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(ioReader([]byte(bodyStr))),
			Header:     make(http.Header),
		}, nil
	default:
		return nil, fmt.Errorf("openbao_test: unexpected response type %T", resp)
	}
}

func ioReader(b []byte) io.Reader {
	return &bytesReader{data: b}
}

type bytesReader struct {
	data []byte
	off  int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	if r.off >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

func startOpenBao(t *testing.T) (addr string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "quay.io/openbao/openbao:latest",
		ExposedPorts: []string{"8200/tcp"},
		Env: map[string]string{
			"BAO_DEV_ROOT_TOKEN_ID":  testToken,
			"BAO_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
		},
		Cmd:        []string{"server", "-dev"},
		WaitingFor: wait.ForAll(wait.ForHTTP("/v1/sys/health").WithPort("8200/tcp")).WithDeadline(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "8200")
	return fmt.Sprintf("http://%s:%s", host, port.Port())
}

func newXRRSession(t *testing.T) *xrr.FileSession {
	t.Helper()
	mode := os.Getenv("XRR_MODE")
	if mode == "" {
		mode = "passthrough"
	}
	dir := os.Getenv("XRR_CASSETTE_DIR")
	if dir == "" {
		dir = "testdata/cassettes"
	}
	if mode == "record" {
		_ = os.MkdirAll(dir, 0o755)
	}
	m := xrr.Mode(mode)
	if m == xrr.ModePassthrough {
		return xrr.NewSession(m, nil)
	}
	return xrr.NewSession(m, xrr.NewFileCassette(dir))
}

func newStore(t *testing.T) *openbao.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping openbao integration test in short mode")
	}
	if os.Getenv("CI") != "" {
		t.Skip("skipping openbao test in CI until xrr nil-body bug is fixed (hop-top/xrr#T-0048)")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	mode := os.Getenv("XRR_MODE")

	var addr string
	if mode == "replay" {
		addr = "http://localhost:8200" // placeholder; won't hit network
	} else {
		addr = startOpenBao(t)
	}

	session := newXRRSession(t)
	adapter := xrrhttp.NewAdapter()

	cfg := api.DefaultConfig()
	cfg.Address = addr
	cfg.HttpClient = &http.Client{
		Transport: &xrrTransport{
			session: session,
			adapter: adapter,
			base:    http.DefaultTransport,
		},
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken(testToken)
	return openbao.NewWithClient(client, "secret", "test/")
}

func TestRoundtrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "db/pass", []byte("hunter2")); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "db/pass")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "hunter2" {
		t.Fatalf("got %q, want %q", got.Value, "hunter2")
	}
	if got.Key != "db/pass" {
		t.Fatalf("got key %q, want %q", got.Key, "db/pass")
	}
}

func TestExists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	ok, err := s.Exists(ctx, "nonexistent-key")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false for missing key")
	}

	_ = s.Set(ctx, "exists-key", []byte("v"))
	ok, err = s.Exists(ctx, "exists-key")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true for present key")
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "del-key", []byte("v"))
	if err := s.Delete(ctx, "del-key"); err != nil {
		t.Fatal(err)
	}

	ok, err := s.Exists(ctx, "del-key")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestList(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "list/a", []byte("1"))
	_ = s.Set(ctx, "list/b", []byte("2"))

	keys, err := s.List(ctx, "test/list/")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) < 2 {
		t.Fatalf("got %d keys, want >= 2: %v", len(keys), keys)
	}
}
