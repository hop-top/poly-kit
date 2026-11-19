// Package llmxrr records and replays model-provider HTTP calls through an
// xrr session, so a test runs a real provider adapter against responses a
// real provider once returned.
//
// Test-only: import it from _test.go files, never from a release build.
//
// Wire a [Transport] into the client with [llm.WithTransport]; the process
// default transport is never touched:
//
//	tr, err := llmxrr.New("testdata/cassettes")
//	if err != nil { t.Fatal(err) }
//	c, err := llm.Resolve("openai://gpt-4o", llm.WithTransport(tr))
//
// Mode follows xrr: XRR_MODE=record|replay|passthrough. Unset means
// replay, so a plain `go test` never reaches the network. Record with
// XRR_MODE=record go test ./...; commit the cassettes.
//
// A cassette never holds a credential: request headers are not stored,
// secret query params are dropped, and secret shapes plus the keys named
// by [WithSecrets] or set in the provider environment variables are masked
// in URLs, request bodies and response bodies before fingerprinting, so
// replay matches whatever key the run uses. A failed exchange (transport
// error, non-2xx) is refused unless [AllowFailures] is set.
// [AssertCassettesClean] scans committed cassettes for leaks.
//
// A streamed (SSE) response is read whole and replayed whole.
package llmxrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	xrr "hop.top/xrr"
	xhttp "hop.top/xrr/adapters/http"
)

// minSecretLen is the shortest exact secret value masked: shorter values
// would mangle ordinary text.
const minSecretLen = 8

const placeholder = "<redacted>"

// envKeys are provider key variables whose values are masked when set.
var envKeys = []string{
	"OPENAI_API_KEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY",
	"GOOGLE_API_KEY", "GEMINI_API_KEY", "LLM_API_KEY",
}

// ErrRefused marks a recording refused because the live exchange failed:
// a transport error or a non-2xx status. Replay would hand a stored
// failure back forever, and record mode records only what is missing, so
// re-recording would never retry it.
var ErrRefused = errors.New("llmxrr: refusing to record a failed exchange")

// ErrStoredFailure marks a cassette entry holding a failed exchange.
// Replay will not hand it back as if the provider had said it; delete it
// and record again, or set [AllowFailures] when the failure is the point
// of the test.
var ErrStoredFailure = errors.New("llmxrr: cassette holds a failed exchange")

// Option configures [New].
type Option func(*Transport)

// WithSecrets adds exact secret values to mask, on top of the provider
// environment keys. Values shorter than 8 bytes are ignored.
func WithSecrets(values ...string) Option {
	return func(t *Transport) { t.secrets = append(t.secrets, values...) }
}

// AllowFailures lets record mode save a failed exchange and replay hand it
// back: a non-2xx response as a response, a transport error as an error.
// For tests of error handling.
func AllowFailures() Option { return func(t *Transport) { t.allowFailures = true } }

// WithNext sets the round tripper that carries record and passthrough
// requests to the network. Default: a clone of http.DefaultTransport.
func WithNext(rt http.RoundTripper) Option { return func(t *Transport) { t.next = rt } }

// Transport is an [http.RoundTripper] over an xrr session.
type Transport struct {
	session       *xrr.FileSession
	next          http.RoundTripper
	secrets       []string
	allowFailures bool
	adapter       xhttp.Adapter
	live          atomic.Int64
}

// New returns a Transport recording into or replaying from dir. The mode
// comes from XRR_MODE (record, replay, passthrough); unset is replay. An
// empty dir falls back to XRR_CASSETTE_DIR.
func New(dir string, opts ...Option) (*Transport, error) {
	if dir == "" {
		dir = os.Getenv(xrr.EnvCassetteDir)
	}
	mode := xrr.Mode(os.Getenv(xrr.EnvMode))
	switch mode {
	case "":
		mode = xrr.ModeReplay
	case xrr.ModeRecord, xrr.ModeReplay, xrr.ModePassthrough:
	default:
		return nil, fmt.Errorf("llmxrr: %s=%q is not a valid mode (want record|replay|passthrough)", xrr.EnvMode, mode)
	}
	var cass *xrr.FileCassette
	if mode != xrr.ModePassthrough {
		if dir == "" {
			return nil, fmt.Errorf("llmxrr: no cassette dir: pass one or set %s", xrr.EnvCassetteDir)
		}
		if mode == xrr.ModeRecord {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("llmxrr: %w", err)
			}
		}
		cass = xrr.NewFileCassette(dir)
	}
	t := &Transport{session: xrr.NewSession(mode, cass)}
	for _, o := range opts {
		o(t)
	}
	for _, k := range envKeys {
		t.secrets = append(t.secrets, os.Getenv(k))
	}
	t.secrets = keepSecrets(t.secrets)
	if t.next == nil {
		t.next = http.DefaultTransport.(*http.Transport).Clone()
	}
	return t, nil
}

// Mode is the session mode in effect.
func (t *Transport) Mode() xrr.Mode { return t.session.Mode() }

// Live is the number of requests that reached the network.
func (t *Transport) Live() int64 { return t.live.Load() }

// RoundTrip implements [http.RoundTripper].
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("llmxrr: read request body: %w", err)
		}
		body = b
	}
	// Headers are never stored; the URL and body are scrubbed.
	req := &xhttp.Request{Method: r.Method, URL: t.scrubURL(r.URL), Body: t.scrub(string(body))}
	fp, _ := t.adapter.Fingerprint(req)
	send := func() (*xhttp.Response, error) {
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		r2.ContentLength = int64(len(body))
		return t.send(r2)
	}

	var resp xrr.Response
	var err error
	switch t.session.Mode() {
	case xrr.ModePassthrough:
		resp, err = t.send2(send)
	case xrr.ModeReplay:
		resp, err = t.session.Record(r.Context(), &t.adapter, req, func() (xrr.Response, error) {
			return nil, errors.New("llmxrr: replay does not reach the network")
		})
	default:
		resp, err = t.record(r, req, send)
	}

	if errors.Is(err, xrr.ErrCassetteMiss) {
		return nil, fmt.Errorf("llmxrr: no recording for %s %s (fingerprint %s): the request differs from every recorded one; if the change is intended, record again with %s=record: %w",
			r.Method, req.URL, fp, xrr.EnvMode, err)
	}
	if t.session.Mode() == xrr.ModeReplay && !t.allowFailures {
		if what, failed := storedFailure(resp, err); failed {
			return nil, fmt.Errorf("%w: %s %s (fingerprint %s): %s; re-record it",
				ErrStoredFailure, r.Method, req.URL, fp, what)
		}
	}
	if err != nil {
		return nil, err
	}
	return t.response(r, resp), nil
}

// send2 runs the live send for passthrough.
func (t *Transport) send2(send func() (*xhttp.Response, error)) (xrr.Response, error) {
	out, err := send()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// record sends live, judges the result, and saves only what may be saved.
func (t *Transport) record(r *http.Request, req *xhttp.Request, send func() (*xhttp.Response, error)) (xrr.Response, error) {
	out, err := send()
	if !t.allowFailures {
		if rerr := checkRecordable(r.Method, req.URL, out, err); rerr != nil {
			return nil, rerr
		}
	}
	// xrr saves whatever do returns, error included.
	return t.session.Record(r.Context(), &t.adapter, req, func() (xrr.Response, error) {
		if err != nil {
			return nil, err
		}
		return out, nil
	})
}

// send carries r to the network and returns the scrubbed response.
func (t *Transport) send(r *http.Request) (*xhttp.Response, error) {
	t.live.Add(1)
	out, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, errors.New(t.scrub(transportCause(err)))
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, errors.New(t.scrub(err.Error()))
	}
	return &xhttp.Response{
		Status:  out.StatusCode,
		Headers: map[string]string{"Content-Type": out.Header.Get("Content-Type")},
		Body:    t.scrub(string(data)),
	}, nil
}

func checkRecordable(method, path string, out *xhttp.Response, err error) error {
	if err != nil {
		return fmt.Errorf("%w: %s %s: transport error: %s; nothing was written, fix the cause and record again",
			ErrRefused, method, path, err)
	}
	if out.Status < 200 || out.Status > 299 {
		return fmt.Errorf("%w: %s %s: status %d %s: %s; nothing was written, fix the cause and record again",
			ErrRefused, method, path, out.Status, http.StatusText(out.Status), snippet(out.Body))
	}
	return nil
}

// snippet is the start of an already scrubbed body on one line.
func snippet(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if s == "" {
		return "(empty body)"
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "..."
	}
	return s
}

// storedFailure describes a replayed entry that holds a failure.
func storedFailure(resp xrr.Response, err error) (string, bool) {
	raw, ok := resp.(*xrr.RawResponse)
	if !ok {
		return "", false
	}
	if err != nil {
		return "recorded error: " + err.Error(), true
	}
	if status, ok := raw.Payload["status"].(int); ok && (status < 200 || status > 299) {
		return fmt.Sprintf("status %d %s", status, http.StatusText(status)), true
	}
	return "", false
}

// response turns a session result into the http.Response the client sees.
func (t *Transport) response(r *http.Request, resp xrr.Response) *http.Response {
	status, ctype, text := http.StatusOK, "application/json", ""
	switch v := resp.(type) {
	case *xhttp.Response:
		status, text = v.Status, v.Body
		if c := v.Headers["Content-Type"]; c != "" {
			ctype = c
		}
	case *xrr.RawResponse:
		// Replay returns the payload as an untyped map.
		if s, ok := v.Payload["status"].(int); ok {
			status = s
		}
		if s, ok := v.Payload["body"].(string); ok {
			text = s
		}
		if h, ok := v.Payload["headers"].(map[string]any); ok {
			if c, ok := h["Content-Type"].(string); ok && c != "" {
				ctype = c
			}
		}
	}
	h := make(http.Header)
	h.Set("Content-Type", ctype)
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(text)),
		ContentLength: int64(len(text)),
		Request:       r,
	}
}

// secretParams are query params dropped from a recorded URL.
var secretParams = map[string]bool{
	"key": true, "api_key": true, "api-key": true, "apikey": true,
	"access_token": true, "token": true, "auth": true, "authorization": true,
	"x-api-key": true, "x-goog-api-key": true, "secret": true, "password": true,
}

// scrubURL is the URL a cassette stores: path and query only, secret
// params removed, secret shapes masked. The host is dropped so a recording
// made against one endpoint replays against any other; xrr's http
// fingerprint ignores it anyway.
func (t *Transport) scrubURL(u *url.URL) string {
	c := *u
	c.Scheme, c.Host, c.User, c.Fragment = "", "", nil, ""
	q := c.Query()
	for k := range q {
		if secretParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	c.RawQuery = q.Encode()
	return t.scrub(c.String())
}

// scrubPatterns mask credential shapes in text.
var scrubPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), placeholder},
	{regexp.MustCompile(`AIza[A-Za-z0-9_-]{16,}`), placeholder},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + placeholder},
	{regexp.MustCompile(`(?i)(\\?"(?:api[_-]?key|x-api-key|x-goog-api-key|authorization|access_token|secret|password|token)\\?"\s*:\s*\\?")[^"\\]*`), "${1}" + placeholder},
	{regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|api-key|access_token|token)=)[^&\s"'\\]*`), "${1}" + placeholder},
}

// scrub masks the exact secrets, then the credential shapes.
func (t *Transport) scrub(s string) string {
	for _, v := range t.secrets {
		s = strings.ReplaceAll(s, v, placeholder)
	}
	for _, p := range scrubPatterns {
		s = p.re.ReplaceAllString(s, p.with)
	}
	return s
}

// transportCause drops the *url.Error wrapper, whose URL can carry a
// key query param.
func transportCause(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// keepSecrets drops short and duplicate values, longest first so a value
// nested in another is not split.
func keepSecrets(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if len(v) >= minSecretLen && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
