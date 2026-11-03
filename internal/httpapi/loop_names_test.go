package httpapi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// TestANameInUseIsRefused: a new loop's name that is, ignoring case,
// another loop's bot username or the name of a person the hub allows is
// refused, saying whose it is; a pending sender's isn't in use, and
// another loop's name and @all are left to their own refusals. A bot
// username that is another loop's name is refused; the loop's own name
// is not another's.
func TestANameInUseIsRefused(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	planner := teamLoop("id-planner", "planner")
	planner.TGBotUsername = "Planner_Bot"
	if err := db.Loops().Create(ctx, planner); err != nil {
		t.Fatal(err)
	}
	for _, sender := range []*store.TGSender{{TGUserID: 1, Username: "Ada", Status: "pending"}, {TGUserID: 2, Username: "stranger", Status: "pending"}} {
		if err := db.TGSenders().Create(ctx, sender); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.TGSenders().SetStatus(ctx, 1, store.SenderAllowed, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.SlackSenders().Create(ctx, &store.SlackSender{SlackUserID: "U1", Username: "grace", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SlackSenders().SetStatus(ctx, "U1", store.SenderAllowed, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Users().Create(ctx, &store.User{ID: "u1", Name: "linus", Role: store.RoleMember}); err != nil {
		t.Fatal(err)
	}
	names, err := (&Server{Store: db}).namesInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for name, whose := range map[string]string{
		"planner_bot": "the loop planner's bot",
		"ada":         "Telegram username of a person",
		"grace":       "Slack username of a person",
		"Linus":       "control room user",
	} {
		refusal := names.nameRefusal(name)
		if refusal == nil || refusal.status != 409 || refusal.code != codeNameInUse || !strings.Contains(refusal.msg, whose) {
			t.Errorf("name %q: refusal %+v; want a 409 %s naming %q", name, refusal, codeNameInUse, whose)
		}
		if !names.taken(name) {
			t.Errorf("name %q: not taken for a team draft", name)
		}
	}
	for _, name := range []string{"stranger", "builder", "planner", "all"} {
		if refusal := names.nameRefusal(name); refusal != nil {
			t.Errorf("name %q: refused %+v; want it left to the store or no refusal", name, refusal)
		}
	}
	if !names.taken("planner") || !names.taken("all") || names.taken("stranger") {
		t.Error("a team draft's taken names: want planner and all, not a pending sender")
	}

	if refusal := names.botRefusal("PLANNER", "id-new"); refusal == nil || refusal.code != codeNameInUse || !strings.Contains(refusal.msg, "the loop planner") {
		t.Errorf("bot @PLANNER for another loop: refusal %+v; want one naming the loop planner", refusal)
	}
	for _, username := range []string{"planner", "fresh_bot"} {
		loopID := "id-new"
		if username == "planner" {
			loopID = "id-planner"
		}
		if refusal := names.botRefusal(username, loopID); refusal != nil {
			t.Errorf("bot @%s for %s: refused %+v", username, loopID, refusal)
		}
	}
}
