package cmdsurface

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestMCPFixtureGeneratorUsesNoDeprecatedAPI keeps the cross-language
// MCP wire-fixture generator off the deprecated public mount. MountMCP,
// MCPOption and the WithMCP* options go when MountMCP is removed; the
// fixtures must still regenerate then, so the generator mounts through
// the unexported mountMCP. The lock-suite server helpers are refused
// too: they call MountMCP.
func TestMCPFixtureGeneratorUsesNoDeprecatedAPI(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "surface_mcp_fixtures_gen_test.go", nil, 0)
	if err != nil {
		t.Fatalf("parse generator: %v", err)
	}
	refused := map[string]bool{
		"MountMCP":         true,
		"MCPOption":        true,
		"legacyLockServer": true,
		"modernLockServer": true,
	}
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if refused[id.Name] || strings.HasPrefix(id.Name, "WithMCP") {
			t.Errorf("%s: generator references %s; mount through mountMCP instead",
				fset.Position(id.Pos()), id.Name)
		}
		return true
	})
}
