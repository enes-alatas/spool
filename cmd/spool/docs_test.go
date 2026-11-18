package main

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// TestDocsLinksLandOnAHeading holds each docs link the upgrade prints to a
// page in site/content and a heading on it, as the site's own anchor for
// that heading spells it.
func TestDocsLinksLandOnAHeading(t *testing.T) {
	for _, link := range []string{docsWithUpgrade, docsByHand, docsRollingBack, docsBackupScripts} {
		rest, ok := strings.CutPrefix(link, docsSite)
		page, anchor, hasAnchor := strings.Cut(rest, "#")
		if !ok || !hasAnchor || !strings.HasSuffix(page, "/") {
			t.Errorf("%s: want %spage/#anchor", link, docsSite)
			continue
		}
		file := filepath.Join("..", "..", "site", "content", strings.TrimSuffix(page, "/")+".md")
		anchors, err := headingAnchors(file)
		if err != nil {
			t.Errorf("%s: %v", link, err)
			continue
		}
		if !anchors[anchor] {
			t.Errorf("%s: %s has no heading #%s", link, file, anchor)
		}
	}
}

// headingAnchors is the anchor of each heading in a markdown file, as Hugo
// makes it: lower case, punctuation dropped, spaces as dashes.
func headingAnchors(file string) (map[string]bool, error) {
	md, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer md.Close()
	anchors := map[string]bool{}
	fenced := false
	sc := bufio.NewScanner(md)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if fenced || !strings.HasPrefix(line, "#") {
			continue
		}
		text := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(text, " ") {
			continue
		}
		var anchor strings.Builder
		for _, char := range strings.ToLower(strings.TrimSpace(text)) {
			switch {
			case unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_':
				anchor.WriteRune(char)
			case char == ' ':
				anchor.WriteRune('-')
			}
		}
		anchors[anchor.String()] = true
	}
	return anchors, sc.Err()
}

// TestNoMessageNamesAMovedDoc finds no string in cmd/ or internal/ that
// sends an operator to a file the docs site replaced (ADR-0071): the
// file keeps only stubs, one click short of the answer.
func TestNoMessageNamesAMovedDoc(t *testing.T) {
	moved := []string{"OPERATIONS", "TELEGRAM.md", "SLACK.md", "CONTAINMENT.md"}
	for _, root := range []string{filepath.Join("..", "..", "cmd"), filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, name := range moved {
					if strings.Contains(lit.Value, name) {
						t.Errorf("%s: a string names %s, which the docs site replaced: %s", path, name, lit.Value)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
