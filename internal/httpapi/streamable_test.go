package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestEveryBusKindIsClassifiedForMembers: every bus.Kind constant is named
// by a case of streamable, so whether it reaches a member is a decision,
// not the fall-through. A kind named nowhere reaches no member, so this
// test is how its author hears of it before the control room misses its
// live updates (#1240).
func TestEveryBusKindIsClassifiedForMembers(t *testing.T) {
	files := token.NewFileSet()
	busFile, err := parser.ParseFile(files, "../bus/bus.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, declaration := range busFile.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				if strings.HasPrefix(name.Name, "Kind") {
					kinds = append(kinds, name.Name)
				}
			}
		}
	}
	if len(kinds) == 0 {
		t.Fatal("found no Kind constants in internal/bus/bus.go")
	}

	rolesFile, err := parser.ParseFile(files, "roles.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, declaration := range rolesFile.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "streamable" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				if selector, ok := expression.(*ast.SelectorExpr); ok {
					if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "bus" {
						named[selector.Sel.Name] = true
					}
				}
			}
			return true
		})
	}
	if len(named) == 0 {
		t.Fatal("found no bus kinds in streamable's cases")
	}
	for _, kind := range kinds {
		if !named[kind] {
			t.Errorf("bus.%s is named by no case of streamable: decide whether a member's stream carries it", kind)
		}
	}
}
