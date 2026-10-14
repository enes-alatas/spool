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
	match := trailerRe.FindStringSubmatch(replyTail(text))
	if match == nil {
		return 0, false
	}
	amount, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(match[2]) {
	case "s":
		return time.Duration(amount) * time.Second, true
	case "m", "min":
		return time.Duration(amount) * time.Minute, true
	default: // h, hr
		return time.Duration(amount) * time.Hour, true
	}
}

// doingRe is the [doing: …] trailer: what the loop says it is working on
// (#751). The value runs to the closing bracket, on one line.
var doingRe = regexp.MustCompile(`(?i)\[doing:([^\]\n]*)\]`)

// ParseDoing scans the last few lines of a reply for a [doing: …] trailer,
// as ParseTrailer does for next-wake. It returns the value trimmed and
// whether there was one: "" with true clears the doing, and no trailer
// leaves it as it was. When a reply carries more than one, the last counts.
// The value is not cut here: the store cuts it to store.MaxDoing after it
// is redacted, so a long secret is never split before it can match.
func ParseDoing(text string) (string, bool) {
	matches := doingRe.FindAllStringSubmatch(replyTail(text), -1)
	if matches == nil {
		return "", false
	}
	return strings.TrimSpace(matches[len(matches)-1][1]), true
}

// replyTail is the last few lines of a reply, where its trailers are.
func replyTail(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.Join(lines[max(len(lines)-5, 0):], "\n")
}

// StripTrailer removes the trailer from a reply for display.
func StripTrailer(text string) string {
	return strings.TrimSpace(trailerRe.ReplaceAllString(text, ""))
}

// Clamp bounds a requested wake duration to [min, max].
func Clamp(duration, min, max time.Duration) time.Duration {
	if duration < min {
		return min
	}
	if duration > max {
		return max
	}
	return duration
}
