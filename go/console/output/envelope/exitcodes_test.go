package envelope_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hop.top/kit/go/console/output/envelope"
)

// TestEveryStandardCodeConstantIsInTheTable is the drift guard.
//
// It parses this package's own source for the Code* constant block and
// asserts every symbol declared there is either present in
// ExitCodeForClass or explicitly excused. The previous arrangement — a
// hand-maintained map in a consumer, refreshed by a comment asking
// someone to remember — failed exactly this way twice: CONSENT_REFUSED
// and PREREQUISITE were added to the constants and never reached the
// map, and nothing went red.
//
// Reading the AST rather than listing the symbols here is deliberate. A
// test that repeats the list is a third copy of the table, and a third
// copy drifts for the same reason the first two did. Adding a Code*
// constant and not the table row fails this test without anyone
// remembering to update it.
func TestEveryStandardCodeConstantIsInTheTable(t *testing.T) {
	// Scenario-grader codes deliberately reuse the numeric slots of the
	// standard classes (1/2/4/5) rather than allocating their own, so
	// they are refinements within a class, not classes. Mapping them to
	// a number would claim a reversibility they do not have.
	//
	// A new entry here is a deliberate act with a reason attached. That
	// is the property the old comment-based contract lacked.
	excused := map[string]string{
		"CodeScenarioParseError":        "grader refinement of USAGE/2",
		"CodeScenarioValidateError":     "grader refinement of USAGE/2",
		"CodeScenarioSchemaUnsupported": "grader refinement of GENERIC/1",
		"CodeGraderTooOld":              "grader refinement of GENERIC/1",
		"CodeStoryHashMismatch":         "grader refinement of CONFLICT/4",
		"CodeJudgeUnavailable":          "grader refinement of UNAUTHORIZED/5",
		"CodeJudgePromptUnresolved":     "grader refinement of UNAUTHORIZED/5",
		"CodeJudgeModelRejected":        "grader refinement of UNAUTHORIZED/5",
		"CodeJudgeParseFailed":          "grader refinement of UNAUTHORIZED/5",
		"CodeGraderInternal":            "grader refinement of GENERIC/1",
	}

	for name, value := range parseConstBlock(t, "Code") {
		if reason, ok := excused[name]; ok {
			if _, inTable := envelope.ExitCodeForClass(value); inTable {
				t.Errorf("%s (%q) is excused from the exit-code table as %q "+
					"but IS in it; remove the excuse or the row", name, value, reason)
			}
			continue
		}
		if _, ok := envelope.ExitCodeForClass(value); !ok {
			t.Errorf("%s (%q) is declared as a standard code but has no row in "+
				"ExitCodeForClass.\n"+
				"Add the row in exitcodes.go, or excuse it in this test with a "+
				"reason if it refines an existing class rather than naming a new one.",
				name, value)
		}
	}
}

// TestEveryTableRowHasAConstant is the guard in the other direction: a
// row whose class symbol no longer matches any Code* constant is a
// typo or a leftover, and either way the table now claims a class kit
// does not define.
func TestEveryTableRowHasAConstant(t *testing.T) {
	declared := map[string]bool{}
	for _, value := range parseConstBlock(t, "Code") {
		declared[value] = true
	}
	for _, class := range envelope.ExitClasses() {
		if !declared[class] {
			t.Errorf("ExitCodeForClass has a row for %q but no Code* constant "+
				"declares it", class)
		}
	}
}

// TestExitConstantsAgreeWithTheTable pins every exported Exit* constant
// against the table row for its class. The two must not be able to
// disagree: the constants are what call sites use and the table is what
// generic consumers read, so a divergence means the same class has two
// different numbers depending on which door you came in.
func TestExitConstantsAgreeWithTheTable(t *testing.T) {
	pairs := []struct {
		class string
		exit  int
		name  string
	}{
		{envelope.CodeOK, envelope.ExitOK, "ExitOK"},
		{envelope.CodeGeneric, envelope.ExitGeneric, "ExitGeneric"},
		{envelope.CodeUsage, envelope.ExitUsage, "ExitUsage"},
		{envelope.CodeNotFound, envelope.ExitNotFound, "ExitNotFound"},
		{envelope.CodeConflict, envelope.ExitConflict, "ExitConflict"},
		{envelope.CodeUnauthorized, envelope.ExitUnauthorized, "ExitUnauthorized"},
		{envelope.CodeTransient, envelope.ExitTransient, "ExitTransient"},
		{envelope.CodeConsentRefused, envelope.ExitConsentRefused, "ExitConsentRefused"},
		{envelope.CodeRateLimited, envelope.ExitRateLimited, "ExitRateLimited"},
		{envelope.CodeProvenanceMissing, envelope.ExitProvenanceMissing, "ExitProvenanceMissing"},
		{envelope.CodePrerequisite, envelope.ExitPrerequisite, "ExitPrerequisite"},
	}

	// The pair list is itself a copy, so assert it is a complete one:
	// an Exit* constant added without a row here would otherwise go
	// unchecked.
	seen := map[string]bool{}
	for _, p := range pairs {
		seen[p.name] = true
	}
	for name := range parseConstBlock(t, "Exit") {
		if !seen[name] {
			t.Errorf("%s is an exported exit-code constant with no row in this "+
				"test; add it so the constant and the table stay pinned together", name)
		}
	}

	for _, p := range pairs {
		got, ok := envelope.ExitCodeForClass(p.class)
		if !ok {
			t.Errorf("%s names class %q, which has no table row", p.name, p.class)
			continue
		}
		if got != p.exit {
			t.Errorf("%s = %d but ExitCodeForClass(%q) = %d; the constant and the "+
				"table disagree about the same class", p.name, p.exit, p.class, got)
		}
	}
}

// TestConstructorsAgreeWithTheTable closes the last gap: the
// constructors stamp ExitCode with literals in several places
// (NotFoundError uses a bare 3, ConflictError a bare 4). A constructor
// that disagrees with the table ships the wrong number on the wire even
// though every constant is correct.
func TestConstructorsAgreeWithTheTable(t *testing.T) {
	for _, env := range []*envelope.Error{
		envelope.GenericError("x"),
		envelope.UsageError("x"),
		envelope.NotFoundError("x"),
		envelope.ConflictError("x"),
		envelope.UnauthorizedError("x"),
		envelope.TransientError("x"),
		envelope.ConsentRefusedError("x"),
		envelope.RateLimitedError("x"),
		envelope.ProvenanceMissingError("x"),
		envelope.PrerequisiteError("x"),
	} {
		want, ok := envelope.ExitCodeForClass(env.Code)
		if !ok {
			t.Errorf("constructor produced code %q with no table row", env.Code)
			continue
		}
		if env.ExitCode != want {
			t.Errorf("constructor for %s stamps ExitCode %d but the table says %d",
				env.Code, env.ExitCode, want)
		}
	}
}

// TestExtensionBandSlotsAreUniqueAndContiguous guards the >6 band. The
// slots are allocated across three trees, so nothing but a shared list
// can catch two features claiming the same number.
func TestExtensionBandSlotsAreUniqueAndContiguous(t *testing.T) {
	byExit := map[int]envelope.ExtensionBandSlot{}
	for _, slot := range envelope.ExtensionBand() {
		if prior, dup := byExit[slot.Exit]; dup {
			t.Fatalf("exit %d claimed by both %s (%s) and %s (%s)",
				slot.Exit, prior.Class, prior.Owner, slot.Class, slot.Owner)
		}
		byExit[slot.Exit] = slot
	}
	for exit := 64; exit <= 70; exit++ {
		if _, ok := byExit[exit]; !ok {
			t.Errorf("band slot %d is unallocated; kit allocates 64-70 "+
				"contiguously so a gap means a row was dropped", exit)
		}
	}
	if len(byExit) != 7 {
		t.Errorf("band has %d slots, want 7 (64-70)", len(byExit))
	}
}

// TestClassForExitCodeRoundTrips checks the reverse lookup agrees with
// the forward one for every class.
func TestClassForExitCodeRoundTrips(t *testing.T) {
	for _, class := range envelope.ExitClasses() {
		exit, ok := envelope.ExitCodeForClass(class)
		if !ok {
			t.Fatalf("ExitClasses returned %q, absent from the table", class)
		}
		back, ok := envelope.ClassForExitCode(exit)
		if !ok {
			t.Errorf("ClassForExitCode(%d) found nothing for class %q", exit, class)
			continue
		}
		if back != class {
			t.Errorf("ClassForExitCode(%d) = %q, want %q", exit, back, class)
		}
	}
	if _, ok := envelope.ClassForExitCode(66); ok {
		t.Error("ClassForExitCode(66) resolved; 66 is owned by " +
			"go/console/cli/conformance and must not be claimed here")
	}
	if _, ok := envelope.ExitCodeForClass("ADOPTER_SPECIFIC"); ok {
		t.Error("ExitCodeForClass resolved an adopter-defined class")
	}
}

// parseConstBlock reads this package's non-test sources and returns the
// exported constants whose names start with prefix, mapped to their
// literal values (strings unquoted, ints as decimal strings).
//
// Parsing rather than reflecting because Go constants are not
// reflectable at the package level: there is no runtime list of them.
// The AST is the only way to ask "what does this package declare" and
// therefore the only way to catch a symbol that was added without a
// table row.
func parseConstBlock(t *testing.T, prefix string) map[string]string {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package source: %v", err)
	}

	fset := token.NewFileSet()
	out := map[string]string{}
	parsed := 0
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", src, err)
		}
		parsed++
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, prefix) || !name.IsExported() {
						continue
					}
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok {
						continue
					}
					switch lit.Kind {
					case token.STRING:
						v, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatalf("unquote %s: %v", name.Name, err)
						}
						out[name.Name] = v
					case token.INT:
						out[name.Name] = lit.Value
					}
				}
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no non-test sources parsed; this guard is vacuous")
	}
	if len(out) == 0 {
		t.Fatalf("no exported %s* constants found; the parser is not reading "+
			"the package and this guard is vacuous", prefix)
	}
	return out
}
