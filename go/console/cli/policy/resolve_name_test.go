package policy

import (
	"strings"
	"testing"
)

func TestResolveRefusesNamesThatArePaths(t *testing.T) {
	for _, bad := range []string{"../x", "../etc/passwd", "a/b", "/abs", `a\b`, "..", ".", "a.b", strings.Repeat("x", 65), "a b"} {
		if _, err := Resolve("t", bad); err == nil || !strings.Contains(err.Error(), "invalid policy name") {
			t.Errorf("Resolve(%q) = %v, want invalid policy name", bad, err)
		}
		if _, err := LoadNamed("t", bad); err == nil || !strings.Contains(err.Error(), "invalid policy name") {
			t.Errorf("LoadNamed(%q) = %v", bad, err)
		}
	}
	if _, err := Resolve("t", "ok_Name-1"); err != nil {
		t.Errorf("valid name refused: %v", err)
	}
}
