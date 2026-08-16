// Package archtest mechanically enforces the seam rules from
// docs/ARCHITECTURE.md (ADR-0004, ADR-0013): adapters stay behind their
// seams, wiring happens only in cmd/, and the store interface package stays
// dependency-free. It walks the source tree with go/parser — stdlib only —
// so a PR that erodes a boundary fails CI instead of eroding quietly.
//
// When packages move (telegram → surface/telegram at L3, runtime seam at L1),
// update the rule tables here in the same PR — the rules are the point, the
// paths are just their current addresses.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/enes-alatas/spool"

// adapters are packages implementing a seam; only cmd/ wiring may import them.
var adapters = []string{
	module + "/internal/telegram", // Surface adapter (moves under internal/surface at L3)
	module + "/internal/store/sqlite",
	module + "/web",
}

// runnerInternals may only be imported by the runner itself and cmd/ wiring.
var runnerInternals = map[string][]string{
	module + "/internal/claude": {module + "/internal/loop"},
}

// adapterForbidden: adapters talk to the hub, never to the runner.
var adapterForbidden = []string{
	module + "/internal/loop",
	module + "/internal/claude",
}

func TestSeamRules(t *testing.T) {
	imports := collectImports(t)

	for pkg, imps := range imports {
		isWiring := strings.HasPrefix(pkg, module+"/cmd/")
		isAdapter := contains(adapters, pkg)

		for _, imp := range imps {
			if !strings.HasPrefix(imp, module) {
				continue // stdlib / external deps: not arch-test business
			}
			if contains(adapters, imp) && !isWiring && pkg != imp && !strings.HasPrefix(imp, pkg) {
				t.Errorf("%s imports adapter %s — only cmd/ wiring may (ADR-0004)", pkg, imp)
			}
			if allowed, ok := runnerInternals[imp]; ok && !isWiring && !contains(allowed, pkg) {
				t.Errorf("%s imports runner-internal %s — only %v and cmd/ may", pkg, imp, allowed)
			}
			if isAdapter && contains(adapterForbidden, imp) {
				t.Errorf("adapter %s imports %s — adapters talk to the hub, not the runner", pkg, imp)
			}
			if pkg == module+"/internal/store" {
				t.Errorf("internal/store imports %s — the store seam must stay dependency-free", imp)
			}
		}
	}
}

// collectImports maps each package import path to the module-internal imports
// of its non-test files.
func collectImports(t *testing.T) map[string][]string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "node_modules", "dist", ".data", "scripts", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := module
		if rel != "." {
			pkg = module + "/" + filepath.ToSlash(rel)
		}
		for _, imp := range f.Imports {
			out[pkg] = append(out[pkg], strings.Trim(imp.Path.Value, `"`))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 5 {
		t.Fatalf("suspiciously few packages found (%d) — walker broken?", len(out))
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
