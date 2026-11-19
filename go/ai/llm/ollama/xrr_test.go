package ollama_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xrr "hop.top/xrr"
	xhttp "hop.top/xrr/adapters/http"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

// xrrTransport routes every request through an xrr session, so an
// adapter resolved with [llm.WithTransport] replays a cassette.
type xrrTransport struct {
	session *xrr.FileSession
	inner   http.RoundTripper // nil in replay: never reached
}

func (x xrrTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}
	xreq := &xhttp.Request{Method: req.Method, URL: req.URL.String(), Body: string(body)}
	do := func() (xrr.Response, error) {
		r2 := req.Clone(req.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		resp, err := x.inner.RoundTrip(r2)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		rb, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return &xhttp.Response{
			Status:  resp.StatusCode,
			Headers: map[string]string{"Content-Type": resp.Header.Get("Content-Type")},
			Body:    string(rb),
		}, nil
	}
	got, err := x.session.Record(req.Context(), xhttp.NewAdapter(), xreq, do)
	if err != nil {
		return nil, err
	}
	var (
		status int
		ctype  = "application/json"
		rbody  string
	)
	switch r := got.(type) {
	case *xhttp.Response:
		status, rbody = r.Status, r.Body
	case *xrr.RawResponse:
		switch v := r.Payload["status"].(type) {
		case int:
			status = v
		case int64:
			status = int(v)
		case string:
			status, _ = strconv.Atoi(v)
		}
		rbody, _ = r.Payload["body"].(string)
	default:
		return nil, fmt.Errorf("xrr: unexpected response %T", got)
	}
	return &http.Response{
		Status:     http.StatusText(status),
		StatusCode: status,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": {ctype}},
		Body:    io.NopCloser(bytes.NewReader([]byte(rbody))),
		Request: req,
	}, nil
}

// TestTransport_XRRReplay replays a cassette recorded against an
// httptest fake. Re-record with OLLAMA_XRR_RECORD=1.
func TestTransport_XRRReplay(t *testing.T) {
	dir := filepath.Join("testdata", "xrr")
	uri := "ollama://llama3?base_url=http://ollama.transport.test"
	call := func(t *testing.T, opts ...llm.ResolveOption) llm.Response {
		t.Helper()
		p, err := llm.Resolve(uri, opts...)
		require.NoError(t, err)
		defer p.Close()
		resp, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
		return resp
	}

	if os.Getenv("OLLAMA_XRR_RECORD") != "" {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"recorded hello"},`+
				`"done":true,"eval_count":3,"prompt_eval_count":2}`)
		}))
		defer fake.Close()
		require.NoError(t, os.MkdirAll(dir, 0o750))
		inner := &http.Transport{}
		rec := xrrTransport{session: xrr.NewSession(xrr.ModeRecord, xrr.NewFileCassette(dir)), inner: inner}
		p, err := llm.Resolve("ollama://llama3?base_url="+fake.URL, llm.WithTransport(rec))
		require.NoError(t, err)
		defer p.Close()
		_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
			Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		})
		require.NoError(t, err)
		return
	}

	defHits := transporttest.BlockDefault(t)
	rep := xrrTransport{session: xrr.NewSession(xrr.ModeReplay, xrr.NewFileCassette(dir))}
	resp := call(t, llm.WithTransport(rep), llm.Hermetic())
	assert.Equal(t, "recorded hello", resp.Content)
	assert.Equal(t, 5, resp.Usage.TotalTokens)
	assert.Zero(t, defHits())
}
