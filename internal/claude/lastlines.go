package claude

import "strings"

// LastLines is the tail of text within limit bytes, starting on a whole
// line. A store or log redacts an excerpt after it is cut, and a secret cut
// in two would no longer match, so a partial first line is dropped instead,
// and a single line longer than limit leaves nothing (#1108).
func LastLines(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[len(text)-limit:]
	if newline := strings.IndexByte(cut, '\n'); newline >= 0 {
		return cut[newline+1:]
	}
	return ""
}
