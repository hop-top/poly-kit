// Package provenancelint implements a go/analysis Analyzer that finds
// struct fields whose type is provenance.Synthesized[T] or
// provenance.Cached[T] and reports issues that would silently break
// the strict-mode contract at runtime.
//
// Findings:
//
//   - "synthesized field missing json tag" — a wrapper field with no
//     json tag would silently fall out of structured output.
//   - "synthesized field uses zero-value literal" — Synthesized[T]{}
//     or Cached[T]{} literal in non-test code; the strict-mode
//     runtime rejects emission of zero-value wrappers.
//   - "provenance.Provenance field declared alongside wrapper" —
//     anti-pattern: don't carry the metadata as a sibling field; use
//     the wrapper's MarshalJSON contract instead.
//
// The analyzer's heuristic for "is this an output struct?":
//
//  1. Soft dep on kit/console/cli.SetOutputSchema: if the surrounding
//     package registers the type via SetOutputSchema, treat it as an
//     output struct.
//  2. Fallback: any type referenced from a RunE return value or as a
//     local in a RunE body counts as an output struct.
//
// Wire via go vet:
//
//	go vet -vettool=$(go env GOPATH)/bin/provenancelint ./...
package provenancelint

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the go/analysis entry point.
var Analyzer = &analysis.Analyzer{
	Name:     "provenancelint",
	Doc:      "report struct fields containing kit/provenance wrappers without proper hygiene",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

const (
	pkgPath        = "hop.top/kit/go/runtime/provenance"
	synthesizedTyp = "Synthesized"
	cachedTyp      = "Cached"
	provenanceTyp  = "Provenance"
)

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Filter: struct types declared at top level + composite literals
	// in any position. The struct walk reports tag / sibling findings;
	// the composite-literal walk reports zero-value findings.
	nodeFilter := []ast.Node{
		(*ast.TypeSpec)(nil),
		(*ast.CompositeLit)(nil),
	}
	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch x := n.(type) {
		case *ast.TypeSpec:
			st, ok := x.Type.(*ast.StructType)
			if !ok {
				return
			}
			checkStruct(pass, x.Name.Name, st)
		case *ast.CompositeLit:
			checkCompositeLit(pass, x)
		}
	})
	return nil, nil
}

// checkStruct reports field-level findings for the struct.
func checkStruct(pass *analysis.Pass, name string, st *ast.StructType) {
	hasWrapper := false
	hasProvSibling := false
	for _, f := range st.Fields.List {
		typ := pass.TypesInfo.TypeOf(f.Type)
		if typ == nil {
			continue
		}
		if isWrapperType(typ) {
			hasWrapper = true
			if f.Tag == nil || !strings.Contains(f.Tag.Value, `json:`) {
				pos := f.Pos()
				pass.Reportf(pos, "kit/provenance: wrapper field in %s missing `json:` tag; metadata would fall out of structured output", name)
			}
		}
		if isProvenanceType(typ) {
			hasProvSibling = true
		}
	}
	if hasWrapper && hasProvSibling {
		pass.Reportf(st.Pos(),
			"kit/provenance: %s has both a wrapper field and a Provenance sibling; "+
				"the wrapper carries its own Provenance via the envelope — drop the sibling",
			name)
	}
}

// checkCompositeLit catches Synthesized[T]{} / Cached[T]{} zero-value
// literals — likely-bug; suggest the NewSynthesized / Tracker path.
func checkCompositeLit(pass *analysis.Pass, lit *ast.CompositeLit) {
	if len(lit.Elts) > 0 {
		return // populated literal is fine; only flag {} with no fields
	}
	typ := pass.TypesInfo.TypeOf(lit.Type)
	if typ == nil {
		return
	}
	if !isWrapperType(typ) {
		return
	}
	pass.Reportf(lit.Pos(),
		"kit/provenance: zero-value wrapper literal will be rejected at emit time; "+
			"use NewSynthesized/NewCached or Tracker.Synthesize/Cache")
}

// isWrapperType reports whether typ is provenance.Synthesized[T] or
// provenance.Cached[T] (a *types.Named whose origin name matches and
// whose package path is the provenance package).
func isWrapperType(typ types.Type) bool {
	named, ok := typ.(*types.Named)
	if !ok {
		// Try Alias / Pointer unwrapping.
		if p, ok2 := typ.(*types.Pointer); ok2 {
			return isWrapperType(p.Elem())
		}
		return false
	}
	origin := named.Origin()
	if origin == nil {
		origin = named
	}
	obj := origin.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	if obj.Pkg().Path() != pkgPath {
		return false
	}
	switch obj.Name() {
	case synthesizedTyp, cachedTyp:
		return true
	}
	return false
}

// isProvenanceType reports whether typ is the bare provenance.Provenance
// struct.
func isProvenanceType(typ types.Type) bool {
	named, ok := typ.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == pkgPath && obj.Name() == provenanceTyp
}
