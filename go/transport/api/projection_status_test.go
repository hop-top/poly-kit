package api

import (
	"net/http"
	"testing"

	"hop.top/kit/go/console/output/envelope"
)

// TestEveryKitClassHasAStatus is the drift guard for this package's copy
// of the exit-code taxonomy.
//
// The constants now come from envelope, so their VALUES cannot drift.
// What can still drift is coverage: kit adding a class does not add a
// row to exitStatusTable, and StatusForExitCode answers 500 for anything
// unmapped. A caller would see "server error" for a refusal kit
// classifies precisely — which is exactly the silent-wrong-answer shape
// the harness table had.
//
// Classes this package deliberately does not distinguish are excused
// individually, with the status they collapse to recorded, so the
// collapse is a decision rather than an omission.
func TestEveryKitClassHasAStatus(t *testing.T) {
	// CONSENT_REFUSED and PREREQUISITE are not reachable through this
	// projection today: it executes commands over HTTP, where there is
	// no TTY to confirm at and no operator to start a dependency. They
	// collapse to 500 with the rest of the unclassified failures. If
	// either becomes reachable, delete its line here and add a real row.
	excused := map[string]string{
		envelope.CodeConsentRefused: "no confirmation gate on this surface",
		envelope.CodePrerequisite:   "no operator to repair the environment mid-request",
	}

	for _, class := range envelope.ExitClasses() {
		exit, ok := envelope.ExitCodeForClass(class)
		if !ok {
			t.Fatalf("ExitClasses returned %q, absent from the table", class)
		}
		_, mapped := exitStatusTable[exit]
		if reason, isExcused := excused[class]; isExcused {
			if mapped {
				t.Errorf("%s (exit %d) is excused as %q but IS mapped; "+
					"remove the excuse or the row", class, exit, reason)
			}
			continue
		}
		if !mapped {
			t.Errorf("kit class %s (exit %d) has no row in exitStatusTable, so "+
				"StatusForExitCode answers 500 for it.\n"+
				"Add the row, or excuse it in this test with the reason it "+
				"collapses to 500.", class, exit)
		}
	}
}

// TestExitStatusTableEnumeratesEveryRow pins ExitStatusTable against the
// map it renders. The function hard-codes its ordering list, so a row
// added to the map alone would never appear in the rendered legend or
// the README generated from it.
func TestExitStatusTableEnumeratesEveryRow(t *testing.T) {
	rendered := map[int]bool{}
	for _, pair := range ExitStatusTable() {
		if rendered[pair.ExitCode] {
			t.Errorf("exit %d appears twice in ExitStatusTable", pair.ExitCode)
		}
		rendered[pair.ExitCode] = true
		if want := exitStatusTable[pair.ExitCode]; pair.Status != want {
			t.Errorf("ExitStatusTable renders exit %d as %d, table says %d",
				pair.ExitCode, pair.Status, want)
		}
	}
	for exit := range exitStatusTable {
		if !rendered[exit] {
			t.Errorf("exit %d is in exitStatusTable but ExitStatusTable does not "+
				"render it; add it to the ordering list", exit)
		}
	}
	for i := 1; i < len(ExitStatusTable()); i++ {
		rows := ExitStatusTable()
		if rows[i-1].ExitCode >= rows[i].ExitCode {
			t.Errorf("ExitStatusTable is not in ascending exit-code order at %d", i)
		}
	}
}

// TestStatusForUnmappedCodeIs500 pins the documented fallback.
func TestStatusForUnmappedCodeIs500(t *testing.T) {
	if got := StatusForExitCode(199); got != http.StatusInternalServerError {
		t.Errorf("StatusForExitCode(199) = %d, want 500", got)
	}
}
