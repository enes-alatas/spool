package claude

import (
	"strings"
	"testing"
)

// A secret the window splits never shows its tail: the excerpt starts on a
// whole line, or is empty when one line fills the window (#1108).
func TestLastLinesNeverSplitsALine(t *testing.T) {
	secret := "synthetic-secret-" + strings.Repeat("x", 40)
	text := "warming up\nremote said " + secret + "\n" + strings.Repeat("y", 70) + "\n"
	if got := LastLines(text, 80); got != strings.Repeat("y", 70)+"\n" {
		t.Errorf("LastLines cut into a line: %q", got)
	}
	if got := LastLines(strings.Repeat("z", 100), 80); got != "" {
		t.Errorf("one line longer than the window: %q, want none of it", got)
	}
	if got := LastLines("boom\n", 80); got != "boom\n" {
		t.Errorf("a short text: %q, want it whole", got)
	}
}

// The stderr a stream keeps is cut on a line too, so an exit's Stderr never
// starts inside a secret (#1108).
func TestReadStderrKeepsWholeLines(t *testing.T) {
	secret := "synthetic-secret-" + strings.Repeat("x", 40)
	line := strings.Repeat("y", 49) + "\n"
	// 8220 bytes: the cut to 8 KiB falls 28 bytes in, inside the secret
	input := "remote said " + secret + "\n" + strings.Repeat(line, 163)
	stream := &Stream{stderrDone: make(chan struct{})}
	stream.readStderr(strings.NewReader(input))
	kept := stream.StderrTail()
	if strings.Contains(kept, "xxxx") {
		t.Errorf("the kept stderr starts inside a line: %q…", kept[:40])
	}
	if !strings.HasSuffix(kept, line) || len(kept) > 8*1024 {
		t.Errorf("the kept stderr is %d bytes and ends %q, want the last lines within 8 KiB", len(kept), kept[len(kept)-10:])
	}
}

// A line longer than the kept 8 KiB is dropped whole: the rest of it, as
// it arrives after the cut, is not kept either, so the kept stderr starts
// on the next line (#1108).
func TestReadStderrDropsALineLongerThanItKeeps(t *testing.T) {
	secret := "synthetic-secret-" + strings.Repeat("x", 40)
	// with 4096-byte reads the buffer is cut at byte 12,288, inside the secret
	long := strings.Repeat("z", 12270-len("head\n")) + secret
	long += strings.Repeat("z", 20000-len(long)) // and a whole read past it with no newline
	stream := &Stream{stderrDone: make(chan struct{})}
	stream.readStderr(strings.NewReader("head\n" + long + "\nafter\n"))
	if kept := stream.StderrTail(); kept != "after\n" {
		t.Errorf("the kept stderr is %q…, want only the line after the long one", kept[:min(len(kept), 40)])
	}
}
