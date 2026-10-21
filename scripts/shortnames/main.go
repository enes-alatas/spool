// Command shortnames fails when Go code declares a single-letter name.
//
// CONVENTIONS.md "Names say what they hold": `actor`, not `a`. Review alone
// let hundreds through, so this check enforces it (#1012). The exceptions are
// the ones the prose lists: `i` and `j` as loop indices, `t *testing.T`, and
// `w http.ResponseWriter` with `r *http.Request` as parameters. `_` is not a
// name. Variables, constants, parameters, results, receivers and type
// parameters are read; struct fields, labels and import aliases are not.
//
// Code that predates the check is fenced off by a baseline: a count of
// findings per file. A file may not exceed its count, and a file whose count
// fell must have its line lowered, so the fence only ever shrinks.
//
// Usage:
//
//	go run ./scripts/shortnames [-write]
//
// Run from the repository root. -write lowers the baseline to what is found,
// and refuses while any file is over its count: raising one is a hand edit,
// so it shows in review as one. Exits 1 and prints file:line findings.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const baselinePath = "scripts/shortnames/baseline.txt"

// finding is one single-letter declaration.
type finding struct {
	pos  token.Position
	name string
}

func main() {
	write := flag.Bool("write", false, "lower the baseline to what is found")
	flag.Parse()
	findings, err := scan([]string{"cmd", "internal", "itest", "scripts"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "shortnames:", err)
		os.Exit(2)
	}
	baseline, err := readBaseline(baselinePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shortnames:", err)
		os.Exit(2)
	}
	lines, code := run(findings, baseline, *write, baselinePath)
	for _, line := range lines {
		fmt.Println(line)
	}
	os.Exit(code)
}

// run checks findings against baseline, or with write lowers the baseline at
// path to them, and returns what to print and the exit code. A finding over
// its file's count fails either way, and is never told to run -write.
func run(findings []finding, baseline map[string]int, write bool, path string) ([]string, int) {
	counts := map[string]int{}
	for _, found := range findings {
		counts[found.pos.Filename]++
	}
	over, stale := compare(findings, counts, baseline)
	if len(over) > 0 {
		lines := append(over, "Name it for what it holds (docs/CONVENTIONS.md, \"Names say what they hold\").")
		if write {
			lines = append(lines, "-write only lowers counts; nothing was written.")
		}
		return lines, 1
	}
	if write {
		if err := writeBaseline(path, counts); err != nil {
			return []string{"shortnames: " + err.Error()}, 2
		}
		return nil, 0
	}
	if len(stale) > 0 {
		return append(stale, "Lower the baseline: go run ./scripts/shortnames -write"), 1
	}
	return nil, 0
}

// compare returns a line for every finding in a file over its baseline, and a
// line per file under its baseline.
func compare(findings []finding, counts, baseline map[string]int) (over, stale []string) {
	for _, found := range findings {
		file := found.pos.Filename
		if counts[file] > baseline[file] {
			over = append(over, fmt.Sprintf("%s:%d:%d: single-letter name %q", file, found.pos.Line, found.pos.Column, found.name))
		}
	}
	for file, allowed := range baseline {
		if counts[file] < allowed {
			stale = append(stale, fmt.Sprintf("%s: %d single-letter names left, baseline allows %d; lower it", file, counts[file], allowed))
		}
	}
	sort.Strings(stale)
	return over, stale
}

// scan parses every .go file under roots, testdata excluded.
func scan(roots []string) ([]finding, error) {
	var findings []finding
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			findings = append(findings, check(fset, file)...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

// check returns the single-letter declarations in file outside the
// exceptions.
func check(fset *token.FileSet, file *ast.File) []finding {
	var findings []finding
	allowed := map[*ast.Ident]bool{}
	report := func(ident *ast.Ident) {
		if ident == nil || len(ident.Name) != 1 || ident.Name == "_" || allowed[ident] {
			return
		}
		findings = append(findings, finding{pos: fset.Position(ident.Pos()), name: ident.Name})
	}
	fields := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, ident := range field.Names {
				if !exempt(ident.Name, field.Type) {
					report(ident)
				}
			}
		}
	}
	loopIndex := func(expr ast.Expr) {
		if ident, ok := expr.(*ast.Ident); ok && (ident.Name == "i" || ident.Name == "j") {
			allowed[ident] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ForStmt:
			if assign, ok := node.Init.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					loopIndex(lhs)
				}
			}
		case *ast.RangeStmt:
			loopIndex(node.Key)
			if node.Tok == token.DEFINE {
				for _, expr := range []ast.Expr{node.Key, node.Value} {
					if ident, ok := expr.(*ast.Ident); ok {
						report(ident)
					}
				}
			}
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						report(ident)
					}
				}
			}
		case *ast.ValueSpec:
			for _, ident := range node.Names {
				report(ident)
			}
		case *ast.FuncDecl:
			fields(node.Recv)
		case *ast.FuncType:
			fields(node.TypeParams)
			fields(node.Params)
			fields(node.Results)
		case *ast.TypeSpec:
			fields(node.TypeParams)
		}
		return true
	})
	return findings
}

// exempt reports whether a parameter of this name and type is one of the
// conventional exceptions.
func exempt(name string, typ ast.Expr) bool {
	switch name {
	case "t":
		return typeIs(typ, true, "testing", "T")
	case "w":
		return typeIs(typ, false, "http", "ResponseWriter")
	case "r":
		return typeIs(typ, true, "http", "Request")
	}
	return false
}

// typeIs reports whether typ is pkg.name, or *pkg.name when pointer is set.
func typeIs(typ ast.Expr, pointer bool, pkg, name string) bool {
	if pointer {
		star, ok := typ.(*ast.StarExpr)
		if !ok {
			return false
		}
		typ = star.X
	}
	selector, ok := typ.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg && selector.Sel.Name == name
}

func readBaseline(path string) (map[string]int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	baseline := map[string]int{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		count, path, ok := strings.Cut(line, " ")
		allowed, err := strconv.Atoi(count)
		if !ok || err != nil {
			return nil, fmt.Errorf("%s: bad line %q, want \"<count> <file>\"", baselinePath, line)
		}
		baseline[path] = allowed
	}
	return baseline, scanner.Err()
}

func writeBaseline(path string, counts map[string]int) error {
	files := make([]string, 0, len(counts))
	for file := range counts {
		files = append(files, file)
	}
	sort.Strings(files)
	var out strings.Builder
	out.WriteString("# Single-letter names that predate scripts/shortnames, per file (#1012).\n")
	out.WriteString("# Lower a count as you rename; never raise one. Lower: go run ./scripts/shortnames -write\n")
	for _, file := range files {
		fmt.Fprintf(&out, "%d %s\n", counts[file], file)
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}
