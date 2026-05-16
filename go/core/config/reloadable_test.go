package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

// reloadCfg is the fixture used by the reload-related tests. Endpoint
// is mutable; ListenAddr is immutable. The Embedded sub-struct exercises
// recursive partition (one mutable, one immutable leaf inside).
type reloadCfg struct {
	ListenAddr string `yaml:"listen_addr"`
	Endpoint   string `yaml:"endpoint" reload:"true"`
	Sub        subCfg `yaml:"sub"`
}

type subCfg struct {
	Threshold int    `yaml:"threshold" reload:"true"`
	BindHost  string `yaml:"bind_host"`
}

// recordingPublisher captures Publish calls under a mutex so tests can
// assert on emitted topics without race detector noise.
type recordingPublisher struct {
	mu     sync.Mutex
	events []capturedReloadEvent
}

type capturedReloadEvent struct {
	Topic   string
	Source  string
	Payload any
}

func (p *recordingPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedReloadEvent{Topic: topic, Source: source, Payload: payload})
	return nil
}

func (p *recordingPublisher) snapshot() []capturedReloadEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]capturedReloadEvent, len(p.events))
	copy(out, p.events)
	return out
}

// waitForEvent polls the recorder until at least n events on the given
// topic have arrived (publish is fire-and-forget on a goroutine).
func waitForEvent(t *testing.T, pub *recordingPublisher, topic string, n int) []capturedReloadEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got []capturedReloadEvent
		for _, e := range pub.snapshot() {
			if e.Topic == topic {
				got = append(got, e)
			}
		}
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events on topic %q (saw %v)", n, topic, pub.snapshot())
	return nil
}

func TestPartition_SplitsTaggedFields(t *testing.T) {
	var cfg reloadCfg
	mut, imm, err := config.Partition(&cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"endpoint", "sub.threshold"}, mut)
	assert.Equal(t, []string{"listen_addr", "sub.bind_host"}, imm)
}

func TestPartition_EmbeddedStruct(t *testing.T) {
	var cfg EmbedOuter
	mut, imm, err := config.Partition(&cfg)
	require.NoError(t, err)
	// Embedded inner is anonymous + untagged → leaves inline at top level.
	assert.Equal(t, []string{"b"}, mut)
	assert.Equal(t, []string{"a", "c"}, imm)
}

// EmbedInner / EmbedOuter exercise the anonymous-embed branch of
// Partition. Types must be package-level so the embedded field is
// exported (anonymous fields inherit the type's exported-ness).
type EmbedInner struct {
	A string `yaml:"a"`
	B string `yaml:"b" reload:"true"`
}

// EmbedOuter inlines EmbedInner (no yaml tag on the embed) so its
// leaves appear at the top level, matching yaml's "inline" default.
type EmbedOuter struct {
	EmbedInner
	C string `yaml:"c"`
}

func TestPartition_RejectsNonStructPointer(t *testing.T) {
	x := 0
	_, _, err := config.Partition(&x)
	require.Error(t, err)
}

func writeReloadYAML(t *testing.T, dir string, c reloadCfg) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	path := filepath.Join(dir, "config.yaml")
	body := []byte(
		"listen_addr: " + c.ListenAddr + "\n" +
			"endpoint: " + c.Endpoint + "\n" +
			"sub:\n" +
			"  threshold: " + itoa(c.Sub.Threshold) + "\n" +
			"  bind_host: " + c.Sub.BindHost + "\n",
	)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

func itoa(i int) string {
	// avoid importing strconv just for one int → string.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func TestReload_SnapshotSwapsAtomically(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://api.example.com",
		Sub:        subCfg{Threshold: 5, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	r := config.New(&loaded, opts)
	require.Equal(t, "https://api.example.com", r.Snapshot().Endpoint)

	// Mutable-only change: bump endpoint + threshold.
	updated := initial
	updated.Endpoint = "https://api.example.org"
	updated.Sub.Threshold = 9
	writeReloadYAML(t, dir, updated)

	require.NoError(t, r.Reload(opts))
	snap := r.Snapshot()
	assert.Equal(t, "https://api.example.org", snap.Endpoint)
	assert.Equal(t, 9, snap.Sub.Threshold)
	// Immutable fields stay where they were because the file kept them.
	assert.Equal(t, ":8080", snap.ListenAddr)
}

func TestReload_VetoesImmutableChange(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://api.example.com",
		Sub:        subCfg{Threshold: 5, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	pub := &recordingPublisher{}
	r := config.New(&loaded, opts, config.WithReloadPublisher(pub))
	pre := r.Snapshot()

	// Flip an immutable field.
	updated := initial
	updated.ListenAddr = ":9090"
	writeReloadYAML(t, dir, updated)

	err := r.Reload(opts)
	require.Error(t, err)
	var imm *config.ErrImmutableChanged
	require.True(t, errors.As(err, &imm), "want ErrImmutableChanged, got %T", err)
	assert.Equal(t, []string{"listen_addr"}, imm.Paths)

	// Snapshot pointer must be unchanged.
	assert.Same(t, pre, r.Snapshot())

	// Failure event should land on the bus with the typed payload.
	events := waitForEvent(t, pub, string(config.DefaultReloadTopics.ReloadFailed), 1)
	require.Len(t, events, 1)
	failed, ok := events[0].Payload.(config.ReloadFailedPayload)
	require.True(t, ok, "want ReloadFailedPayload, got %T", events[0].Payload)
	assert.Equal(t, config.ReloadFailReasonImmutableChanged, failed.Reason)
	assert.Equal(t, []string{"listen_addr"}, failed.Offending)
}

func TestReload_PublishesSuccessDiff(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://api.example.com",
		Sub:        subCfg{Threshold: 5, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	pub := &recordingPublisher{}
	r := config.New(&loaded, opts, config.WithReloadPublisher(pub))

	updated := initial
	updated.Endpoint = "https://api.example.net"
	writeReloadYAML(t, dir, updated)
	require.NoError(t, r.Reload(opts))

	events := waitForEvent(t, pub, string(config.DefaultReloadTopics.Reloaded), 1)
	require.Len(t, events, 1)
	payload, ok := events[0].Payload.(config.ReloadedPayload)
	require.True(t, ok, "want ReloadedPayload, got %T", events[0].Payload)
	require.Contains(t, payload.MutableDiff, "endpoint")
	assert.Equal(t, "https://api.example.com", payload.MutableDiff["endpoint"].From)
	assert.Equal(t, "https://api.example.net", payload.MutableDiff["endpoint"].To)
}

func TestReload_LoadFailureLeavesSnapshot(t *testing.T) {
	// ExtraConfigPaths point to a missing file → Load returns an error.
	dir := t.TempDir()
	initial := reloadCfg{ListenAddr: ":8080", Endpoint: "https://x"}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	pub := &recordingPublisher{}
	r := config.New(&loaded, opts, config.WithReloadPublisher(pub))

	bad := opts
	bad.ExtraConfigPaths = []string{filepath.Join(dir, "does-not-exist.yaml")}
	pre := r.Snapshot()
	err := r.Reload(bad)
	require.Error(t, err)
	assert.Same(t, pre, r.Snapshot())

	events := waitForEvent(t, pub, string(config.DefaultReloadTopics.ReloadFailed), 1)
	failed, ok := events[0].Payload.(config.ReloadFailedPayload)
	require.True(t, ok)
	assert.Equal(t, config.ReloadFailReasonLoadError, failed.Reason)
}

// TestReload_ConcurrentReadersSafe spins up readers calling Snapshot in
// a tight loop while a writer keeps issuing Reloads. With -race, any
// torn read or unsynchronised write is fatal.
func TestReload_ConcurrentReadersSafe(t *testing.T) {
	dir := t.TempDir()
	initial := reloadCfg{
		ListenAddr: ":8080",
		Endpoint:   "https://a",
		Sub:        subCfg{Threshold: 1, BindHost: "127.0.0.1"},
	}
	path := writeReloadYAML(t, dir, initial)
	opts := config.Options{ProjectConfigPath: path}

	var loaded reloadCfg
	require.NoError(t, config.Load(&loaded, opts))
	r := config.New(&loaded, opts)

	stop := make(chan struct{})
	var reads int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := r.Snapshot()
				// Exercise both string and int leaves.
				_ = snap.Endpoint
				_ = snap.Sub.Threshold
				atomic.AddInt64(&reads, 1)
			}
		}()
	}

	for i := 0; i < 50; i++ {
		updated := initial
		updated.Endpoint = "https://" + itoa(i)
		updated.Sub.Threshold = i + 1
		writeReloadYAML(t, dir, updated)
		require.NoError(t, r.Reload(opts))
	}
	close(stop)
	wg.Wait()

	assert.Greater(t, atomic.LoadInt64(&reads), int64(0))
	final := r.Snapshot()
	assert.Equal(t, "https://49", final.Endpoint)
}
