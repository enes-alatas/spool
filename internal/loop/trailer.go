package loop

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var trailerRe = regexp.MustCompile(`(?i)\[next-wake:\s*(\d+)\s*(s|m|min|h|hr)\s*\]`)

// ParseTrailer scans the last few lines of a reply for a [next-wake: 45m]
// trailer. Returns the requested duration and whether one was found.
func ParseTrailer(text string) (time.Duration, bool) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	start := len(lines) - 5
	if start < 0 {
		start = 0
	}
	tail := strings.Join(lines[start:], "\n")
	m := trailerRe.FindStringSubmatch(tail)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(m[2]) {
	case "s":
		return time.Duration(n) * time.Second, true
	case "m", "min":
		return time.Duration(n) * time.Minute, true
	default: // h, hr
		return time.Duration(n) * time.Hour, true
	}
}

// StripTrailer removes the trailer from a reply for display.
func StripTrailer(text string) string {
	return strings.TrimSpace(trailerRe.ReplaceAllString(text, ""))
}

// Clamp bounds a requested wake duration to [min, max].
func Clamp(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}
