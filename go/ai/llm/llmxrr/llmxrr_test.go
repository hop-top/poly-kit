package llmxrr_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	xrr "hop.top/xrr"

	"hop.top/kit/go/ai/llm/llmxrr"
)

const (
	liveKey  = "sk-live-abcdefghijklmnop1234"
	reply    = `{"choices":[{"message":{"content":"pong"}}]}`
	chatBody = `{"model":"m","messages":[{"role":"user","content":"ping"}]}`
)

// server counts hits and answers status/body.
type server struct {
	*httptest.Server
	hits   atomic.Int64
	status int
	body   string
	lastH  atomic.Value // http.Header
}

func newServer(t *testing.T, status int, body string) *server {
	t.Helper()
	s := &server{status: status, body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.lastH.Store(r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func newTransport(t *testing.T, mode xrr.Mode, dir string, opts ...llmxrr.Option) *llmxrr.Transport {
	t.Helper()
	t.Setenv(xrr.EnvMode, string(mode))
	tr, err := llmxrr.New(dir, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func post(t *testing.T, tr http.RoundTripper, url, body string) (*http.Response, string, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+liveKey)
	req.Header.Set("x-api-key", liveKey)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b), nil
}

func cassetteText(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			sb.Write(b)
			n++
		}
		return nil
	})
	if n == 0 {
		t.Fatalf("no cassette files in %s", dir)
	}
	return sb.String()
}

func TestRecordThenReplayWithoutServer(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(t, 200, reply)
	url := srv.URL + "/v1/chat/completions"

	rec := newTransport(t, xrr.ModeRecord, dir)
	_, got, err := post(t, rec, url, chatBody)
	if err != nil || got != reply {
		t.Fatalf("record: body=%q err=%v", got, err)
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("record hits = %d, want 1", srv.hits.Load())
	}

	srv.Close() // replay must not need the network
	rep := newTransport(t, xrr.ModeReplay, dir)
	resp, got, err := post(t, rep, url, chatBody)
	if err != nil || got != reply || resp.StatusCode != 200 {
		t.Fatalf("replay: status=%v body=%q err=%v", resp, got, err)
	}
}

func TestReplayMissIsExplicit(t *testing.T) {
	dir := t.TempDir()
	rep := newTransport(t, xrr.ModeReplay, dir)
	_, _, err := post(t, rep, "http://127.0.0.1:1/v1/chat/completions", chatBody)
	if !errors.Is(err, xrr.ErrCassetteMiss) {
		t.Fatalf("err = %v, want ErrCassetteMiss", err)
	}
}

func TestModeFollowsXRREnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(xrr.EnvMode, "")
	tr, err := llmxrr.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Mode() != xrr.ModeReplay {
		t.Fatalf("unset %s: mode = %q, want replay", xrr.EnvMode, tr.Mode())
	}
	t.Setenv(xrr.EnvMode, "record")
	if tr, _ = llmxrr.New(dir); tr.Mode() != xrr.ModeRecord {
		t.Fatalf("mode = %q, want record", tr.Mode())
	}
	t.Setenv(xrr.EnvMode, "bogus")
	if _, err := llmxrr.New(dir); err == nil {
		t.Fatal("bogus mode accepted")
	}
	t.Setenv(xrr.EnvMode, "record")
	t.Setenv(xrr.EnvCassetteDir, dir)
	if _, err := llmxrr.New(""); err != nil {
		t.Fatalf("dir from %s: %v", xrr.EnvCassetteDir, err)
	}
	t.Setenv(xrr.EnvCassetteDir, "")
	if _, err := llmxrr.New(""); err == nil {
		t.Fatal("no dir and no env accepted")
	}
}

func TestCassetteHoldsNoSecret(t *testing.T) {
	dir := t.TempDir()
	// The server echoes the key back in its body; the cassette must not keep it.
	echo := `{"echo":"Bearer ` + liveKey + `","api_key":"` + liveKey + `","ok":true}`
	srv := newServer(t, 200, echo)
	url := srv.URL + "/v1beta/models/g:generateContent?key=" + liveKey + "&api_key=" + liveKey + "&alt=sse"
	body := `{"api_key":"` + liveKey + `","note":"use Bearer ` + liveKey + `","q":"hi"}`

	rec := newTransport(t, xrr.ModeRecord, dir, llmxrr.WithSecrets("plain-secret-value-9"))
	if _, _, err := post(t, rec, url, body+` plain-secret-value-9`); err != nil {
		t.Fatal(err)
	}
	got := cassetteText(t, dir)
	for _, bad := range []string{liveKey, "plain-secret-value-9", "Authorization", "authorization", "x-api-key", "key=", srv.URL} {
		if strings.Contains(got, bad) {
			t.Errorf("cassette holds %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "alt=sse") || !strings.Contains(got, "q") {
		t.Errorf("non-secret request content lost:\n%s", got)
	}
	if err := llmxrr.CheckNoSecrets(dir, liveKey); err != nil {
		t.Fatalf("scan of scrubbed cassette: %v", err)
	}
}

func TestReplayIgnoresKeyChange(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(t, 200, reply)
	rec := newTransport(t, xrr.ModeRecord, dir)
	if _, _, err := post(t, rec, srv.URL+"/v1/chat/completions?api_key=sk-first-key-0123456789", chatBody); err != nil {
		t.Fatal(err)
	}
	rep := newTransport(t, xrr.ModeReplay, dir)
	if _, got, err := post(t, rep, srv.URL+"/v1/chat/completions?api_key=sk-other-key-9876543210", chatBody); err != nil || got != reply {
		t.Fatalf("replay with another key: body=%q err=%v", got, err)
	}
}

func TestEnvKeysAreScrubbed(t *testing.T) {
	dir := t.TempDir()
	const envKey = "not-shaped-like-a-key-123456"
	t.Setenv("OPENAI_API_KEY", envKey)
	srv := newServer(t, 200, `{"k":"`+envKey+`"}`)
	rec := newTransport(t, xrr.ModeRecord, dir)
	if _, _, err := post(t, rec, srv.URL+"/v1/chat/completions", `{"k":"`+envKey+`"}`); err != nil {
		t.Fatal(err)
	}
	if got := cassetteText(t, dir); strings.Contains(got, envKey) {
		t.Fatalf("cassette holds env key:\n%s", got)
	}
}

func TestRecordRefusesFailedExchange(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		down   bool
	}{
		"401":       {status: 401},
		"429":       {status: 429},
		"500":       {status: 500},
		"transport": {down: true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			srv := newServer(t, tc.status, `{"error":"nope `+liveKey+`"}`)
			url := srv.URL + "/v1/chat/completions"
			if tc.down {
				srv.Close()
			}
			rec := newTransport(t, xrr.ModeRecord, dir)
			_, _, err := post(t, rec, url, chatBody)
			if !errors.Is(err, llmxrr.ErrRefused) {
				t.Fatalf("err = %v, want ErrRefused", err)
			}
			if strings.Contains(err.Error(), liveKey) {
				t.Errorf("error leaks key: %v", err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("refused exchange wrote %d files", len(entries))
			}
		})
	}
}

func TestAllowFailuresRecordsAndReplaysThem(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(t, 429, `{"error":"slow down"}`)
	url := srv.URL + "/v1/chat/completions"
	rec := newTransport(t, xrr.ModeRecord, dir, llmxrr.AllowFailures())
	resp, _, err := post(t, rec, url, chatBody)
	if err != nil || resp.StatusCode != 429 {
		t.Fatalf("record: resp=%v err=%v", resp, err)
	}
	srv.Close()

	rep := newTransport(t, xrr.ModeReplay, dir, llmxrr.AllowFailures())
	resp, got, err := post(t, rep, url, chatBody)
	if err != nil || resp.StatusCode != 429 || !strings.Contains(got, "slow down") {
		t.Fatalf("replay: resp=%v body=%q err=%v", resp, got, err)
	}
}

func TestReplayRefusesStoredFailureByDefault(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(t, 500, `{"error":"boom"}`)
	url := srv.URL + "/v1/chat/completions"
	rec := newTransport(t, xrr.ModeRecord, dir, llmxrr.AllowFailures())
	if _, _, err := post(t, rec, url, chatBody); err != nil {
		t.Fatal(err)
	}
	rep := newTransport(t, xrr.ModeReplay, dir)
	if _, _, err := post(t, rep, url, chatBody); !errors.Is(err, llmxrr.ErrStoredFailure) {
		t.Fatalf("err = %v, want ErrStoredFailure", err)
	}
	if err := llmxrr.CheckNoFailures(dir); !errors.Is(err, llmxrr.ErrStoredFailure) {
		t.Fatalf("CheckNoFailures = %v, want ErrStoredFailure", err)
	}
}

func TestCheckNoSecretsCatchesPlantedKey(t *testing.T) {
	cases := map[string]string{
		"openai key":  "body: 'x sk-proj-abcdefghijklmnop1234'\n",
		"gemini key":  "body: 'AIzaSyA1234567890abcdefghijklmnopqrstu'\n",
		"query param": "url: /v1/m?key=abc123\n",
		"api_key":     "url: /v1/m?x=1&api_key=abc123\n",
		"bearer":      "body: 'Bearer abcdefghijklmnop12345'\n",
		"header key":  "headers:\n  Authorization: foo\n",
		"x-api-key":   "headers:\n  x-api-key: foo\n",
		"json field":  `body: '{"api_key":"hunter2hunter2"}'` + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "a.req.yaml"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := llmxrr.CheckNoSecrets(dir); err == nil {
				t.Fatal("planted secret not found")
			}
		})
	}

	t.Run("exact value", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("body: opaque-token-value-77\n"), 0o600)
		if err := llmxrr.CheckNoSecrets(dir, "opaque-token-value-77"); err == nil {
			t.Fatal("exact secret not found")
		}
	})
	t.Run("clean and missing dir", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("body: 'the authorization flow, key=value talk, <redacted>'\nurl: /v1/x?alt=sse\n"), 0o600)
		if err := llmxrr.CheckNoSecrets(dir); err != nil {
			t.Fatalf("clean cassette flagged: %v", err)
		}
		if err := llmxrr.CheckNoSecrets(filepath.Join(dir, "absent")); err != nil {
			t.Fatalf("missing dir: %v", err)
		}
	})
}

type fakeTB struct {
	testing.TB
	failed bool
	msg    string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = format
}

func TestAssertCassettesClean(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.resp.yaml"), []byte("adapter: http\npayload:\n  status: 200\n"), 0o600)
	clean := &fakeTB{}
	llmxrr.AssertCassettesClean(clean, dir)
	if clean.failed {
		t.Fatal("clean dir failed")
	}
	_ = os.WriteFile(filepath.Join(dir, "b.req.yaml"), []byte("url: /x?api_key=zzz\n"), 0o600)
	dirty := &fakeTB{}
	llmxrr.AssertCassettesClean(dirty, dir)
	if !dirty.failed {
		t.Fatal("leak not reported")
	}
}

func TestRoundTripperIsUsableAsLLMTransport(t *testing.T) {
	var _ http.RoundTripper = (*llmxrr.Transport)(nil)
}
