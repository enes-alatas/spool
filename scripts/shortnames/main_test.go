package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// names returns the single-letter names check reports in src, in order.
func names(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", "package x\n"+src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, finding := range check(fset, file) {
		found = append(found, finding.name)
	}
	return found
}

func TestEachKindOfDeclarationIsCaught(t *testing.T) {
	cases := map[string]string{
		"short variable":  "func f() { a := 1; _ = a }",
		"var":             "var a = 1",
		"const":           "const a = 1",
		"parameter":       "func f(a int) {}",
		"result":          "func f() (a int) { return }",
		"receiver":        "type s struct{}\nfunc (a s) f() {}",
		"type parameter":  "func f[a any]() {}",
		"generic type":    "type s[a any] struct{}",
		"range value":     "func f(xs []int) { for _, a := range xs { _ = a } }",
		"type switch":     "func f(value any) { switch a := value.(type) { default: _ = a } }",
		"closure param":   "var fn = func(a int) {}",
		"interface param": "type s interface{ f(a int) }",
	}
	for name, src := range cases {
		if got := names(t, src); !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("%s: got %v, want [a]", name, got)
		}
	}
}

func TestTheConventionalExceptionsPass(t *testing.T) {
	cases := map[string]string{
		"for index":       "func f() { for i := 0; i < 3; i++ {}; for i, j := 0, 1; i < j; i++ {} }",
		"range index":     "func f(xs []int) { for i := range xs { _ = i } }",
		"testing.T":       "func f(t *testing.T) {}",
		"handler":         "func f(w http.ResponseWriter, r *http.Request) {}",
		"blank":           "func f() { _ := 1; var _ = 2 }",
		"long names":      "func f(ok bool, err error, ctx any) {}",
		"reassignment":    "func f() { a = 2 }",
		"struct field":    "type s struct{ a int }",
		"composite field": "var value = struct{ a int }{a: 1}",
	}
	for name, src := range cases {
		if got := names(t, src); len(got) != 0 {
			t.Errorf("%s: got %v, want none", name, got)
		}
	}
}

// An exception is the name and the type together: i outside a loop, or t, w
// and r of any other type, is reported.
func TestAnExceptionNeedsItsContext(t *testing.T) {
	cases := map[string]string{
		"i outside a loop": "func f() { i := 1; _ = i }",
		"t not testing.T":  "func f(t int) {}",
		"t testing.T":      "func f(t testing.T) {}",
		"r not a pointer":  "func f(r http.Request) {}",
		"w other type":     "func f(w io.Writer) {}",
		"loop body var":    "func f() { for i := 0; i < 1; i++ { j := i; _ = j } }",
	}
	for name, src := range cases {
		if got := names(t, src); len(got) != 1 {
			t.Errorf("%s: got %v, want one finding", name, got)
		}
	}
}

// findingsIn is n findings in each named file.
func findingsIn(files map[string]int) []finding {
	var findings []finding
	for file, count := range files {
		for line := 1; line <= count; line++ {
			findings = append(findings, finding{pos: token.Position{Filename: file, Line: line}, name: "s"})
		}
	}
	return findings
}

func TestTheBaselineOnlyShrinks(t *testing.T) {
	findings := findingsIn(map[string]int{"a.go": 2, "b.go": 1})
	if lines, code := run(findings, map[string]int{"a.go": 2, "b.go": 1}, false, ""); code != 0 {
		t.Errorf("at the baseline: exit %d, %v", code, lines)
	}
	lines, code := run(findings, map[string]int{"a.go": 2}, false, "")
	if code != 1 || !strings.HasPrefix(lines[0], "b.go:1:") {
		t.Errorf("a new file with a finding: exit %d, %v, want b.go:1 first", code, lines)
	}
	lines, _ = run(findings, map[string]int{"a.go": 1, "b.go": 1}, false, "")
	if over := strings.Count(strings.Join(lines, "\n"), "a.go:"); over != 2 {
		t.Errorf("a file over its count lists each finding: got %d, want 2: %v", over, lines)
	}
	lines, code = run(findings, map[string]int{"a.go": 3, "b.go": 1}, false, "")
	if code != 1 || !strings.Contains(lines[0], "lower it") || !strings.Contains(lines[len(lines)-1], "-write") {
		t.Errorf("a file under its count: exit %d, %v, want a stale line and the -write hint", code, lines)
	}
}

// An author who adds a name is told to rename it, never to run -write: that
// would be an instruction to raise the count.
func TestAnOverCountIsNotToldToWrite(t *testing.T) {
	findings := findingsIn(map[string]int{"a.go": 2, "b.go": 1})
	lines, code := run(findings, map[string]int{"a.go": 1, "b.go": 5}, false, "")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if out := strings.Join(lines, "\n"); strings.Contains(out, "-write") {
		t.Errorf("an over-count failure names -write:\n%s", out)
	}
}

func TestWriteOnlyLowers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.txt")
	const before = "2 a.go\n1 b.go\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]map[string]int{
		"over its count": {"a.go": 3},
		"a new file":     {"a.go": 1, "c.go": 1},
	} {
		lines, code := run(findingsIn(files), map[string]int{"a.go": 2, "b.go": 1}, true, path)
		if code != 1 {
			t.Errorf("%s: exit %d, want 1: %v", name, code, lines)
		}
		if got, _ := os.ReadFile(path); string(got) != before {
			t.Errorf("%s: the baseline changed to %q", name, got)
		}
	}
	if lines, code := run(findingsIn(map[string]int{"a.go": 1}), map[string]int{"a.go": 2, "b.go": 1}, true, path); code != 0 {
		t.Fatalf("lowering: exit %d, %v", code, lines)
	}
	baseline, err := readBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"a.go": 1}; !reflect.DeepEqual(baseline, want) {
		t.Errorf("lowered baseline = %v, want %v", baseline, want)
	}
}
