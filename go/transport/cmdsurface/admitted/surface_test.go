package admitted_test

import (
	"testing"

	"hop.top/kit/go/transport/cmdsurface/admitted"
)

func TestAllSurfaces_DeclarationOrderAndValidity(t *testing.T) {
	want := []admitted.Surface{
		"cli", "rest", "ws", "sse", "rpc", "mcp", "webhook", "bus",
		"cron", "lib", "oauth-cb", "signed", "faas", "socket",
	}
	got := admitted.AllSurfaces()
	if len(got) != len(want) {
		t.Fatalf("AllSurfaces has %d surfaces, want %d", len(got), len(want))
	}
	for i, s := range got {
		if s != want[i] {
			t.Errorf("AllSurfaces[%d] = %q, want %q", i, s, want[i])
		}
		if !s.IsValid() {
			t.Errorf("%q is not valid", s)
		}
		if s.String() != string(want[i]) {
			t.Errorf("String() = %q, want %q", s.String(), want[i])
		}
	}
	if admitted.Surface("telnet").IsValid() {
		t.Error("an undeclared surface is valid")
	}
}
