package util

import (
	"testing"
	"time"
)

func TestLoadTimezone(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    *time.Location
		wantErr bool
	}{
		{"empty defaults to local", "", time.Local, false},
		{"local literal", "local", time.Local, false},
		{"UTC literal", "UTC", time.UTC, false},
		{"valid IANA", "America/New_York", nil, false},
		{"invalid IANA", "Bogus/Land", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadTimezone(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("LoadTimezone(%q) = nil err, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadTimezone(%q) returned err: %v", tc.in, err)
			}
			if tc.want != nil && got != tc.want {
				t.Errorf("LoadTimezone(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if tc.in == "America/New_York" && got.String() != "America/New_York" {
				t.Errorf("LoadTimezone(%q).String() = %q", tc.in, got.String())
			}
		})
	}
}

func TestFormatInZone(t *testing.T) {
	// Fixed timestamp: 2026-05-11T14:00:00Z
	when := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		loc    *time.Location
		layout string
		want   string
	}{
		{
			name:   "UTC formatted",
			loc:    time.UTC,
			layout: time.RFC3339,
			want:   "2026-05-11T14:00:00Z",
		},
		{
			name:   "nil location falls back to local",
			loc:    nil,
			layout: "2006-01-02",
			want:   when.In(time.Local).Format("2006-01-02"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatInZone(when, tc.loc, tc.layout)
			if got != tc.want {
				t.Errorf("FormatInZone(...) = %q, want %q", got, tc.want)
			}
		})
	}

	// IANA round-trip: render UTC time in NY zone and ensure
	// it's offset correctly (NY in May is EDT = UTC-4).
	ny, err := LoadTimezone("America/New_York")
	if err != nil {
		t.Fatalf("load NY: %v", err)
	}
	got := FormatInZone(when, ny, time.RFC3339)
	if got != "2026-05-11T10:00:00-04:00" {
		t.Errorf("FormatInZone(NY) = %q, want 2026-05-11T10:00:00-04:00", got)
	}
}
