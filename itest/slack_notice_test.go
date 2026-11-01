//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A sender Spool does not allow is told nothing, in a DM or the channel,
// and registered nowhere (ADR-0051): a reply to a stranger is spam, and
// tells them the app is there. An allowed sender is turned away from no
// loop's DM (ADR-0049), so nothing tells them a loop is not theirs.
func TestSlackStrangerIsToldNothing(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const stranger, strangerDM = "U0STRANGER", "D0STRANGER"
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, stranger, "anyone?", "1727600000.000100")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "let me in", "1727600000.000101")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "link-aaaaaaaaaa", "1727600000.000102")

	operatorDM := "D" + slackOperator
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, "hi terra", "1727600000.000110")
	srv.waitForMessage("hi terra")

	// a notice is paced a second behind the message before it
	time.Sleep(2500 * time.Millisecond)
	for _, post := range slack.postsTaken() {
		if post.Channel == strangerDM || strings.Contains(post.Text, "direct messages only from") {
			t.Errorf("a post went to a stranger, or turned an allowed sender away: %+v", post)
		}
	}
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	for _, sender := range senders {
		if sender.SlackUserID == stranger {
			t.Errorf("the stranger was registered: %s", dump(senders))
		}
	}
	for _, text := range []string{"anyone?", "let me in", "link-aaaaaaaaaa"} {
		if got := srv.activityWith(text); len(got) != 0 {
			t.Errorf("a stranger's %q was stored: %s", text, dump(got))
		}
	}
}

// countPosts is how many posts any app has made containing text.
func countPosts(slack *fakeSlack, text string) int {
	n := 0
	for _, post := range slack.postsTaken() {
		if strings.Contains(post.Text, text) {
			n++
		}
	}
	return n
}

// A Slack owner hears of a refused Claude login in their DM with a loop's
// app, which opens it, once for the outage however many of their loops it
// stopped, and once more when it works again, from the same app (#419,
// #230).
func TestSlackOwnerIsToldOfARefusedLoginOnce(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	appOf := map[string]string{slackBotToken: "terra", slackMiloBotToken: "milo"}
	for _, name := range []string{"terra", "milo"} {
		srv.mustJSON("PUT", "/api/loops/"+name+"/owner", map[string]any{"user": slackUser(slackOperator)}, nil)
		srv.message(name, "hi "+name)
		srv.waitTurn(name, 30*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "hi "+name)
		})
	}

	expired := filepath.Join(srv.fkState, "login-expired")
	if err := os.MkdirAll(srv.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.message(name, "are you there, "+name)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.waitState(name, "workstation_down", 30*time.Second)
	}
	const refusedText = "the Claude login was refused"
	told := slack.waitPost(t, refusedText)
	teller := appOf[told.Token]
	if told.Channel != "D"+slackOperator || !strings.Contains(told.Text, teller+" has stopped") {
		t.Fatalf("the owner was told %+v, want the notice naming the loop whose app posted it, in the owner's DM", told)
	}
	time.Sleep(2 * time.Second)
	if n := countPosts(slack, refusedText); n != 1 {
		t.Fatalf("the owner was told of one refused login %d times, want once", n)
	}
	if !srv.hasEvent(teller, "owner_notice", 5*time.Second) {
		t.Fatalf("%s told the owner without an owner_notice on its timeline", teller)
	}

	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"terra", "milo"} {
		srv.waitTurn(name, 60*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "are you there, "+name)
		})
	}
	const worksText = "the Claude login works again"
	if works := slack.waitPost(t, worksText); works.Token != told.Token || works.Channel != told.Channel {
		t.Fatalf("the all-clear was posted as %+v, want %s's app in the DM it told", works, teller)
	}
	time.Sleep(2 * time.Second)
	if n := countPosts(slack, worksText); n != 1 {
		t.Fatalf("the owner was told the login works again %d times, want once", n)
	}
}

// A Slack sender whose message a loop dropped is told so once, in the
// thread of that message, on the loop's own app (#1205).
func TestSlackSenderIsToldOfADroppedMessage(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	hub, slack := startSlackFleetWith(t, map[string]any{"workspace_path": workspace, "workspace_mode": "dir"})
	hub.waitTurn("terra", firstTurnWait, func(turn) bool { return true })
	hub.waitState("terra", "asleep", 30*time.Second)
	setScript(t, workspace, "!crash\n")

	operatorDM := "D" + slackOperator
	const text, ts = "the poisoned question", "1727600000.000200"
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, text, ts)
	if !hub.hasEvent("terra", "batch_dropped", 2*time.Minute) {
		t.Fatalf("nothing was dropped; turns: %s", dump(hub.turns("terra")))
	}
	const noticeText = "couldn't take this message"
	notice := slack.waitPost(t, noticeText)
	if notice.Token != slackBotToken || notice.Channel != operatorDM || notice.ThreadTS != ts {
		t.Fatalf("notice posted as %+v, want terra's app in the thread of %s in %s", notice, ts, operatorDM)
	}
	// give a second notice the time to go out, if it were going to
	time.Sleep(2 * time.Second)
	if told := countPosts(slack, noticeText); told != 1 {
		t.Fatalf("the sender was told of one drop %d times, want once", told)
	}
}
