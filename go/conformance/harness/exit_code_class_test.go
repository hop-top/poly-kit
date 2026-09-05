package harness

import "testing"

// The class map mirrors the kit/output exit-code table
// (go/console/output/error.go). These pins catch drift between the two.
func TestClassToExitCode(t *testing.T) {
	tests := []struct {
		class string
		want  int
	}{
		{"OK", 0},
		{"GENERIC", 1},
		{"USAGE", 2},
		{"NOT_FOUND", 3},
		{"CONFLICT", 4},
		{"UNAUTHORIZED", 5},
		{"TRANSIENT", 6},
		{"RATE_LIMITED", 64},
		{"PROVENANCE_MISSING", 65},
		{"transient", 6},     // case-insensitive
		{" conflict ", 4},    // trimmed
		{"NO_SUCH_CLASS", 1}, // unknown defaults to GENERIC
	}
	for _, tc := range tests {
		t.Run(tc.class, func(t *testing.T) {
			if got := ClassToExitCode(tc.class); got != tc.want {
				t.Errorf("ClassToExitCode(%q) = %d, want %d", tc.class, got, tc.want)
			}
		})
	}
}

func TestExitCodeToClass(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{6, "TRANSIENT"},
		{65, "PROVENANCE_MISSING"},
		{99, "UNKNOWN"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := exitCodeToClass(tc.code); got != tc.want {
				t.Errorf("exitCodeToClass(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// Every class must own a distinct numeric code; a collision would make
// exitCodeToClass ambiguous and failure messages misleading.
func TestExitClassCodesAreUnique(t *testing.T) {
	seen := make(map[int]string, len(exitClassToCode))
	for class, code := range exitClassToCode {
		if prev, dup := seen[code]; dup {
			t.Errorf("exit code %d claimed by both %s and %s", code, prev, class)
		}
		seen[code] = class
	}
}
