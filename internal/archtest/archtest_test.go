// Package archtest mechanically enforces the seam rules from
// docs/ARCHITECTURE.md (ADR-0004, ADR-0013): seam packages own their
// interfaces and depend on nothing else — least of all their own
// implementations — adapters stay behind those seams, and wiring happens only
// in cmd/. It walks the source tree with go/parser — stdlib only — so a PR
// that erodes a boundary fails CI instead of eroding quietly.
//
// When packages move (telegram → surface/telegram at L3), update the rule
// tables here in the same PR — the rules are the point, the paths are just
// their current addresses.
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

// adapters implement a seam: only cmd/ wiring may import them, and each maps
// to what it may never import itself.
var adapters = map[string][]string{
	// Hub adapters serve the hub and must not reach into the runner at all.
	module + "/internal/telegram":     runnerPkgs, // Surface (moves under internal/surface at L3)
	module + "/internal/store/sqlite": runnerPkgs,
	module + "/web":                   runnerPkgs,
	// SandboxRuntime adapters are the runner's own, so speaking the claude
	// protocol is their job — but they serve the seam, not the loop actors.
	module + "/internal/runtime/bare": {module + "/internal/loop"}, // docker joins it at L1
}

var runnerPkgs = []string{
	module + "/internal/loop",
	module + "/internal/claude",
}

// seamAllowedImports maps a seam's interface package to the only
// module-internal imports it may have. Anything else — a hub type, a store
// row, one of its own implementations — makes the seam un-substitutable, and
// substitutability is the whole point (ADR-0004): docker replaces bare, and
// postgres replaces sqlite, without the dependents noticing.
var seamAllowedImports = map[string][]string{
	module + "/internal/store":   {},                            // the store seam is plain interfaces and rows
	module + "/internal/runtime": {module + "/internal/claude"}, // the SandboxRuntime seam speaks the claude protocol
}

// runnerInternals may only be imported by the runner itself and cmd/ wiring.
var runnerInternals = map[string][]string{
	module + "/internal/claude": {
		module + "/internal/loop",
		module + "/internal/runtime",      // the seam speaks the claude protocol
		module + "/internal/runtime/bare", // …and so does every implementation
	},
}

func TestSeamRules(t *testing.T) {
	imports := collectImports(t)

	for pkg, imps := range imports {
		isWiring := strings.HasPrefix(pkg, module+"/cmd/")
		forbidden, isAdapter := adapters[pkg]
		seamAllows, isSeam := seamAllowedImports[pkg]

		for _, imp := range imps {
			if !strings.HasPrefix(imp, module) {
				continue // stdlib / external deps: not arch-test business
			}
			// A parent may reach into its own subpackage; everyone else has
			// to go through the seam.
			ownSubpackage := strings.HasPrefix(imp, pkg+"/")
			if _, isAdapterImport := adapters[imp]; isAdapterImport && !isWiring && !ownSubpackage {
				t.Errorf("%s imports adapter %s — only cmd/ wiring may (ADR-0004)", pkg, imp)
			}
			if allowed, ok := runnerInternals[imp]; ok && !isWiring && !contains(allowed, pkg) {
				t.Errorf("%s imports runner-internal %s — only %v and cmd/ may", pkg, imp, allowed)
			}
			if isAdapter && contains(forbidden, imp) {
				t.Errorf("adapter %s imports %s — an adapter serves its seam, nothing else (ADR-0004)", pkg, imp)
			}
			if isSeam && !contains(seamAllows, imp) {
				t.Errorf("seam %s imports %s — a seam depends on nothing but its own protocol, and never on an implementation (ADR-0004)", pkg, imp)
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
