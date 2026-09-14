package route

import (
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestMentions(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"@ping 1", []string{"ping"}},
		{"hey @Fixer and @watcher-2, look", []string{"fixer", "watcher-2"}},
		{"emails like a@b.com don't count", nil},
		{"@dup @dup once", []string{"dup"}},
		{"(@paren) works", []string{"paren"}},
		{"none here", nil},
		{"@planner_spool_bot hello", []string{"planner_spool_bot"}},
	}
	for _, c := range cases {
		got := Mentions(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Mentions(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

// TestVisibility pins ADR-0023's classification: human-authored messages are
// always human-facing; a loop's message is human-facing only when it replies
// a turn that carried a human trigger (a DM, or a non-DM one — group/web).
// Everything else a loop sends — mentioning another loop, a tick reply — is
// coordination.
func TestVisibility(t *testing.T) {
	cases := []struct {
		name string
		in   InboundMessage
		want string
	}{
		{"human via telegram dm", InboundMessage{Origin: store.OriginTelegramDM}, store.VisibilityHumanFacing},
		{"human via telegram group", InboundMessage{Origin: store.OriginTelegramGroup}, store.VisibilityHumanFacing},
		{"human via web", InboundMessage{Origin: store.OriginWeb}, store.VisibilityHumanFacing},
		{"loop mentioning another loop, no human trigger",
			InboundMessage{FromLoopID: "loop_a"}, store.VisibilityCoordination},
		{"loop replying a DM-triggered turn",
			InboundMessage{FromLoopID: "loop_a", ReplyDMChats: []int64{42}}, store.VisibilityHumanFacing},
		{"loop replying a group-triggered turn",
			InboundMessage{FromLoopID: "loop_a", GroupWorthy: true}, store.VisibilityHumanFacing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := visibility(c.in); got != c.want {
				t.Errorf("visibility(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
