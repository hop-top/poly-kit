package transporttest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	xrr "hop.top/xrr"
	xhttp "hop.top/xrr/adapters/http"

	"hop.top/kit/go/ai/llm"
)

// XRRTransport routes every request through an xrr session, so an
// adapter resolved with [llm.WithTransport] records to, or replays,
// a cassette.
type XRRTransport struct {
	Session *xrr.FileSession
	Inner   http.RoundTripper // nil in replay: never reached
}

func (x XRRTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
		resp, err := x.Inner.RoundTrip(r2)
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
	got, err := x.Session.Record(req.Context(), xhttp.NewAdapter(), xreq, do)
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

// Fake is a loopback server that answers every request with a canned
// body and keeps the last request it saw.
type Fake struct {
	*httptest.Server
	mu   sync.Mutex
	body []byte
	path string
}

// NewFake starts a [Fake], closed at cleanup.
func NewFake(t *testing.T, contentType, body string) *Fake {
	t.Helper()
	f := &Fake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.body, f.path = b, r.URL.Path
		f.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

// Body is the last request's body.
func (f *Fake) Body() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body
}

// Path is the last request's URL path.
func (f *Fake) Path() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path
}

// Cassette replays an xrr cassette of an adapter's exchange with a
// loopback fake: no network and no credential, in CI or anywhere.
// Re-record, offline and scrubbed by construction (the fake sees only
// what the test sends), by setting the variable named in Env.
type Cassette struct {
	Dir         string // holds the cassette files
	Env         string // a non-empty value makes Run record
	ContentType string
	Reply       string // what the fake answers, when recording
	// URI is the provider URI for a base URL.
	URI func(base string) string
}

// replayBase is never dialed: the replay transport answers first. It
// is loopback because a key may not go over http to another host.
const replayBase = "http://127.0.0.1:9"

// Run makes the call, through a recording of the fake when c.Env is set,
// else from the cassette with the default transport blocked. call gets
// the adapter and must return nil on success.
func (c Cassette) Run(t *testing.T, call func(llm.Provider) error) {
	t.Helper()
	if os.Getenv(c.Env) != "" {
		fake := NewFake(t, c.ContentType, c.Reply)
		require.NoError(t, os.MkdirAll(c.Dir, 0o750))
		rec := XRRTransport{Session: xrr.NewSession(xrr.ModeRecord, xrr.NewFileCassette(c.Dir)), Inner: &http.Transport{}}
		p, err := llm.Resolve(c.URI(fake.URL), llm.WithTransport(rec))
		require.NoError(t, err)
		defer p.Close()
		require.NoError(t, call(p))
		return
	}
	defHits := BlockDefault(t)
	rep := XRRTransport{Session: xrr.NewSession(xrr.ModeReplay, xrr.NewFileCassette(c.Dir))}
	p, err := llm.Resolve(c.URI(replayBase), llm.WithTransport(rep), llm.Hermetic())
	require.NoError(t, err)
	defer p.Close()
	require.NoError(t, call(p))
	assert.Zero(t, defHits())
}

// VerdictSchema is a small object schema the structured-output tests
// share, and VerdictOK / VerdictBad an output that matches it and one
// that does not.
const (
	VerdictSchema = `{"type":"object","properties":{"label":{"type":"string","enum":["pass","fail"]},` +
		`"score":{"type":"integer"}},"required":["label","score"],"additionalProperties":false}`
	VerdictOK  = `{"label":"pass","score":7}`
	VerdictBad = `{"label":"pass"}`
)
