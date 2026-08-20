package telegram

import (
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A group's ingest election only counts bots whose binding predates the
// message, so pollers racing on one message read the same candidate set.
func TestBoundBefore(t *testing.T) {
	const msgDate = 1_700_000_000 // seconds, Telegram's clock

	cases := []struct {
		name      string
		boundAtMS int64
		want      bool
	}{
		{"never recorded, so from before the rule existed", 0, true},
		{"bound well before the message", (msgDate - 3600) * 1000, true},
		{"bound just outside the settle margin", (msgDate - int64(bindSettle.Seconds()) - 1) * 1000, true},
		{"bound inside the settle margin", (msgDate - 1) * 1000, false},
		{"bound while handling this very message", msgDate * 1000, false},
		{"bound after the message, catching up on a backlog", (msgDate + 60) * 1000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := boundBefore(&store.Loop{TGGroupBoundAt: c.boundAtMS}, msgDate)
			if got != c.want {
				t.Fatalf("boundBefore(bound_at=%d, date=%d) = %v, want %v",
					c.boundAtMS, msgDate, got, c.want)
			}
		})
	}
	if bindSettle <= 0 {
		t.Fatalf("bindSettle must be positive, got %v", time.Duration(bindSettle))
	}
}
