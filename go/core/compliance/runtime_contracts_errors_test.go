package compliance

// Tests for the Factor-4 runtime check. The check has two separate
// obligations — non-zero exit, and an envelope that carries recovery
// guidance — and the statuses they produce differ, so each is asserted
// against its own stub binary.

import (
	"encoding/json"
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

func TestRtContractsErrors_HelpPointerIsNotGuidance(t *testing.T) {
	// The round trip this factor exists to eliminate, dressed as a fix.
	// A tool whose only answer is "go read --help" has offered nothing,
	// so it must not pass "with recovery guidance".
	for _, fix := range []string{
		`run 'tool sub --help' for usage`,
		`tool --help`,
		`see --help`,
		`--help`,
		`Try tool list --help.`,
	} {
		t.Run(fix, func(t *testing.T) {
			bin := stubBinary(t, `{"code":"USAGE","message":"unknown flag","suggested_fix":`+
				quoteJSON(fix)+`}`, 2)
			got := rtContractsErrors(bin)
			if got.Status != "warn" {
				t.Errorf("status = %q, want warn for a bare help pointer\n%+v", got.Status, got)
			}
		})
	}
}

func TestRtContractsErrors_HelpPointerInAlternativesIsNotGuidance(t *testing.T) {
	bin := stubBinary(t,
		`{"code":"USAGE","message":"unknown flag","suggested_fix":"run 'tool --help' for usage",`+
			`"alternatives":["tool --help","see help"]}`, 2)
	if got := rtContractsErrors(bin); got.Status != "warn" {
		t.Errorf("status = %q, want warn\n%+v", got.Status, got)
	}
}

// quoteJSON renders s as a JSON string literal for embedding in a stub body.
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestRtContractsErrors_KitHelpPointerFallbackWarns: kit's own no-match
// fallback IS a help pointer (a bogus flag at the root matches nothing), so
// the tier this factor added must not pass kit for offering exactly the
// round trip it exists to eliminate. Asserted against the rendered envelope
// rather than a live binary so the check stays a unit test.
func TestRtContractsErrors_KitHelpPointerFallbackWarns(t *testing.T) {
	// Verbatim from `flagerrtool --format json --bogus-arg-xyzzy`.
	body := `{
  "code": "USAGE",
  "message": "unknown flag --bogus-arg-xyzzy",
  "cause": "no flag named --bogus-arg-xyzzy on flagerrtool",
  "suggested_fix": "run 'flagerrtool --help' for usage",
  "exit_code": 2,
  "transience": "permanent"
}`
	got := rtContractsErrors(stubBinary(t, body, 2))
	if got.Status != "warn" {
		t.Errorf("status = %q, want warn: kit's help-pointer fallback is not recovery guidance\n%+v",
			got.Status, got)
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
		// A bare help pointer is the round trip, not the fix.
		{"help pointer sentence", map[string]any{"suggested_fix": "run 'tool sub --help' for usage"}, false},
		{"help pointer bare", map[string]any{"suggested_fix": "--help"}, false},
		{"help pointer shorthand", map[string]any{"suggested_fix": "-h"}, false},
		{"help pointer with path", map[string]any{"suggested_fix": "tool widget list --help"}, false},
		{"help pointer in alternatives", map[string]any{"alternatives": []any{"tool --help"}}, false},
		// A fix that names --help alongside something concrete still
		// counts: the concrete half is the guidance.
		{"concrete plus help", map[string]any{"suggested_fix": "use --format json (see --help)"}, true},
		{"flag with value", map[string]any{"suggested_fix": "--status TODO"}, true},
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
