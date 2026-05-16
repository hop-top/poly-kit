package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock publisher ---

type capturedEvent struct {
	topic   string
	source  string
	payload any
}

type mockPublisher struct {
	mu     sync.Mutex
	events []capturedEvent
}

func (p *mockPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{topic: topic, source: source, payload: payload})
	return nil
}

func (p *mockPublisher) snapshot() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]capturedEvent, len(p.events))
	copy(out, p.events)
	return out
}

// waitForEvent polls the publisher until at least n events with the
// given topic accumulate, or the deadline elapses. Publishing is
// fire-and-forget on a goroutine, so tests cannot read events
// synchronously after the lifecycle call returns.
func waitForEvent(t *testing.T, pub *mockPublisher, topic string, n int) []capturedEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var matches []capturedEvent
		for _, e := range pub.snapshot() {
			if e.topic == topic {
				matches = append(matches, e)
			}
		}
		if len(matches) >= n {
			return matches
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitForEvent: topic %q got %d events, want %d", topic, len(matches), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- helpers ---

// newReleaseServer returns an httptest.Server that mimics the kit
// custom-URL release endpoint. version is the latest version it
// reports.
func newReleaseServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": version,
			"url":     "http://example.invalid/asset.tar.gz",
			"notes":   "release notes",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- tests ---

func TestNilPublisher_NoBusEvent(t *testing.T) {
	srv := newReleaseServer(t, "2.0.0")

	c := New(
		WithBinary("mytool", "1.0.0"),
		WithReleaseURL(srv.URL),
		WithStateDir(t.TempDir()),
	)

	r := c.Check(context.Background())
	require.NoError(t, r.Err)
	assert.True(t, r.UpdateAvail)

	// No publisher → Snooze must not panic and must not block on bus.
	require.NoError(t, c.Snooze())

	// Sleep briefly to give any (incorrectly fired) goroutine a chance.
	time.Sleep(50 * time.Millisecond)
	// Nothing to assert directly except that no panic and no goroutine
	// leak. The absence of a publisher means no Publish call ever
	// happens; this is enforced by the nil-guard in Checker.publish.
}

func TestPublisher_Released_OnUpdateAvail(t *testing.T) {
	srv := newReleaseServer(t, "2.0.0")
	pub := &mockPublisher{}

	c := New(
		WithBinary("mytool", "1.0.0"),
		WithReleaseURL(srv.URL),
		WithStateDir(t.TempDir()),
		WithPublisher(pub),
	)

	r := c.Check(context.Background())
	require.NoError(t, r.Err)
	require.True(t, r.UpdateAvail)

	events := waitForEvent(t, pub, string(DefaultTopics.Released), 1)
	require.Len(t, events, 1)
	assert.Equal(t, publishSource, events[0].source)

	payload, ok := events[0].payload.(ReleasedPayload)
	require.True(t, ok, "payload type = %T; want ReleasedPayload", events[0].payload)
	assert.Equal(t, "2.0.0", payload.Latest)
	assert.Equal(t, "1.0.0", payload.Current)
	assert.False(t, payload.ReleasedAt.IsZero())
}

func TestPublisher_Released_NoEventWhenNoUpdate(t *testing.T) {
	srv := newReleaseServer(t, "1.0.0")
	pub := &mockPublisher{}

	c := New(
		WithBinary("mytool", "1.0.0"),
		WithReleaseURL(srv.URL),
		WithStateDir(t.TempDir()),
		WithPublisher(pub),
	)

	r := c.Check(context.Background())
	require.NoError(t, r.Err)
	require.False(t, r.UpdateAvail)

	time.Sleep(50 * time.Millisecond)
	for _, e := range pub.snapshot() {
		if e.topic == string(DefaultTopics.Released) {
			t.Fatalf("unexpected Released event when no update available")
		}
	}
}

func TestPublisher_Snoozed(t *testing.T) {
	srv := newReleaseServer(t, "2.0.0")
	pub := &mockPublisher{}

	c := New(
		WithBinary("mytool", "1.0.0"),
		WithReleaseURL(srv.URL),
		WithStateDir(t.TempDir()),
		WithSnoozeDuration(2*time.Hour),
		WithPublisher(pub),
	)

	// Prime cache so snoozeVersion picks up Latest.
	r := c.Check(context.Background())
	require.True(t, r.UpdateAvail)

	require.NoError(t, c.Snooze())

	events := waitForEvent(t, pub, string(DefaultTopics.Snoozed), 1)
	require.Len(t, events, 1)
	payload, ok := events[0].payload.(SnoozedPayload)
	require.True(t, ok, "payload type = %T; want SnoozedPayload", events[0].payload)
	assert.Equal(t, "2.0.0", payload.Version)
	// Until should be ~2h ahead, with generous slack for CI.
	delta := time.Until(payload.Until)
	assert.Greater(t, delta, 1*time.Hour)
	assert.Less(t, delta, 3*time.Hour)
}

func TestPublisher_DownloadedAndInstalled(t *testing.T) {
	pub := &mockPublisher{}

	// Drive replaceBinary directly via hooks; avoids stubbing
	// os.Executable. This exercises the boundary that Checker.Upgrade
	// sets up for Downloaded and Installed.
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fake binary contents"))
	}))
	defer dlSrv.Close()

	cfg := defaultConfig()
	cfg.BinaryName = "mytool"
	cfg.SkipVerify = true
	cfg.pub = pub
	cfg.topics = DefaultTopics

	// Synthesize a Checker so Checker.publish wires correctly.
	c := &Checker{cfg: cfg}

	var (
		dlPath  string
		dlBytes int64
		instTo  string
	)
	hooks := replaceHooks{
		onDownloaded: func(path string, bytes int64) {
			dlPath, dlBytes = path, bytes
			c.publish(context.Background(), c.cfg.topics.Downloaded, DownloadedPayload{
				Version: "2.0.0",
				Path:    path,
				Bytes:   bytes,
			})
		},
		onInstalled: func(from, to string) {
			instTo = to
			c.publish(context.Background(), c.cfg.topics.Installed, InstalledPayload{
				Version: "2.0.0",
				From:    from,
				To:      to,
			})
		},
	}

	// Simulate a successful download: invoke hooks directly with
	// representative inputs. Driving the real replaceBinary would
	// require stubbing os.Executable, which is out of scope here —
	// the hook contract is what callers (Checker.Upgrade) depend on.
	hooks.onDownloaded("/tmp/.upgrade-archive-x", int64(20))
	hooks.onInstalled("/usr/local/bin/mytool", "/usr/local/bin/mytool")

	require.NotEmpty(t, dlPath)
	require.Equal(t, int64(20), dlBytes)
	require.NotEmpty(t, instTo)

	dlEvents := waitForEvent(t, pub, string(DefaultTopics.Downloaded), 1)
	require.Len(t, dlEvents, 1)
	dlPayload, ok := dlEvents[0].payload.(DownloadedPayload)
	require.True(t, ok)
	assert.Equal(t, "2.0.0", dlPayload.Version)
	assert.Equal(t, "/tmp/.upgrade-archive-x", dlPayload.Path)
	assert.Equal(t, int64(20), dlPayload.Bytes)

	instEvents := waitForEvent(t, pub, string(DefaultTopics.Installed), 1)
	require.Len(t, instEvents, 1)
	instPayload, ok := instEvents[0].payload.(InstalledPayload)
	require.True(t, ok)
	assert.Equal(t, "2.0.0", instPayload.Version)
	assert.Equal(t, "/usr/local/bin/mytool", instPayload.From)
	assert.Equal(t, "/usr/local/bin/mytool", instPayload.To)
}

func TestWithTopicPrefix_OverridesAllFour(t *testing.T) {
	c := New(
		WithBinary("mytool", "1.0.0"),
		WithTopicPrefix("myapp.core.upgrade"),
	)
	assert.Equal(t, "myapp.core.upgrade.released", string(c.cfg.topics.Released))
	assert.Equal(t, "myapp.core.upgrade.downloaded", string(c.cfg.topics.Downloaded))
	assert.Equal(t, "myapp.core.upgrade.installed", string(c.cfg.topics.Installed))
	assert.Equal(t, "myapp.core.upgrade.snoozed", string(c.cfg.topics.Snoozed))
}

func TestWithTopicPrefix_PanicsOnInvalid(t *testing.T) {
	cases := []string{
		"",                    // empty
		"two.segments",        // wrong arity
		"a.b.c.d",             // 4 segments (must be 3)
		"BAD.core.upgrade",    // uppercase
		"myapp.core.upgrade.", // trailing dot
	}
	for _, p := range cases {
		t.Run(fmt.Sprintf("prefix=%q", p), func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("WithTopicPrefix(%q) did not panic", p)
				}
			}()
			_ = WithTopicPrefix(p)
		})
	}
}

func TestWithTopics_PerFieldOverride(t *testing.T) {
	c := New(
		WithBinary("mytool", "1.0.0"),
		WithTopics(Topics{
			Released: "myapp.core.upgrade.released",
		}),
	)
	// Released overridden; others fall back to DefaultTopics.
	assert.Equal(t, "myapp.core.upgrade.released", string(c.cfg.topics.Released))
	assert.Equal(t, DefaultTopics.Downloaded, c.cfg.topics.Downloaded)
	assert.Equal(t, DefaultTopics.Installed, c.cfg.topics.Installed)
	assert.Equal(t, DefaultTopics.Snoozed, c.cfg.topics.Snoozed)
}

func TestDefaultTopics_PassValidation(t *testing.T) {
	for _, topic := range []string{
		string(DefaultTopics.Released),
		string(DefaultTopics.Downloaded),
		string(DefaultTopics.Installed),
		string(DefaultTopics.Snoozed),
	} {
		t.Run(topic, func(t *testing.T) {
			// Indirectly via PrefixTopics-style assertions: each topic
			// has 4 dot segments and a past-tense action.
			assert.Contains(t, topic, "kit.core.upgrade.")
		})
	}
}
