package util

import (
	"testing"
	"time"
)

func TestFormatStorageTime(t *testing.T) {
	// Fixed instant: 2026-05-11T14:00:00Z.
	when := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "UTC input round-trips unchanged",
			in:   when,
			want: "2026-05-11T14:00:00Z",
		},
		{
			name: "non-UTC input is converted to UTC",
			in:   when.In(time.FixedZone("EDT", -4*3600)),
			want: "2026-05-11T14:00:00Z",
		},
		{
			name: "zero time renders as RFC3339 zero",
			in:   time.Time{},
			want: "0001-01-01T00:00:00Z",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatStorageTime(tc.in)
			if got != tc.want {
				t.Errorf("FormatStorageTime(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseStorageTime(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{
			name: "RFC3339 UTC",
			in:   "2026-05-11T14:00:00Z",
			want: time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC),
		},
		{
			name: "RFC3339 with offset is normalised to UTC",
			in:   "2026-05-11T10:00:00-04:00",
			want: time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC),
		},
		{
			name: "RFC3339Nano is accepted",
			in:   "2026-05-11T14:00:00.123456789Z",
			want: time.Date(2026, 5, 11, 14, 0, 0, 123456789, time.UTC),
		},
		{
			name:    "non-RFC3339 string errors",
			in:      "not a time",
			wantErr: true,
		},
		{
			name:    "empty string errors",
			in:      "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseStorageTime(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseStorageTime(%q) = nil err, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStorageTime(%q) returned err: %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseStorageTime(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if got.Location() != time.UTC {
				t.Errorf("ParseStorageTime(%q) returned non-UTC location %v", tc.in, got.Location())
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// A writer in EDT, a reader anywhere: the parsed value must
	// equal the original instant and be in UTC.
	edt := time.FixedZone("EDT", -4*3600)
	original := time.Date(2026, 5, 11, 10, 0, 0, 0, edt)

	encoded := FormatStorageTime(original)
	decoded, err := ParseStorageTime(encoded)
	if err != nil {
		t.Fatalf("round trip parse: %v", err)
	}
	if !decoded.Equal(original) {
		t.Errorf("round trip lost instant: encoded=%q decoded=%v original=%v",
			encoded, decoded, original)
	}
	if decoded.Location() != time.UTC {
		t.Errorf("round trip lost UTC normalisation: location=%v", decoded.Location())
	}
}

func TestSameDayInZone(t *testing.T) {
	ny, err := LoadTimezone("America/New_York")
	if err != nil {
		t.Fatalf("load NY: %v", err)
	}

	// UTC instant: 2026-05-12T02:00:00Z
	// In UTC: May 12.
	// In NY (EDT, -4h): 2026-05-11 22:00 — May 11.
	lateNight := time.Date(2026, 5, 12, 2, 0, 0, 0, time.UTC)
	// UTC instant: 2026-05-11T15:00:00Z
	// In NY: 2026-05-11 11:00 — May 11.
	sameDayUTCDifferentZone := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		a, b time.Time
		loc  *time.Location
		want bool
	}{
		{
			name: "same day in UTC, same day in NY",
			a:    sameDayUTCDifferentZone,
			b:    time.Date(2026, 5, 11, 20, 0, 0, 0, time.UTC),
			loc:  ny,
			want: true,
		},
		{
			name: "different UTC days but same NY day",
			a:    sameDayUTCDifferentZone, // May 11 NY
			b:    lateNight,               // May 11 NY (May 12 UTC)
			loc:  ny,
			want: true,
		},
		{
			name: "same NY day but compared in UTC says different",
			a:    sameDayUTCDifferentZone, // May 11 UTC + NY
			b:    lateNight,               // May 12 UTC, May 11 NY
			loc:  time.UTC,
			want: false,
		},
		{
			name: "nil loc falls back to time.Local",
			a:    sameDayUTCDifferentZone,
			b:    sameDayUTCDifferentZone,
			loc:  nil,
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SameDayInZone(tc.a, tc.b, tc.loc)
			if got != tc.want {
				t.Errorf("SameDayInZone(%v, %v, %v) = %v, want %v",
					tc.a, tc.b, tc.loc, got, tc.want)
			}
		})
	}
}
