package envelope_test

// Generator and drift gate for contracts/exit-taxonomy-v1/taxonomy.json,
// the cross-language pin for kit's exit-code taxonomy.
//
// The file is GENERATED from this package, not hand-written. That choice
// is the point of the contract. A hand-written JSON table is a second
// copy of the taxonomy, and a second copy is exactly the mechanism that
// let CONSENT_REFUSED 7 and PREREQUISITE 70 ship in Go and reach none of
// the four SDK ports for two releases. Pinning the file against
// ExitClasses/ExitCodeForClass/TransienceForCode instead means a
// Go-side addition makes this test red in the same commit, and the fix
// is to regenerate — which then makes the four port loaders red until
// they carry the new class too.
//
// Regenerate with:
//
//	go test ./go/console/output/envelope/ -run TestGenerateExitTaxonomyContract \
//	    -update-taxonomy-contract
//
// Regenerating is a deliberate act: it re-baselines every port against
// new Go behavior, so a diff in the file must be explainable as an
// intended Go-side change.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/console/output/envelope"
)

var updateTaxonomyContract = flag.Bool("update-taxonomy-contract", false,
	"rewrite contracts/exit-taxonomy-v1/taxonomy.json from the live Go taxonomy")

// taxonomyClass is one standard class row: the symbol every port must
// export, the numeric exit code it carries, and the default retry answer
// TransienceForCode gives for it.
type taxonomyClass struct {
	Class      string `json:"class"`
	Exit       int    `json:"exit"`
	Transience string `json:"transience"`
}

// taxonomyBandSlot is one allocated slot in kit's >6 extension band.
// Owner is carried because three different packages declare these slots;
// a port reading the contract needs to know which rows are envelope's to
// export and which are recorded only so a future slot cannot collide.
type taxonomyBandSlot struct {
	Exit  int    `json:"exit"`
	Class string `json:"class"`
	Owner string `json:"owner"`
}

// taxonomyDoc is the emitted file. Field order is the emitted JSON key
// order (encoding/json follows struct order).
type taxonomyDoc struct {
	Schema        string             `json:"$schema"`
	ID            string             `json:"$id"`
	Comment       []string           `json:"_comment"`
	Version       string             `json:"version"`
	Source        string             `json:"source"`
	Transiences   []string           `json:"transiences"`
	Classes       []taxonomyClass    `json:"classes"`
	ExtensionBand []taxonomyBandSlot `json:"extension_band"`
}

func taxonomyComment() []string {
	return []string{
		"Cross-language exit-code taxonomy. Generated from",
		"go/console/output/envelope — do not hand-edit. Regenerate with:",
		"  go test ./go/console/output/envelope/ \\",
		"      -run TestGenerateExitTaxonomyContract -update-taxonomy-contract",
		"",
		"`classes` is the complete set of standard classes kit defines,",
		"in ascending exit order. Every port MUST export each `class`",
		"symbol, MUST resolve it to `exit`, and MUST return `transience`",
		"from its transience-for-code function. A port missing a row, or",
		"answering a different number or transience, is a drift bug: the",
		"exit code and the transience are what adopters and agents branch",
		"on, so a port that disagrees gives wrong retry guidance rather",
		"than merely lacking a constant.",
		"",
		"`extension_band` records every allocated slot above 6, including",
		"the four owned by the conformance trees rather than by envelope.",
		"Ports are NOT required to export the slots they do not own — see",
		"`owner`. The rows are pinned here so a new slot cannot silently",
		"double-book a number that is already spent.",
		"",
		"OK and GENERIC carry no transience by design: OK is not a",
		"failure, and GENERIC is uncharacterized by construction, so",
		"\"unknown\" is the honest answer rather than a gap. Both appear",
		"with transience \"unknown\", which is what TransienceForCode",
		"returns for them.",
	}
}

// contractPath resolves contracts/exit-taxonomy-v1/taxonomy.json from
// this package's directory. go/console/output/envelope -> repo root is
// four levels up.
func contractPath() string {
	return filepath.Join("..", "..", "..", "..", "contracts",
		"exit-taxonomy-v1", "taxonomy.json")
}

// buildTaxonomyDoc reads the live Go taxonomy. Nothing here is a
// literal: every class, number and transience comes out of the package
// under test, which is what makes the emitted file a projection of Go
// rather than a copy of it.
func buildTaxonomyDoc() taxonomyDoc {
	classes := make([]taxonomyClass, 0, len(envelope.ExitClasses()))
	for _, class := range envelope.ExitClasses() {
		exit, ok := envelope.ExitCodeForClass(class)
		if !ok {
			// Unreachable: ExitClasses enumerates the same map
			// ExitCodeForClass reads.
			panic("envelope: ExitClasses returned a class ExitCodeForClass does not know: " + class)
		}
		classes = append(classes, taxonomyClass{
			Class:      class,
			Exit:       exit,
			Transience: envelope.TransienceForCode(class),
		})
	}

	band := make([]taxonomyBandSlot, 0, len(envelope.ExtensionBand()))
	for _, slot := range envelope.ExtensionBand() {
		band = append(band, taxonomyBandSlot{
			Exit:  slot.Exit,
			Class: slot.Class,
			Owner: slot.Owner,
		})
	}

	return taxonomyDoc{
		Schema:  "https://json-schema.org/draft/2020-12/schema",
		ID:      "https://hop.top/kit/contracts/exit-taxonomy-v1/taxonomy.json",
		Comment: taxonomyComment(),
		Version: "v1",
		Source:  "hop.top/kit/go/console/output/envelope",
		Transiences: []string{
			envelope.TransienceTransient,
			envelope.TransiencePermanent,
			envelope.TransienceUnknown,
		},
		Classes:       classes,
		ExtensionBand: band,
	}
}

func encodeTaxonomyDoc(t *testing.T, doc taxonomyDoc) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal taxonomy contract: %v", err)
	}
	return append(encoded, '\n')
}

func TestGenerateExitTaxonomyContract(t *testing.T) {
	doc := buildTaxonomyDoc()
	if len(doc.Classes) == 0 {
		t.Fatal("no classes generated; ExitClasses returned nothing")
	}
	encoded := encodeTaxonomyDoc(t, doc)

	path := contractPath()
	if *updateTaxonomyContract {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d classes and %d band slots to %s",
			len(doc.Classes), len(doc.ExtensionBand), path)
		return
	}

	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\nRun with -update-taxonomy-contract to generate it.", path, err)
	}
	if string(committed) != string(encoded) {
		t.Errorf("committed taxonomy contract is stale: the Go taxonomy no "+
			"longer produces these bytes.\nRegenerate with "+
			"-update-taxonomy-contract, and expect the four SDK port loaders "+
			"to go red until they carry the change too.\nfile: %s", path)
	}
}

// TestExitTaxonomyContractIsSelfConsistent guards the shape of the
// emitted file rather than its content, so a generator change that
// silently drops a field or double-books a number fails here.
func TestExitTaxonomyContractIsSelfConsistent(t *testing.T) {
	doc := buildTaxonomyDoc()

	seenClass := map[string]bool{}
	seenExit := map[int]string{}
	for _, row := range doc.Classes {
		if row.Class == "" {
			t.Errorf("class row with empty symbol: %+v", row)
		}
		if row.Transience == "" {
			t.Errorf("class %q has empty transience", row.Class)
		}
		if seenClass[row.Class] {
			t.Errorf("class %q appears twice", row.Class)
		}
		seenClass[row.Class] = true
		if prior, dup := seenExit[row.Exit]; dup {
			t.Errorf("exit %d claimed by both %q and %q", row.Exit, prior, row.Class)
		}
		seenExit[row.Exit] = row.Class
	}

	// Every band slot must be either a standard class at the same
	// number, or a slot owned elsewhere. A band row naming a number the
	// standard table gives to a different class is a double-booking.
	for _, slot := range doc.ExtensionBand {
		if slot.Exit <= 6 {
			t.Errorf("band slot %q at exit %d is inside the reserved 0-6 range",
				slot.Class, slot.Exit)
		}
		if owner, ok := seenExit[slot.Exit]; ok && owner != slot.Class {
			t.Errorf("band slot %q at exit %d collides with standard class %q",
				slot.Class, slot.Exit, owner)
		}
	}
}
