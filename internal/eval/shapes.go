package eval

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Shape is one credential or fleet-identifier pattern.
type Shape struct {
	Name string
	Re   *regexp.Regexp
	// BodyOnly marks a fleet identifier — a bot handle, a group chat id —
	// rather than a credential: a quote of the group, not a key. It counts
	// in a GitHub write and on export, not in chat.
	BodyOnly bool
}

// shapeLine matches one `name[i] = "…"; regex[i] = "…"` rule, with its
// optional `only[i] = "body"`. The regex is an awk string, so it may hold an
// escaped quote.
var shapeLine = regexp.MustCompile(`name\[\d+\]\s*=\s*"([^"]+)";\s*regex\[\d+\]\s*=\s*"((?:[^"\\]|\\.)*)"(?:;\s*only\[\d+\]\s*=\s*"([^"]+)")?`)

// awkString undoes the escapes an awk string literal adds: \" and \\. The
// rest (\t) mean the same to Go's regexp.
var awkString = strings.NewReplacer(`\"`, `"`, `\\`, `\`)

// LoadShapes reads the shapes out of scripts/secret-rules.awk. The awk file
// is the one list the PR scan and the body redaction already share; a second
// list here would be a third place to forget a shape the day one is added.
func LoadShapes(path string) ([]Shape, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shapes []Shape
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		match := shapeLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		// The awk front ends fold every line before matching, so the
		// patterns are written in lowercase; (?i) is the same fold.
		re, err := regexp.Compile(`(?i)` + awkString.Replace(match[2]))
		if err != nil {
			return nil, fmt.Errorf("shape %s: %w", match[1], err)
		}
		shapes = append(shapes, Shape{Name: match[1], Re: re, BodyOnly: match[3] == "body"})
	}
	if len(shapes) == 0 {
		return nil, fmt.Errorf("%s: no shapes found", path)
	}
	return shapes, nil
}

// Scrub replaces every match of every shape with <redacted:name>.
func Scrub(shapes []Shape, text string) string {
	for _, shape := range shapes {
		text = shape.Re.ReplaceAllString(text, "<redacted:"+shape.Name+">")
	}
	return text
}
