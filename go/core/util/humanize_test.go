package util

import (
	"testing"
	"time"
)

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "0s"},
		{3 * time.Second, "3s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{36 * time.Hour, "1d"},
		{10 * 24 * time.Hour, "1w"},
		{60 * 24 * time.Hour, "2mo"},
		{400 * 24 * time.Hour, "1y"},
	}
	for _, tt := range tests {
		if got := HumanDuration(tt.d); got != tt.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		b    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{3 * 1024 * 1024, "3.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
		{5 * 1024 * 1024 * 1024 * 1024, "5.0 TB"},
	}
	for _, tt := range tests {
		if got := HumanBytes(tt.b); got != tt.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tt.b, got, tt.want)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-2 * time.Hour), "2h ago"},
		{now.Add(-36 * time.Hour), "yesterday"},
		{now.Add(-72 * time.Hour), "3d ago"},
	}
	for _, tt := range tests {
		if got := RelativeTime(tt.t); got != tt.want {
			t.Errorf("RelativeTime(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestRelativeTimeFuture(t *testing.T) {
	future := time.Now().Add(5*time.Minute + 30*time.Second)
	got := RelativeTime(future)
	if got != "in 5m" {
		t.Errorf("got %q, want %q", got, "in 5m")
	}
}
