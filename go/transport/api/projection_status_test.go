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
// collapse is a decision rather than an omission. Nothing is excused
// today: CONSENT_REFUSED and PREREQUISITE were, on the reading that
// neither reaches an HTTP surface, and both readings were wrong.
// TestRootFactoryKeepsTheGatesOverREST in go/console/cli calls an
// unconfirmed gated command over real HTTP and gets the confirmation
// gate's refusal back in the body, so there IS a confirmation gate
// here — a served request has no terminal, which is precisely the
// non-TTY default the gate refuses on. PREREQUISITE is exported for
// adopters rather than raised by kit, so its reachability is the
// adopter's to create and not kit's to rule out.
func TestEveryKitClassHasAStatus(t *testing.T) {
	excused := map[string]string{}

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

// TestConsentAndPrerequisiteAnswerTheirNextAction pins the two rows
// that used to fall through to 500, each against the action the status
// is chosen to prompt.
//
// Both are worth pinning by name rather than leaving to the coverage
// guard above, which only asks whether a row exists. A row answering
// 500 would satisfy that guard and still tell an agent the server
// broke, which is the failure this pair is here to prevent.
func TestConsentAndPrerequisiteAnswerTheirNextAction(t *testing.T) {
	// A consent refusal is cleared by re-sending the same call WITH
	// confirmation. 403 says the server understood and declined; it
	// does not invite a bare retry, which would refuse identically.
	if got := StatusForExitCode(envelope.ExitConsentRefused); got != http.StatusForbidden {
		t.Errorf("CONSENT_REFUSED (exit %d) = %d, want 403",
			envelope.ExitConsentRefused, got)
	}
	// A prerequisite failure is cleared by an operator repairing the
	// environment; the identical call then succeeds. 503 is the one
	// status that says retry this call later rather than change it.
	if got := StatusForExitCode(envelope.ExitPrerequisite); got != http.StatusServiceUnavailable {
		t.Errorf("PREREQUISITE (exit %d) = %d, want 503",
			envelope.ExitPrerequisite, got)
	}
	// Neither may quietly regress to the unclassified-failure answer.
	for _, exit := range []int{envelope.ExitConsentRefused, envelope.ExitPrerequisite} {
		if StatusForExitCode(exit) == http.StatusInternalServerError {
			t.Errorf("exit %d answers 500; a classified refusal must not "+
				"report itself as a server error", exit)
		}
	}
}
