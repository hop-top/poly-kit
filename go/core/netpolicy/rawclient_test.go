package netpolicy_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// A client built as http.Client{Transport: netpolicy.Guard(...)} judges
// every request but follows redirects unchecked, so a hop to another
// origin forwards credential headers. Outside this package, guarded
// clients come from NewClient, or install CheckRedirect.
func TestNoRawGuardedClients(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(self)
	root := filepath.Join(pkgDir, "..", "..") // the go/ tree

	var bad []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == pkgDir || d.Name() == "node_modules" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil // not this test's concern
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isHTTPClient(lit.Type) {
				return true
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Transport" && isGuardCall(kv.Value) {
					rel, _ := filepath.Rel(root, path)
					bad = append(bad, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("%s: http.Client{Transport: netpolicy.Guard(...)} skips the redirect check; use netpolicy.NewClient or set CheckRedirect: netpolicy.CheckRedirect(...)", b)
	}
}

func isHTTPClient(e ast.Expr) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != "Client" {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == "http"
}

func isGuardCall(e ast.Expr) bool {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	s, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != "Guard" {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == "netpolicy"
}
