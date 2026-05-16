package tui_test

import (
	"errors"
	"testing"

	"hop.top/kit/go/console/tui"
	"hop.top/kit/go/core/upgrade"
)

func newTestChecker() *upgrade.Checker {
	return upgrade.New(
		upgrade.WithBinary("test", "1.0.0"),
	)
}

func TestBadge_InitialState(t *testing.T) {
	b := tui.NewBadge(newTestChecker(), testTheme())

	if !b.Loading() {
		t.Fatal("expected badge to be loading initially")
	}
	if b.Result() != nil {
		t.Fatal("expected nil result initially")
	}
}

func TestBadge_CheckDoneMsg_NoUpdate(t *testing.T) {
	b := tui.NewBadge(newTestChecker(), testTheme())

	msg := tui.CheckDoneMsg{Result: &upgrade.Result{
		Current:     "1.0.0",
		Latest:      "1.0.0",
		UpdateAvail: false,
	}}

	m, _ := b.Update(msg)
	badge := m.(tui.Badge)
	if badge.Loading() {
		t.Fatal("expected loading=false after CheckDoneMsg")
	}
	if badge.View().Content != "" {
		t.Fatalf("expected empty view for no update, got %q", badge.View().Content)
	}
}

func TestBadge_CheckDoneMsg_UpdateAvailable(t *testing.T) {
	b := tui.NewBadge(newTestChecker(), testTheme())

	msg := tui.CheckDoneMsg{Result: &upgrade.Result{
		Current:     "1.0.0",
		Latest:      "2.0.0",
		UpdateAvail: true,
	}}

	m, _ := b.Update(msg)
	badge := m.(tui.Badge)
	body := badge.View().Content
	if body == "" {
		t.Fatal("expected non-empty view when update available")
	}
}

func TestBadge_CheckDoneMsg_Error(t *testing.T) {
	b := tui.NewBadge(newTestChecker(), testTheme())

	msg := tui.CheckDoneMsg{Result: &upgrade.Result{
		Err: errors.New("network failure"),
	}}

	m, _ := b.Update(msg)
	badge := m.(tui.Badge)
	if badge.View().Content != "" {
		t.Fatal("expected empty view on error result")
	}
}

func TestBadge_ViewWhileLoading(t *testing.T) {
	b := tui.NewBadge(newTestChecker(), testTheme())

	if b.View().Content != "" {
		t.Fatal("expected empty view while loading")
	}
}
