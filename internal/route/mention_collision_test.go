package route

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestANameOutranksABotUsername: when one loop's bot username is another
// loop's name, a mention of it reaches the loop of that name whatever
// order the loops are listed in, and the collision is logged once.
func TestANameOutranksABotUsername(t *testing.T) {
	var logged bytes.Buffer
	router := &Router{log: slog.New(slog.NewTextHandler(&logged, nil))}
	planner := &store.Loop{ID: "id-planner", Name: "planner"}
	builder := &store.Loop{ID: "id-builder", Name: "builder", TGBotUsername: "Planner"}
	everyone := func(*store.Loop) bool { return true }
	for _, loops := range [][]*store.Loop{{planner, builder}, {builder, planner}} {
		targets := router.mentionTargets(loops, []string{"planner"}, "", everyone)
		if len(targets) != 1 || targets["id-planner"] == nil {
			t.Errorf("@planner reached %v; want the loop named planner alone", targets)
		}
	}
	if count := strings.Count(logged.String(), "another loop's name"); count != 1 {
		t.Errorf("the collision was logged %d times; want once:\n%s", count, logged.String())
	}
	if targets := router.mentionTargets([]*store.Loop{planner, builder}, []string{"builder"}, "", everyone); targets["id-builder"] == nil {
		t.Errorf("@builder reached %v; want builder", targets)
	}
}
