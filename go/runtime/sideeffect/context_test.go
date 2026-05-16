package sideeffect_test

import (
	"context"
	"testing"

	"hop.top/kit/go/runtime/sideeffect"
)

func TestIsDryRun_NilContext(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // SA1012 — this test deliberately exercises nil-ctx behavior
	if sideeffect.IsDryRun(nil) {
		t.Fatalf("nil ctx must report false")
	}
}

func TestIsDryRun_UnsetContext(t *testing.T) {
	t.Parallel()
	if sideeffect.IsDryRun(context.Background()) {
		t.Fatalf("untagged ctx must report false")
	}
}

func TestIsDryRun_TrueRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := sideeffect.WithDryRun(context.Background(), true)
	if !sideeffect.IsDryRun(ctx) {
		t.Fatalf("WithDryRun(true) must round-trip")
	}
}

func TestIsDryRun_ExplicitFalseClears(t *testing.T) {
	t.Parallel()
	ctx := sideeffect.WithDryRun(context.Background(), true)
	ctx = sideeffect.WithDryRun(ctx, false)
	if sideeffect.IsDryRun(ctx) {
		t.Fatalf("WithDryRun(false) must clear the flag")
	}
}
