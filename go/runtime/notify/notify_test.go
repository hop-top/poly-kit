package notify

import (
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// typedPayload implements WithSeverity for the in-process path.
type typedPayload struct{ sev Severity }

func (t typedPayload) Severity() Severity { return t.sev }

// untypedPayload does not implement WithSeverity.
type untypedPayload struct{ Note string }

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		in   Severity
		want string
	}{
		{SeverityDebug, "debug"},
		{SeverityInfo, "info"},
		{SeverityWarn, "warn"},
		{SeverityError, "error"},
		{SeverityCritical, "critical"},
		{Severity(99), "unknown"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			got := c.in.String()
			if got != c.want {
				t.Fatalf("Severity(%d).String() = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSeverityOf_TypedPayloadWithInterface(t *testing.T) {
	t.Parallel()
	for _, sev := range []Severity{
		SeverityDebug, SeverityInfo, SeverityWarn,
		SeverityError, SeverityCritical,
	} {
		e := bus.Event{Payload: typedPayload{sev: sev}}
		got := SeverityOf(e)
		if got != sev {
			t.Fatalf("SeverityOf(typed=%s) = %s, want %s", sev, got, sev)
		}
	}
}

func TestSeverityOf_TypedPayloadWithoutInterface(t *testing.T) {
	t.Parallel()
	e := bus.Event{Payload: untypedPayload{Note: "hi"}}
	if got := SeverityOf(e); got != SeverityInfo {
		t.Fatalf("SeverityOf(untyped) = %s, want info", got)
	}
}

func TestSeverityOf_MapStringKeyword(t *testing.T) {
	t.Parallel()
	cases := map[string]Severity{
		"debug":    SeverityDebug,
		"info":     SeverityInfo,
		"warn":     SeverityWarn,
		"error":    SeverityError,
		"critical": SeverityCritical,
	}
	for kw, want := range cases {
		kw, want := kw, want
		t.Run(kw, func(t *testing.T) {
			t.Parallel()
			e := bus.Event{Payload: map[string]any{"severity": kw}}
			if got := SeverityOf(e); got != want {
				t.Fatalf("SeverityOf(map[severity=%q]) = %s, want %s", kw, got, want)
			}
		})
	}
}

func TestSeverityOf_MapIntSeverity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want Severity
	}{
		{0, SeverityDebug},
		{1, SeverityInfo},
		{2, SeverityWarn},
		{3, SeverityError},
		{4, SeverityCritical},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want.String(), func(t *testing.T) {
			t.Parallel()
			e := bus.Event{Payload: map[string]any{"severity": c.in}}
			if got := SeverityOf(e); got != c.want {
				t.Fatalf("SeverityOf(map[severity=%d]) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

func TestSeverityOf_MapInt64Severity(t *testing.T) {
	t.Parallel()
	e := bus.Event{Payload: map[string]any{"severity": int64(3)}}
	if got := SeverityOf(e); got != SeverityError {
		t.Fatalf("SeverityOf(map[severity=int64(3)]) = %s, want error", got)
	}
}

func TestSeverityOf_MapFloat64Severity(t *testing.T) {
	t.Parallel()
	// post-JSON-decode shape: numbers come through as float64.
	cases := []struct {
		in   float64
		want Severity
	}{
		{0, SeverityDebug},
		{1, SeverityInfo},
		{2, SeverityWarn},
		{3, SeverityError},
		{4, SeverityCritical},
	}
	for _, c := range cases {
		c := c
		t.Run(c.want.String(), func(t *testing.T) {
			t.Parallel()
			e := bus.Event{Payload: map[string]any{"severity": c.in}}
			if got := SeverityOf(e); got != c.want {
				t.Fatalf("SeverityOf(map[severity=%v]) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

func TestSeverityOf_MapInvalidKeyword(t *testing.T) {
	t.Parallel()
	cases := []string{
		"unknown", "INFO", "Warn", "panic", "", "FATAL",
	}
	for _, kw := range cases {
		kw := kw
		t.Run(kw, func(t *testing.T) {
			t.Parallel()
			e := bus.Event{Payload: map[string]any{"severity": kw}}
			if got := SeverityOf(e); got != SeverityInfo {
				t.Fatalf("SeverityOf(map[severity=%q]) = %s, want info", kw, got)
			}
		})
	}
}

func TestSeverityOf_MapOutOfRangeInt(t *testing.T) {
	t.Parallel()
	for _, n := range []int{-1, 5, 99, -100} {
		e := bus.Event{Payload: map[string]any{"severity": n}}
		if got := SeverityOf(e); got != SeverityInfo {
			t.Fatalf("SeverityOf(map[severity=%d]) = %s, want info", n, got)
		}
	}
}

func TestSeverityOf_MapNonIntegerFloat(t *testing.T) {
	t.Parallel()
	// 2.5 isn't a valid Severity even though it's "in range".
	e := bus.Event{Payload: map[string]any{"severity": 2.5}}
	if got := SeverityOf(e); got != SeverityInfo {
		t.Fatalf("SeverityOf(map[severity=2.5]) = %s, want info", got)
	}
}

func TestSeverityOf_MapNoSeverityKey(t *testing.T) {
	t.Parallel()
	e := bus.Event{Payload: map[string]any{"other": "value"}}
	if got := SeverityOf(e); got != SeverityInfo {
		t.Fatalf("SeverityOf(map without severity) = %s, want info", got)
	}
}

func TestSeverityOf_MapWrongTypeForSeverity(t *testing.T) {
	t.Parallel()
	// e.g. a slice or map in the severity field — not a recognized shape.
	e := bus.Event{Payload: map[string]any{"severity": []string{"warn"}}}
	if got := SeverityOf(e); got != SeverityInfo {
		t.Fatalf("SeverityOf(map[severity=slice]) = %s, want info", got)
	}
}

func TestSeverityOf_NilPayload(t *testing.T) {
	t.Parallel()
	e := bus.Event{Payload: nil}
	if got := SeverityOf(e); got != SeverityInfo {
		t.Fatalf("SeverityOf(nil) = %s, want info", got)
	}
}
