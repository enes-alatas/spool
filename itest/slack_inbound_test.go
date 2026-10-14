//go:build integration

package itest

import (
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic credentials for a second loop's app, in the same workspace as
// terra's.
const (
	slackMiloBotToken = "xoxb-synthetic-milo"
	slackMiloAppToken = "xapp-synthetic-milo"
	slackOperator     = "U0OPER"
	slackChannel      = "C0FLEET"
)

var miloBot = slackBot{UserID: "U0MILO", Name: "milo", TeamID: "T0ACME", TeamName: "Acme"}

// startSlackFleet brings up terra and milo, each with its own Slack app in
// one workspace, both connected, and the operator allowed. The operator's
// first message, before they link, reaches nobody and registers nothing,
// as any stranger's does.
func startSlackFleet(t *testing.T) (*server, *fakeSlack) {
	t.Helper()
	return startSlackFleetWith(t, nil)
}

// startSlackFleetWith is startSlackFleet with terra created with overrides.
func startSlackFleetWith(t *testing.T, terraOverrides map[string]any) (*server, *fakeSlack) {
	t.Helper()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	slack.addApp(slackMiloBotToken, slackMiloAppToken, miloBot)
	srv := startSlackServer(t, slack)
	terra := slackPair(slackAppToken, slackBotToken)
	maps.Copy(terra, terraOverrides)
	srv.createLoop("terra", terra)
	srv.createLoop("milo", slackPair(slackMiloAppToken, slackMiloBotToken))
	for _, name := range []string{"terra", "milo"} {
		srv.waitSlackLink(name, func(link slackStatus) bool { return link.Bridge.Connected })
	}

	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "hello", "1727600000.000001")
	srv.allowSlackSender(slack, slackAppToken, slackOperator)
	if got := srv.activityWith("hello"); len(got) != 0 {
		t.Fatalf("a stranger's message was stored: %s", dump(got))
	}
	return srv, slack
}

// slackLinkTS numbers the DMs allowSlackSender sends, so no two share a ts.
var slackLinkTS atomic.Int64

// slackUser is the hub user allowSlackSender links userID to.
func slackUser(userID string) string { return "slack-" + strings.ToLower(userID) }

// allowSlackSender makes userID an allowed Slack sender the one way there
// is (ADR-0051): a hub user of their own, named for them, asks for a Slack
// link code, and userID sends it to app in a DM and confirms it. The
// sender is registered under the name users.info gives. It answers with
// that user's session.
func (s *server) allowSlackSender(slack *fakeSlack, app, userID string) string {
	s.t.Helper()
	name := slackUser(userID)
	session := s.signedInAs(name, "member")
	dm := "D" + userID
	slack.pushMessage(s.t, app, "im", dm, userID, s.linkCode(session, "slack"),
		fmt.Sprintf("1727690000.%06d", slackLinkTS.Add(1)))
	slack.waitPost(s.t, "Slack account to "+name)
	slack.pushMessage(s.t, app, "im", dm, userID, "YES", fmt.Sprintf("1727690000.%06d", slackLinkTS.Add(1)))
	slack.waitPost(s.t, "now linked to "+name)
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
		Username    string `json:"username"`
		Status      string `json:"status"`
	}
	s.mustJSON("GET", "/api/slack/senders", nil, &senders)
	for _, sender := range senders {
		if sender.SlackUserID == userID {
			if sender.Status != "allowed" || sender.Username != strings.ToLower(userID) {
				s.t.Errorf("sender registered as %+v, want allowed under the name users.info gave", sender)
			}
			return session
		}
	}
	s.t.Fatalf("slack sender %s was never registered", userID)
	return ""
}

// waitInput waits for one of a loop's turns to be handed an envelope
// containing every one of want.
func (s *server) waitInput(name string, want ...string) {
	s.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		for _, inputs := range s.turnInputs(name) {
			for _, envelope := range inputs {
				found := true
				for _, part := range want {
					found = found && strings.Contains(envelope, part)
				}
				if found {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("%s was never handed an envelope with %q; inputs: %s", name, want, dump(s.turnInputs(name)))
}

// A message in the channel reaches the hub once, although both loops' apps
// hear it, and every loop it mentions by its app is delivered it (#230,
// ADR-0020). Slack writes a mention as the app's user id; the loop reads
// its own name.
func TestSlackChannelMessageIngestedOnceAndDeliveredToMentions(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)

	const ts = "1727600000.000100"
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushMessage(t, app, "channel", slackChannel, slackOperator, "<@U0TERRA> <@U0MILO> status please", ts)
	}
	const text = "@terra @milo status please"
	srv.waitForMessage(text)
	for _, name := range []string{"terra", "milo"} {
		srv.waitInput(name, "message from @u0oper via slack · group", text)
	}

	// give a second ingester time to add its own row
	time.Sleep(time.Second)
	stored := srv.activityWith(text)
	if len(stored) != 1 {
		t.Fatalf("channel message stored %d times, want 1: %s", len(stored), dump(stored))
	}
	if stored[0].Origin != "slack-channel" || stored[0].Conversation != "group" ||
		stored[0].Mirror != "mirrored" || len(stored[0].DeliveredTo) != 2 {
		t.Fatalf("stored %s", dump(stored[0]))
	}

	// The apps heard it in the channel an allowed sender first spoke in,
	// and are bound there.
	for _, name := range []string{"terra", "milo"} {
		if status := srv.waitSlackLink(name, func(slackStatus) bool { return true }); status.ChannelID != slackChannel {
			t.Errorf("%s bound to %q, want %s", name, status.ChannelID, slackChannel)
		}
	}
}

// The owner's DM with a loop's app is the loop's owner_dm (#230). Someone
// else who is allowed reaches the loop in a conversation of their own,
// dm:<name>, never the owner's (ADR-0049).
func TestSlackOwnerDMReachesItsLoop(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"user": slackUser(slackOperator)}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}

	slack.pushMessage(t, slackAppToken, "im", "D0OPER", slackOperator, "just between us", "1727600000.000200")
	srv.waitInput("terra", "message from @u0oper via slack dm · owner_dm", "just between us")
	stored := srv.activityWith("just between us")
	if len(stored) != 1 || stored[0].Conversation != "owner_dm" || stored[0].ConversationLoopID == "" ||
		len(stored[0].DeliveredTo) != 1 {
		t.Fatalf("stored %s", dump(stored))
	}

	// Another allowed sender's DM to the same app is their own conversation.
	srv.allowSlackSender(slack, slackAppToken, "U0ALICE")
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "psst terra", "1727600000.000400")
	srv.waitInput("terra", "message from @u0alice via slack dm · dm:u0alice", "psst terra")
	if got := srv.activityWith("psst terra"); len(got) != 1 || got[0].Conversation != "dm:u0alice" ||
		got[0].ConversationLoopID != stored[0].ConversationLoopID {
		t.Fatalf("a DM from someone other than the owner was stored as %s", dump(got))
	}
}

// The link status names the channel a loop's app is bound to, as Slack
// names it, both when the app binds and when its link comes back up after
// a restart (#230).
func TestSlackLinkNamesItsChannel(t *testing.T) {
	t.Parallel()
	slack := startFakeSlack(t)
	slack.addApp(slackBotToken, slackAppToken, terraBot)
	dataDir := t.TempDir()
	srv := startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.createLoop("terra", slackPair(slackAppToken, slackBotToken))
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.Bridge.Connected })
	srv.allowSlackSender(slack, slackAppToken, slackOperator)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "binding", "1727600000.000002")
	named := func(link slackStatus) bool {
		return link.ChannelID == slackChannel && link.ChannelName == strings.ToLower(slackChannel)
	}
	srv.waitSlackLink("terra", named)

	srv.stop()
	srv = startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.waitSlackLink("terra", named)
}

// Two people's DMs with a loop's app never share a turn (ADR-0049). Slack
// DMs carry no chat id to batch by, so the conversation is what keeps the
// owner's and another sender's apart.
func TestSlackTwoPeoplesDMsAreTwoTurns(t *testing.T) {
	t.Parallel()
	// the first line answers the wake every loop takes as the hub starts
	srv, slack := startSlackFleetWith(t, map[string]any{"workspace_path": workspaceWithScript(t, "!ctx 0\n!hang 3\n!echo\n")})
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"user": slackUser(slackOperator)}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}
	srv.allowSlackSender(slack, slackAppToken, "U0ALICE")

	slack.pushMessage(t, slackAppToken, "im", "D"+slackOperator, slackOperator, "start hanging", "1727600000.000400")
	srv.waitState("terra", "busy", 10*time.Second)
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "from alice", "1727600000.000401")
	slack.pushMessage(t, slackAppToken, "im", "D"+slackOperator, slackOperator, "from op", "1727600000.000402")
	srv.waitTurn("terra", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "from op") })
	srv.waitTurn("terra", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "from alice") })
	for id, envelopes := range srv.turnInputs("terra") {
		input := strings.Join(envelopes, "\n")
		if strings.Contains(input, "from alice") && strings.Contains(input, "from op") {
			t.Fatalf("turn %s was given two people's DMs: %q", id, input)
		}
	}
}

// A link code said in a channel is just words, as in a Telegram group:
// nothing claims it, the stranger who said it stays one, and the code
// still works in a DM (ADR-0051, #790).
func TestASlackLinkCodeInAChannelLinksNobody(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	bea := srv.signedInAs("bea", "member")
	const stranger = "U0EVE"

	code := srv.linkCode(bea, "slack")
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, stranger, code, "1727600000.000500")
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "after the code in the channel", "1727600000.000501")
	srv.waitForMessage("after the code in the channel")
	for _, post := range slack.postsTaken() {
		if strings.Contains(post.Text, "Slack account to bea") {
			t.Fatalf("a code in a channel was answered: %+v", post)
		}
	}
	if waiting := srv.myLinks(bea, "/api/me/links/waiting"); len(waiting) != 0 {
		t.Fatalf("a code in a channel left bea waiting on %+v", waiting)
	}
	if got := srv.activityWith(code); len(got) != 0 {
		t.Fatalf("a stranger's code in a channel was stored: %s", dump(got))
	}

	slack.pushMessage(t, slackAppToken, "im", "D0EVE", stranger, code, "1727600000.000502")
	slack.waitPost(t, "Slack account to bea")
}
