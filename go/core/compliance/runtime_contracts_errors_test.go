package compliance

// Tests for the Factor-4 runtime check. The check has two separate
// obligations — non-zero exit, and an envelope that carries recovery
// guidance — and the statuses they produce differ, so each is asserted
// against its own stub binary.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// stubBinary writes an executable shell script that prints body to stderr
// and exits with code. Returns its path.
func stubBinary(t *testing.T, stderrBody string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "stub")
	script := "#!/bin/sh\ncat >&2 <<'STUB_EOF'\n" + stderrBody + "\nSTUB_EOF\nexit " +
		itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestRtContractsErrors_ZeroExitFails(t *testing.T) {
	// The worst outcome: a rejected flag the caller cannot detect.
	bin := stubBinary(t, `{"code":"USAGE","suggested_fix":"--counters"}`, 0)
	got := rtContractsErrors(bin)
	if got.Status != "fail" {
		t.Errorf("status = %q, want fail (exit 0 on a bogus flag)\n%+v", got.Status, got)
	}
}

func TestRtContractsErrors_StructuredWithFixPasses(t *testing.T) {
	bin := stubBinary(t,
		`{"code":"USAGE","message":"unknown flag --bogus-arg-xyzzy","suggested_fix":"--counters","exit_code":2}`, 2)
	got := rtContractsErrors(bin)
	if got.Status != "pass" {
		t.Errorf("status = %q, want pass\n%+v", got.Status, got)
	}
}

func TestRtContractsErrors_AlternativesAlsoCountAsGuidance(t *testing.T) {
	// A tool that declines to guess between candidates is behaving
	// correctly, not incompletely — the shortlist is the guidance.
	bin := stubBinary(t,
		`{"code":"USAGE","message":"unknown flag","alternatives":["--counters","--count-only"]}`, 2)
	got := rtContractsErrors(bin)
	if got.Status != "pass" {
		t.Errorf("status = %q, want pass\n%+v", got.Status, got)
	}
}

func TestRtContractsErrors_StructuredWithoutFixWarns(t *testing.T) {
	// This is the dead end the factor exists to eliminate: the caller
	// learns only that it failed, and must spend a --help round trip.
	bin := stubBinary(t, `{"code":"USAGE","message":"unknown flag: --bogus-arg-xyzzy"}`, 2)
	got := rtContractsErrors(bin)
	if got.Status != "warn" {
		t.Fatalf("status = %q, want warn\n%+v", got.Status, got)
	}
	if got.Suggestion == "" {
		t.Error("warn carries no suggestion telling the adopter what to add")
	}
}

func TestRtContractsErrors_BlankFixIsNotGuidance(t *testing.T) {
	// A present-but-empty field is the same dead end as an absent one.
	bin := stubBinary(t,
		`{"code":"USAGE","suggested_fix":"   ","alternatives":["",""]}`, 2)
	got := rtContractsErrors(bin)
	if got.Status != "warn" {
		t.Errorf("status = %q, want warn\n%+v", got.Status, got)
	}
}

func TestRtContractsErrors_UnstructuredWarns(t *testing.T) {
	bin := stubBinary(t, "unknown flag: --bogus-arg-xyzzy", 2)
	got := rtContractsErrors(bin)
	if got.Status != "warn" {
		t.Errorf("status = %q, want warn\n%+v", got.Status, got)
	}
}

func TestErrorCarriesFix(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want bool
	}{
		{"suggested_fix", map[string]any{"suggested_fix": "--counters"}, true},
		{"alternatives", map[string]any{"alternatives": []any{"--a", "--b"}}, true},
		{"one blank one real alternative", map[string]any{"alternatives": []any{"", "--b"}}, true},
		{"empty object", map[string]any{}, false},
		{"blank fix", map[string]any{"suggested_fix": "  "}, false},
		{"empty alternatives", map[string]any{"alternatives": []any{}}, false},
		{"blank alternatives", map[string]any{"alternatives": []any{"", " "}}, false},
		{"wrong type fix", map[string]any{"suggested_fix": 42}, false},
		{"wrong type alternatives", map[string]any{"alternatives": "--a"}, false},
		{"non-string alternative", map[string]any{"alternatives": []any{1, 2}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := errorCarriesFix(c.obj); got != c.want {
				t.Errorf("errorCarriesFix(%v) = %v, want %v", c.obj, got, c.want)
			}
		})
	}
}
