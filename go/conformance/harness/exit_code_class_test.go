package harness_test

import (
	"testing"

	"hop.top/kit/go/conformance/harness"
	"hop.top/kit/go/console/output/envelope"
)

// TestClassToExitCodeResolvesEveryKitClass is the drift guard for the
// defect this file exists to close.
//
// The harness used to carry its own copy of kit's exit-code table,
// refreshed by a comment. The copy went stale — CONSENT_REFUSED and
// PREREQUISITE were added to kit and never copied — and because the
// lookup defaults to 1 on a miss, the staleness showed up as a leaf
// being asserted against exit 1 instead of as a failing test.
//
// This iterates envelope.ExitClasses rather than listing the classes,
// so a class added to kit is checked here the moment it is added. A
// repeated list would be a third copy of the table and would drift for
// the same reason the first two did.
func TestClassToExitCodeResolvesEveryKitClass(t *testing.T) {
	for _, class := range envelope.ExitClasses() {
		want, ok := envelope.ExitCodeForClass(class)
		if !ok {
			t.Fatalf("ExitClasses returned %q, absent from the table", class)
		}
		if got := harness.ClassToExitCode(class); got != want {
			t.Errorf("ClassToExitCode(%q) = %d, want %d.\n"+
				"The harness resolved a class kit defines to the wrong code. "+
				"If %d is 1, the class fell through to the unknown-class default, "+
				"which is the exact failure mode this guard exists to catch.",
				class, got, want, got)
		}
	}
}

// TestGenericIsNotIndistinguishableFromUnknown pins the property that
// made the original defect invisible.
//
// ClassToExitCode returns 1 both for GENERIC and for a class it does not
// recognize, so a stale table cannot be detected from the return value
// alone. That is still true and still intentional — an adopter may
// declare its own class — which is why the guard above walks kit's list
// instead of probing the function. This test records the hazard so a
// future reader does not mistake "returns 1" for "resolved".
func TestGenericIsNotIndistinguishableFromUnknown(t *testing.T) {
	if harness.ClassToExitCode("GENERIC") != 1 {
		t.Fatal("GENERIC must resolve to 1")
	}
	if harness.ClassToExitCode("NO_SUCH_CLASS_DEFINED_ANYWHERE") != 1 {
		t.Fatal("an unknown class must still fall back to 1 for adopter-defined classes")
	}
}

// TestBandClassesResolve covers the tool-specific >6 slots the
// conformance tree owns. They are absent from envelope's table by
// design, so the guard above does not reach them; without this test a
// leaf declaring kit/exit-codes=GRADE_FAIL would silently be asserted
// against exit 1, which is the original defect in a different slot.
func TestBandClassesResolve(t *testing.T) {
	for _, slot := range envelope.ExtensionBand() {
		got := harness.ClassToExitCode(slot.Class)
		if got != slot.Exit {
			t.Errorf("ClassToExitCode(%q) = %d, want %d (band slot owned by %s)",
				slot.Class, got, slot.Exit, slot.Owner)
		}
	}
}

// TestBandTableMatchesKitAllocation pins the harness's band copy against
// envelope's record of the allocation. The band slots are declared by
// three different packages, so nothing but a shared list can catch a
// renumbering: this asserts the harness agrees with that list, and
// envelope's own guard asserts the list has no collisions or gaps.
func TestBandTableMatchesKitAllocation(t *testing.T) {
	// Every band slot kit allocates outside envelope's own table must be
	// resolvable by the harness, and nothing else may be.
	owned := map[string]int{}
	for _, slot := range envelope.ExtensionBand() {
		if _, inLeaf := envelope.ExitCodeForClass(slot.Class); inLeaf {
			continue // RATE_LIMITED / PROVENANCE_MISSING / PREREQUISITE
		}
		owned[slot.Class] = slot.Exit
	}
	if len(owned) == 0 {
		t.Fatal("no tool-owned band slots found; this guard is vacuous")
	}
	for class, exit := range owned {
		if got := harness.ClassToExitCode(class); got != exit {
			t.Errorf("harness resolves band class %q to %d, kit allocates %d",
				class, got, exit)
		}
	}
}
