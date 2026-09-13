package svc

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

// TestNoLocalCodeShadowsAKitClass is this package's drift guard.
//
// svc's vocabulary is legitimately its own: CASSETTE_MALFORMED and
// JUDGE_QUOTA_EXCEEDED are not kit classes and kit has no opinion on
// their exit codes. That is why this file does not assert svc's table
// against kit's — they are different tables by design.
//
// What must hold is narrower and was violated: where a code string here
// IS one of kit's class names, it must answer kit's number. It did not.
// CodeProvenanceMissing carries the string "PROVENANCE_MISSING", which
// kit assigns exit 65, and ExitForCode returned 6 — so this service
// reported a permanent refusal with the transient class's code, and an
// agent branching on $? would retry forever.
//
// The guard reads the Code* constants out of the source rather than
// listing them, so a new constant that happens to collide with a kit
// class is caught the moment it is added.
func TestNoLocalCodeShadowsAKitClass(t *testing.T) {
	constants := parseCodeConstants(t)
	shadowed := 0
	for name, value := range constants {
		want, isKitClass := envelope.ExitCodeForClass(value)
		if !isKitClass {
			continue
		}
		shadowed++
		if got := ExitForCode(value); got != want {
			t.Errorf("%s = %q is a kit class, but ExitForCode returns %d and kit "+
				"assigns %d.\n"+
				"A code string kit owns must answer kit's number. Either return "+
				"kit's exit code, or rename the constant so the two vocabularies "+
				"stop colliding.", name, value, got, want)
		}
	}
	if shadowed == 0 {
		t.Fatal("no svc code string matched a kit class; this guard is vacuous " +
			"and would not have caught the PROVENANCE_MISSING defect")
	}
}

// TestEveryCodeConstantIsClassified checks ExitForCode does not quietly
// fall through for a declared code. The fallback is GENERIC/1, so an
// unclassified code is reported as an uncharacterized failure rather
// than as the class it belongs to — the same silent-wrong-answer shape
// the harness table had.
func TestEveryCodeConstantIsClassified(t *testing.T) {
	// A code deliberately classified GENERIC needs an entry here, so
	// "classified as 1" and "fell through to 1" stay distinguishable.
	genericByChoice := map[string]bool{
		CodeSvcInternal:    true,
		CodeGraderInternal: true,
		// L4B_NOT_IMPLEMENTED answers HTTP 501; the shared taxonomy has
		// no "not implemented" class, and it is a server-side gap rather
		// than a caller error, so GENERIC is the honest class.
		CodeL4BNotImplemented: true,
	}
	for name, value := range parseCodeConstants(t) {
		if ExitForCode(value) == envelope.ExitGeneric && !genericByChoice[value] {
			t.Errorf("%s = %q resolves to GENERIC/1 via the fallback, not by "+
				"choice.\nAdd it to a case in ExitForCode, or record it in this "+
				"test's genericByChoice map with the reason.", name, value)
		}
	}
}

// TestEveryCodeConstantHasAStatus is the same coverage guard for the
// HTTP side: HTTPStatus also falls through to 500.
func TestEveryCodeConstantHasAStatus(t *testing.T) {
	internalByChoice := map[string]bool{
		CodeSvcInternal:           true,
		CodeGraderInternal:        true,
		CodeJudgePromptUnresolved: true,
	}
	for name, value := range parseCodeConstants(t) {
		if HTTPStatus(value) == 500 && !internalByChoice[value] {
			t.Errorf("%s = %q resolves to HTTP 500 via the fallback, not by "+
				"choice.\nAdd it to a case in HTTPStatus, or record it in this "+
				"test's internalByChoice map with the reason.", name, value)
		}
	}
}

// parseCodeConstants returns this package's exported Code* string
// constants mapped to their literal values. Parsed rather than listed:
// a list here would be a second copy of the constant block and would
// drift out of date exactly as the tables this work removed did.
func parseCodeConstants(t *testing.T) map[string]string {
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
					if !strings.HasPrefix(name.Name, "Code") || !name.IsExported() {
						continue
					}
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquote %s: %v", name.Name, err)
					}
					out[name.Name] = v
				}
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no non-test sources parsed; this guard is vacuous")
	}
	if len(out) == 0 {
		t.Fatal("no exported Code* constants found; this guard is vacuous")
	}
	return out
}
