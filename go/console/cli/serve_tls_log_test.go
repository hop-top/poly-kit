package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/transport/api"
)

// eventually polls cond until it holds or 10s pass.
func eventually(t *testing.T, cond func() bool, msg string, args ...any) {
	t.Helper()
	require.Eventually(t, cond, 10*time.Second, 25*time.Millisecond, append([]any{msg}, args...)...)
}

// accepted reports whether a request with c completes.
// handshakeCounter is an observability provider that records the
// refusals a listener reports before any request exists.
type handshakeCounter struct {
	fakeObservability
	gotMu sync.Mutex
	got   []string
}

func (c *handshakeCounter) RecordHTTPRefusal(_ context.Context, service, code string) {
	c.gotMu.Lock()
	defer c.gotMu.Unlock()
	c.got = append(c.got, service+" "+code)
}

func (c *handshakeCounter) counted() []string {
	c.gotMu.Lock()
	defer c.gotMu.Unlock()
	return append([]string(nil), c.got...)
}

// logBuffer is a buffer a logger and a test share.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestAPITLSHandshakeFailuresAreLoggedAndCounted(t *testing.T) {
	f := newTLSFixture(t)
	for _, tc := range []struct {
		name   string
		args   []string
		logged bool
	}{
		{"at -V, logged at debug", []string{"-V"}, true},
		{"by default, counted and not logged", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := &handshakeCounter{}
			logs := &logBuffer{}
			r := guardRoot(t, f.tlsKeys(APIServiceName))
			WithObservability(counter)(r)
			r.Cmd.SetErr(logs)
			base, stop := serveAPI(t, r, tc.args...)
			defer stop()

			// Plain HTTP to the TLS listener, and a client that does not
			// trust the server's certificate.
			resp, err := http.Get(base + "/healthz")
			require.NoError(t, err)
			_ = resp.Body.Close()
			untrusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
			_, err = untrusting.Get(strings.Replace(base, "http://", "https://", 1) + "/healthz")
			require.Error(t, err)

			eventually(t, func() bool { return len(counter.counted()) == 2 }, "both failures are counted: %v", counter.counted())
			want := "api " + api.CodeTLSHandshake
			assert.Equal(t, []string{want, want}, counter.counted())
			if tc.logged {
				eventually(t, func() bool { return strings.Count(logs.String(), "tls: handshake failed") == 2 },
					"both failures are logged\n%s", logs)
				assert.Contains(t, logs.String(), "client sent an HTTP request to an HTTPS server")
				assert.Contains(t, logs.String(), "remote=127.0.0.1:")
			} else {
				assert.NotContains(t, logs.String(), "tls: handshake failed")
			}
		})
	}
}

func TestHandshakeLogIsRateLimited(t *testing.T) {
	var out bytes.Buffer
	logger := kitlog.WithVerbose(guardRoot(t, nil).Viper, 1)
	logger.SetOutput(&out)
	counter := &handshakeCounter{}
	r := guardRoot(t, nil)
	WithObservability(counter)(r)

	h := newHandshakeLog(r, "rpc", logger)
	h.limit = rate.NewLimiter(rate.Every(50*time.Millisecond), 3)
	for range 10 {
		h.failed("192.0.2.1:4000", "EOF")
	}
	assert.Len(t, counter.counted(), 10, "every failure is counted")
	assert.Equal(t, 3, strings.Count(out.String(), "tls: handshake failed"), "the burst is logged")

	time.Sleep(60 * time.Millisecond)
	h.failed("192.0.2.1:4000", "EOF")
	assert.Equal(t, 4, strings.Count(out.String(), "tls: handshake failed"))
	assert.Contains(t, out.String(), "suppressed=7", "the next line reports what was not logged")
}
