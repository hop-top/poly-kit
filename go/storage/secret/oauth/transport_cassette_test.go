package oauth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hop.top/xrr"
	xrrhttp "hop.top/xrr/adapters/http"
)

// Cassette tests replay recorded token-endpoint exchanges from
// testdata/cassettes/<scenario>. Replay is the default and never touches the
// network. Recording is opt-in:
//
//	XRR_MODE=record go test ./go/storage/secret/oauth -run Cassette
//
// There are no real credentials: recording runs against local fake IdPs that
// reproduce the provider response shapes (see fake_idp_cassette_test.go). Every
// secret, token, assertion, client id and tenant id is generated at runtime
// and scrubbed before the exchange reaches the cassette.

// cassScrubOn gates the scrubber. It exists so a mutation check can flip it;
// keep it true.
const cassScrubOn = true

// cassCollected holds every runtime-generated sensitive value of the process,
// for the leak test.
var (
	cassMu        sync.Mutex
	cassCollected = map[string]string{}
)

func cassCollect(label, v string) {
	if len(v) < 8 {
		return
	}
	cassMu.Lock()
	cassCollected[v] = label
	cassMu.Unlock()
}

// cassRand returns n hex chars of fresh randomness.
func cassRand(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)[:n]
}

// cassUUID returns a random v4-shaped GUID, the shape of Entra tenant and
// application ids.
func cassUUID(t testing.TB) string {
	h := cassRand(t, 32)
	return fmt.Sprintf("%s-%s-4%s-a%s-%s", h[0:8], h[8:12], h[13:16], h[17:20], h[20:32])
}

type cassSub struct{ value, placeholder string }

// cassScrub rewrites exchanges into their cassette form: sensitive form
// fields and token fields become fixed placeholders, and known identifiers
// (tenant id, client id, secrets) are replaced wherever they appear, URLs and
// error text included. The cassette form is also what fingerprints the
// request, so it must be identical between recording and replay.
type cassScrub struct{ subs []cassSub }

// know registers a runtime value for textual replacement and for the leak
// test.
func (s *cassScrub) know(label, value string) string {
	cassCollect(label, value)
	s.subs = append(s.subs, cassSub{value, "scrubbed-" + label})
	return value
}

var (
	cassFormKeys = map[string]bool{
		"client_id": true, "client_secret": true, "client_assertion": true,
		"assertion": true, "refresh_token": true, "code": true, "code_verifier": true,
	}
	cassJSONKeys = map[string]bool{
		"access_token": true, "refresh_token": true, "id_token": true,
	}
)

func (s *cassScrub) text(in string) string {
	if !cassScrubOn {
		return in
	}
	for _, sub := range s.subs {
		in = strings.ReplaceAll(in, sub.value, sub.placeholder)
		in = strings.ReplaceAll(in, url.QueryEscape(sub.value), sub.placeholder)
	}
	return in
}

func (s *cassScrub) form(body string) string {
	if !cassScrubOn {
		return body
	}
	v, err := url.ParseQuery(body)
	if err != nil {
		return s.text(body)
	}
	for k, vals := range v {
		for i := range vals {
			if cassFormKeys[k] {
				vals[i] = "scrubbed-" + strings.ReplaceAll(k, "_", "-")
			} else {
				vals[i] = s.text(vals[i])
			}
		}
	}
	return v.Encode()
}

func (s *cassScrub) response(body string) string {
	if !cassScrubOn {
		return body
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return s.text(body)
	}
	for k := range m {
		if cassJSONKeys[k] {
			m[k] = "scrubbed-" + strings.ReplaceAll(k, "_", "-")
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return s.text(body)
	}
	return s.text(strings.TrimRight(buf.String(), "\n"))
}

// cassTransport is an http.RoundTripper that records or replays each
// exchange through xrr's http adapter. The recorded request and response are
// the scrubbed forms; the caller always receives the scrubbed response, so
// assertions are identical in record and replay.
type cassTransport struct {
	session *xrr.FileSession
	adapter *xrrhttp.Adapter
	base    http.RoundTripper
	scrub   *cassScrub
	hits    int
}

func (x *cassTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	x.hits++
	u := x.scrub.text(req.URL.String())
	creq := &xrrhttp.Request{Method: req.Method, URL: u, Body: x.scrub.form(string(body))}
	resp, err := x.session.Record(req.Context(), x.adapter, creq, func() (xrr.Response, error) {
		out := req.Clone(req.Context())
		out.Body = io.NopCloser(bytes.NewReader(body))
		r, err := x.base.RoundTrip(out)
		if err != nil {
			return nil, err
		}
		defer func() { _ = r.Body.Close() }()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		return &xrrhttp.Response{
			Status:  r.StatusCode,
			Headers: map[string]string{"Content-Type": r.Header.Get("Content-Type")},
			Body:    x.scrub.response(string(b)),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	var status int
	var out, ctype string
	switch v := resp.(type) {
	case *xrrhttp.Response:
		status, out, ctype = v.Status, v.Body, v.Headers["Content-Type"]
	case *xrr.RawResponse:
		switch s := v.Payload["status"].(type) {
		case int:
			status = s
		case float64:
			status = int(s)
		}
		out, _ = v.Payload["body"].(string)
		if h, ok := v.Payload["headers"].(map[string]any); ok {
			ctype, _ = h["Content-Type"].(string)
		}
	default:
		return nil, fmt.Errorf("xrr: unexpected response %T", resp)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{ctype}},
		Body:       io.NopCloser(strings.NewReader(out)),
		Request:    req,
	}, nil
}

var errCassetteNoNetwork = errors.New("cassette replay: network access refused")

type cassNoNet struct{}

func (cassNoNet) RoundTrip(*http.Request) (*http.Response, error) { return nil, errCassetteNoNetwork }

// cassHostRewrite sends every request to the fake IdP, keeping the Host
// header and path of the real provider URL so the fake can check them.
type cassHostRewrite struct{ addr string }

func (r cassHostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Host = req.URL.Host
	out.URL.Scheme = "http"
	out.URL.Host = r.addr
	return http.DefaultTransport.RoundTrip(out)
}

// cassEnv is one scenario's recording/replay environment.
type cassEnv struct {
	Client    *http.Client
	Recording bool
	dir       string
	tr        *cassTransport
}

func cassMode(t *testing.T) xrr.Mode {
	t.Helper()
	switch m := os.Getenv(xrr.EnvMode); m {
	case "":
		return xrr.ModeReplay
	case string(xrr.ModeRecord), string(xrr.ModeReplay):
		return xrr.Mode(m)
	default:
		t.Fatalf("%s=%q: want record or replay", xrr.EnvMode, m)
		return ""
	}
}

// cassStart wires scenario name. idp is only served when recording.
func cassStart(t *testing.T, name string, scrub *cassScrub, idp http.Handler) *cassEnv {
	t.Helper()
	mode := cassMode(t)
	dir := filepath.Join("testdata", "cassettes", name)
	var base http.RoundTripper = cassNoNet{}
	if mode == xrr.ModeRecord {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(idp)
		t.Cleanup(srv.Close)
		base = cassHostRewrite{addr: strings.TrimPrefix(srv.URL, "http://")}
	}
	tr := &cassTransport{
		session: xrr.NewSession(mode, xrr.NewFileCassette(dir)),
		adapter: xrrhttp.NewAdapter(),
		base:    base,
		scrub:   scrub,
	}
	env := &cassEnv{Client: &http.Client{Transport: tr}, Recording: mode == xrr.ModeRecord, dir: dir, tr: tr}
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("cassette dir: %v", err)
			return
		}
		if len(ents) != 2 || tr.hits != 1 {
			t.Errorf("scenario %s: %d cassette files, %d requests; want one exchange (2 files)", name, len(ents), tr.hits)
		}
		cassAssertClean(t, dir, nil)
	})
	return env
}
