package outbound

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestExcerptCutsOnRuneBoundary pins the property that matters for a note the
// operator reads: the excerpt of a lost message is always valid UTF-8, whatever
// byte length the message happens to have. Turkish, because it is the language
// the note is most likely to be quoting.
func TestExcerptCutsOnRuneBoundary(t *testing.T) {
	const limit = 12
	body := strings.Repeat("çalışıyor ", 8)
	for pad := 0; pad < 16; pad++ {
		text := strings.Repeat("a", pad) + body
		got := Excerpt(text, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("pad=%d: excerpt is not valid UTF-8: %q", pad, got)
		}
		if len(got) > limit+len("…") {
			t.Fatalf("pad=%d: excerpt %q exceeds the cap", pad, got)
		}
		if !strings.HasPrefix(text, strings.TrimSuffix(got, "…")) {
			t.Fatalf("pad=%d: excerpt %q is not the opening of the message", pad, got)
		}
	}
}
