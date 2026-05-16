//go:build os_notify_e2e

// Package osnotifysink integration tests. These shell out to the
// real osascript / notify-send binaries and pop a real desktop
// notification — they exist for local dev-loop confidence and are
// gated behind //go:build os_notify_e2e so CI never runs them.
//
// Run locally:
//
//	go test -tags os_notify_e2e ./go/runtime/notify/sinks/osnotify/...
package osnotifysink

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
)

// TestDrain_RealOsascript_Darwin actually runs osascript on darwin
// and asserts no error. A real notification will pop on screen.
func TestDrain_RealOsascript_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}

	sink, err := New(
		WithTitle(LiteralTemplate("kit notify e2e")),
		WithText(LiteralTemplate("hello from osnotify integration test")),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, sink.Drain(ctx, bus.Event{
		Topic:     "kit.notify.e2e",
		Source:    "osnotify-e2e",
		Timestamp: time.Now(),
	}))
}

// TestDrain_RealNotifySend_Linux actually runs notify-send on linux
// and asserts no error. A real notification will pop on screen.
func TestDrain_RealNotifySend_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only")
	}

	sink, err := New(
		WithTitle(LiteralTemplate("kit notify e2e")),
		WithText(LiteralTemplate("hello from osnotify integration test")),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, sink.Drain(ctx, bus.Event{
		Topic:     "kit.notify.e2e",
		Source:    "osnotify-e2e",
		Timestamp: time.Now(),
	}))
}
