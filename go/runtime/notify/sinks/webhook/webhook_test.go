package webhooksink_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

// newEvent builds a stable test event so assertions don't depend on
// time.Now.
func newEvent() bus.Event {
	return bus.Event{
		Topic:     "kit.test.thing.created",
		Source:    "webhook-test",
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Payload:   map[string]any{"id": "42", "secret": "AKIAIOSFODNN7EXAMPLE"},
	}
}

func TestNew_DefaultsApply(t *testing.T) {
	t.Parallel()

	// Construction has no IO; should not panic and should return a
	// usable bus.Sink even with the zero option set.
	s := webhooksink.New("http://example.invalid")
	require.NotNil(t, s)
	assert.NoError(t, s.Close(), "Close should be a no-op without resources")
}

func TestDrain_Success(t *testing.T) {
	t.Parallel()

	var (
		gotBody []byte
		gotCT   string
		hits    int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL)
	require.NoError(t, s.Drain(context.Background(), newEvent()))

	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
	assert.Equal(t, "application/json", gotCT)

	var got map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &got))
	assert.Equal(t, "kit.test.thing.created", got["topic"])
	assert.Equal(t, "webhook-test", got["source"])
}

func TestDrain_Non2xxIsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL)
	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "boom")
}

func TestDrain_Non2xxBodyTruncatedTo512(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(huge))
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL)
	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	// The error message should contain at most ~512 bytes of body.
	// Allow some headroom for the "webhook: http 400: " prefix.
	assert.Less(t, len(err.Error()), 700, "error body must be truncated")
}

func TestDrain_Timeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL, webhooksink.WithTimeout(20*time.Millisecond))
	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	// http.Client.Timeout surfaces as a (deadline-exceeded) error
	// wrapped in url.Error; errors.Is unwraps it.
	assert.True(t,
		errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "Timeout") || strings.Contains(err.Error(), "timeout"),
		"expected a timeout-class error, got: %v", err,
	)
}

func TestWithAuthBearer_AddsHeader(t *testing.T) {
	t.Parallel()

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL, webhooksink.WithAuthBearer("s3cr3t"))
	require.NoError(t, s.Drain(context.Background(), newEvent()))
	assert.Equal(t, "Bearer s3cr3t", got)
}

func TestWithHeader_AllowsMultiple(t *testing.T) {
	t.Parallel()

	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := webhooksink.New(srv.URL,
		webhooksink.WithHeader("X-Trace", "abc"),
		webhooksink.WithHeader("X-Channel", "alerts"),
		webhooksink.WithHeader("X-Trace", "def"), // duplicate key should accumulate
	)
	require.NoError(t, s.Drain(context.Background(), newEvent()))

	assert.Equal(t, []string{"abc", "def"}, got.Values("X-Trace"))
	assert.Equal(t, "alerts", got.Get("X-Channel"))
}

func TestSlackTemplate_ProducesValidJSON(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	tmpl, err := webhooksink.SlackTemplate("alert: {{.Topic}}")
	require.NoError(t, err)

	s := webhooksink.New(srv.URL, webhooksink.WithTemplate(tmpl))
	require.NoError(t, s.Drain(context.Background(), newEvent()))

	var got struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(gotBody, &got))
	assert.Equal(t, "alert: kit.test.thing.created", got.Text)
}

func TestRedactor_RunsOnRenderedBody(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	r := redact.New()
	_, err := r.AddRule("aws-access-key", `AKIA[0-9A-Z]{16}`, "")
	require.NoError(t, err)

	s := webhooksink.New(srv.URL, webhooksink.WithRedactor(r))
	require.NoError(t, s.Drain(context.Background(), newEvent()))

	body := string(gotBody)
	assert.NotContains(t, body, "AKIAIOSFODNN7EXAMPLE", "redactor must scrub the secret before egress")
	assert.Contains(t, body, "REDACTED", "default Mask strategy emits ***REDACTED***")
}

func TestBreaker_OpenCircuitShortCircuits(t *testing.T) {
	t.Parallel()

	const breakerName = "webhook-test-trip"
	t.Cleanup(func() { breaker.Unregister(breakerName) })

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := breaker.New(breakerName)
	b.Trip("test")

	s := webhooksink.New(srv.URL, webhooksink.WithBreaker(b))
	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	assert.True(t, errors.Is(err, breaker.ErrBrokenCircuit),
		"open circuit must surface ErrBrokenCircuit so RetrySink can detect terminal state; got: %v", err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&hits),
		"open circuit must short-circuit before any HTTP egress")
}

func TestBreaker_ClosedCircuitPasses(t *testing.T) {
	t.Parallel()

	const breakerName = "webhook-test-pass"
	t.Cleanup(func() { breaker.Unregister(breakerName) })

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := breaker.New(breakerName)
	s := webhooksink.New(srv.URL, webhooksink.WithBreaker(b))

	require.NoError(t, s.Drain(context.Background(), newEvent()))
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

func TestWithHTTPClient_OverrideRespectsBreakerWrap(t *testing.T) {
	t.Parallel()

	const breakerName = "webhook-test-custom-client"
	t.Cleanup(func() { breaker.Unregister(breakerName) })

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := breaker.New(breakerName)
	b.Trip("test")

	// Caller supplies a client; breaker must still wrap its Transport.
	custom := &http.Client{Timeout: 2 * time.Second}
	s := webhooksink.New(srv.URL,
		webhooksink.WithHTTPClient(custom),
		webhooksink.WithBreaker(b),
	)

	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	assert.True(t, errors.Is(err, breaker.ErrBrokenCircuit),
		"WithBreaker must wrap a caller-supplied client's Transport too; got: %v", err)
	assert.Equal(t, int32(0), atomic.LoadInt32(&hits))
}

func TestDrain_BadURLReturnsError(t *testing.T) {
	t.Parallel()

	// http.NewRequestWithContext rejects URLs with control bytes.
	s := webhooksink.New("http://example.invalid/\x7f")
	err := s.Drain(context.Background(), newEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook")
}
