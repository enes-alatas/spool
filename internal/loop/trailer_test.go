package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

func TestParseTrailer(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"all done\n[next-wake: 45m]", 45 * time.Minute, true},
		{"done [NEXT-WAKE: 2h]", 2 * time.Hour, true},
		{"[next-wake:30s]", 30 * time.Second, true},
		{"[next-wake: 10 min ]", 10 * time.Minute, true},
		{"no trailer here", 0, false},
		// trailer buried too deep (not in last 5 lines) is ignored
		{"[next-wake: 5m]\na\nb\nc\nd\ne\nf", 0, false},
	}
	for _, testCase := range cases {
		got, ok := ParseTrailer(testCase.in)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("ParseTrailer(%q) = %v,%v want %v,%v", testCase.in, got, ok, testCase.want, testCase.ok)
		}
	}
}

// The doing trailer sets, clears, and sits beside next-wake (#751). It is
// not cut here: the store cuts it, after redaction.
func TestParseDoing(t *testing.T) {
	long := strings.Repeat("é", store.MaxDoing+10)
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"all done\n[next-wake: 45m]\n[doing: implementing #123]", "implementing #123", true},
		{"[DOING:reviewing PR #456] [next-wake: 1h]", "reviewing PR #456", true},
		{"cleared\n[doing: ]", "", true},
		{"cleared\n[doing:]", "", true},
		{"[doing: first]\n[doing: second]", "second", true},
		{"[doing: " + long + " ]", long, true},
		{"no trailer here [next-wake: 5m]", "", false},
		{"[doing: split\nacross lines]", "", false},
		// buried too deep (not in the last 5 lines), as next-wake is
		{"[doing: old]\na\nb\nc\nd\ne\nf", "", false},
	}
	for _, testCase := range cases {
		got, ok := ParseDoing(testCase.in)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("ParseDoing(%q) = %q,%v want %q,%v", testCase.in, got, ok, testCase.want, testCase.ok)
		}
	}
}

func TestStripTrailer(t *testing.T) {
	if got := StripTrailer("hello\n[next-wake: 45m]"); got != "hello" {
		t.Errorf("StripTrailer = %q", got)
	}
	if got := StripTrailer("plain reply"); got != "plain reply" {
		t.Errorf("StripTrailer = %q", got)
	}
}

func TestClamp(t *testing.T) {
	if Clamp(time.Second, time.Minute, time.Hour) != time.Minute {
		t.Error("clamp below min failed")
	}
	if Clamp(2*time.Hour, time.Minute, time.Hour) != time.Hour {
		t.Error("clamp above max failed")
	}
}
