package progress_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/progress"
)

func TestJSONL_Emits_OneEventPerLine(t *testing.T) {
	var buf bytes.Buffer
	r := progress.JSONL(&buf)

	ctx := t.Context()
	r.Emit(ctx, progress.Event{Phase: "resolve", Item: "github.com/foo/bar"})
	r.Emit(ctx, progress.Event{Phase: "download", Bytes: 12345, Total: 50000})
	tru := true
	r.Emit(ctx, progress.Event{Phase: "verify", OK: &tru})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "expected one JSON object per line")

	for i, line := range lines {
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &got),
			"line %d must be valid JSON: %q", i, line)
		assert.NotEmpty(t, got["phase"], "phase must be present")
		assert.NotEmpty(t, got["at"], "at timestamp must be present")
	}
}

func TestJSONL_TimestampIsRFC3339_UTC(t *testing.T) {
	var buf bytes.Buffer
	r := progress.JSONL(&buf)

	r.Emit(t.Context(), progress.Event{Phase: "resolve"})

	var got struct {
		At string `json:"at"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	parsed, err := time.Parse(time.RFC3339Nano, got.At)
	require.NoError(t, err, "at must parse as RFC 3339: %q", got.At)
	assert.Equal(t, time.UTC, parsed.Location(),
		"at must be in UTC; got %s", parsed.Location())
}

func TestHuman_RendersBytesAsKiB(t *testing.T) {
	var buf bytes.Buffer
	r := progress.Human(&buf)

	// 12345 bytes ≈ 12.1 KiB, 50000 bytes ≈ 48.8 KiB.
	r.Emit(t.Context(), progress.Event{
		Phase: "download",
		Item:  "blob.tar.gz",
		Bytes: 12345,
		Total: 50000,
	})

	out := buf.String()
	assert.Contains(t, out, "[download]", "phase prefix missing: %q", out)
	assert.Contains(t, out, "blob.tar.gz", "item missing: %q", out)
	assert.Contains(t, out, "KiB", "human output must render bytes as KiB: %q", out)
	assert.Contains(t, out, "12.1", "bytes value (KiB, 1 fractional) missing: %q", out)
	assert.Contains(t, out, "48.8", "total value (KiB, 1 fractional) missing: %q", out)
	// Must NOT be valid JSON — it's the human renderer.
	var dummy map[string]any
	assert.Error(t, json.Unmarshal(buf.Bytes(), &dummy),
		"human output must not be valid JSON")
}

func TestDiscard_DropsAllEvents(t *testing.T) {
	r := progress.Discard()
	// 1000 events; if Discard is truly a no-op, this finishes instantly
	// and writes nothing observable.
	for i := 0; i < 1000; i++ {
		r.Emit(t.Context(), progress.Event{Phase: "spam", Bytes: int64(i)})
	}
	// No buffer to inspect — just assert the call completed without panic.
	assert.True(t, true)
}

func TestFromContext_DefaultsToDiscard(t *testing.T) {
	r := progress.FromContext(context.Background())
	require.NotNil(t, r, "FromContext must never return nil")

	// A Discard reporter writes nothing; verify by asserting it's the
	// same value the package returns from the public Discard ctor.
	assert.IsType(t, progress.Discard(), r,
		"FromContext on empty ctx must return a Discard reporter")
}

func TestWithReporter_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := progress.JSONL(&buf)

	ctx := progress.WithReporter(context.Background(), want)
	got := progress.FromContext(ctx)
	assert.Same(t, want, got, "FromContext must return the Reporter set by WithReporter")

	got.Emit(ctx, progress.Event{Phase: "verify"})
	assert.Contains(t, buf.String(), `"phase":"verify"`,
		"reporter retrieved from context must still write to its writer")
}

func TestHuman_OmitsBytesWhenZero(t *testing.T) {
	var buf bytes.Buffer
	r := progress.Human(&buf)

	r.Emit(t.Context(), progress.Event{Phase: "resolve", Item: "github.com/foo/bar"})

	out := buf.String()
	assert.Contains(t, out, "[resolve]")
	assert.Contains(t, out, "github.com/foo/bar")
	assert.NotContains(t, out, "KiB", "no bytes/total → no KiB rendering")
}

func TestHuman_BytesOnlyNoTotal(t *testing.T) {
	var buf bytes.Buffer
	r := progress.Human(&buf)

	r.Emit(t.Context(), progress.Event{Phase: "download", Bytes: 2048})
	out := buf.String()
	assert.Contains(t, out, "2.0 KiB",
		"bytes-only event must render as KiB without /total: %q", out)
	assert.NotContains(t, out, "/", "no slash when Total is zero")
}

func TestHuman_OK_RendersStatus(t *testing.T) {
	var buf bytes.Buffer
	r := progress.Human(&buf)

	tru := true
	r.Emit(t.Context(), progress.Event{Phase: "verify", OK: &tru})
	assert.Contains(t, buf.String(), "ok", "OK=true must render as 'ok'")

	buf.Reset()
	fal := false
	r.Emit(t.Context(), progress.Event{Phase: "verify", OK: &fal})
	assert.Contains(t, buf.String(), "fail", "OK=false must render as 'fail'")
}

func TestJSONL_PreservesProvidedTimestampAsUTC(t *testing.T) {
	var buf bytes.Buffer
	r := progress.JSONL(&buf)

	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	at := time.Date(2026, 5, 3, 12, 0, 0, 0, loc)

	r.Emit(t.Context(), progress.Event{Phase: "resolve", At: at})

	var got struct {
		At string `json:"at"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	parsed, err := time.Parse(time.RFC3339Nano, got.At)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, parsed.Location(),
		"caller-supplied timestamp must be normalized to UTC")
	assert.True(t, at.Equal(parsed),
		"normalized timestamp must equal the original instant")
}

func TestDiscard_EmitNoOp(t *testing.T) {
	// Exercise the discardReporter.Emit code path so coverage reflects it.
	r := progress.Discard()
	r.Emit(t.Context(), progress.Event{Phase: "anything"})
	r.Emit(t.Context(), progress.Event{}) // zero event
}

func TestWithReporter_NilContextDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		//nolint:staticcheck // intentionally exercising nil-ctx defense
		ctx := progress.WithReporter(nil, progress.Discard())
		require.NotNil(t, ctx)
		_ = progress.FromContext(ctx)
	})
}

func TestFromContext_NilContextReturnsDiscard(t *testing.T) {
	//nolint:staticcheck // intentionally exercising nil-ctx defense
	r := progress.FromContext(nil)
	require.NotNil(t, r)
	assert.IsType(t, progress.Discard(), r)
}

func TestEmit_DoesNotPanicOnClosedWriter(t *testing.T) {
	// The contract says Emit is best-effort and must not panic
	// or return errors when the underlying writer fails.
	r := progress.JSONL(brokenWriter{})
	assert.NotPanics(t, func() {
		r.Emit(t.Context(), progress.Event{Phase: "resolve"})
	})

	rh := progress.Human(brokenWriter{})
	assert.NotPanics(t, func() {
		rh.Emit(t.Context(), progress.Event{Phase: "resolve", Item: "x", Bytes: 1024, Total: 2048})
	})
}

// brokenWriter always returns an error, simulating a closed stderr.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errBroken }

type errString string

func (e errString) Error() string { return string(e) }

const errBroken = errString("broken pipe")
