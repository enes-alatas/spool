//go:build integration

package itest

import (
	"maps"
	"net/http"
	"strings"
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
// first message registers them as pending, as any stranger is, and reaches
// nobody.
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
	srv.allowSlackSender(slackOperator)
	if got := srv.activityWith("hello"); len(got) != 0 {
		t.Fatalf("a pending sender's message was stored: %s", dump(got))
	}
	return srv, slack
}

// allowSlackSender waits for the pending record a first message creates,
// then approves it.
func (s *server) allowSlackSender(userID string) {
	s.t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		var senders []struct {
			SlackUserID string `json:"slack_user_id"`
			Username    string `json:"username"`
		}
		s.mustJSON("GET", "/api/slack/senders", nil, &senders)
		for _, sender := range senders {
			if sender.SlackUserID == userID {
				if sender.Username != strings.ToLower(userID) {
					s.t.Errorf("sender registered as %q, want the name users.info gave", sender.Username)
				}
				s.mustJSON("POST", "/api/slack/senders/"+userID+"/allow", nil, nil)
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("slack sender %s never registered", userID)
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

// The owner's DM with a loop's app is the loop's owner_dm, and only the
// owner reaches the loop there: someone else who is allowed is still not
// the person owner_dm answers (#230).
func TestSlackOwnerDMReachesItsLoop(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	if resp, body := srv.do("PUT", "/api/loops/terra/owner", map[string]any{"slack_user_id": slackOperator}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set owner: %d %s", resp.StatusCode, body)
	}

	slack.pushMessage(t, slackAppToken, "im", "D0OPER", slackOperator, "just between us", "1727600000.000200")
	srv.waitInput("terra", "message from @u0oper via slack dm · owner_dm", "just between us")
	stored := srv.activityWith("just between us")
	if len(stored) != 1 || stored[0].Conversation != "owner_dm" || stored[0].ConversationLoopID == "" ||
		len(stored[0].DeliveredTo) != 1 {
		t.Fatalf("stored %s", dump(stored))
	}

	// Another allowed sender's DM to the same app reaches nobody.
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, "U0ALICE", "hi all", "1727600000.000300")
	srv.allowSlackSender("U0ALICE")
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "psst terra", "1727600000.000400")
	slack.waitAck(t, slackAppToken, slackAppToken+":D0ALICE:1727600000.000400")
	time.Sleep(time.Second)
	if got := srv.activityWith("psst terra"); len(got) != 0 {
		t.Fatalf("a DM from someone other than the owner was stored: %s", dump(got))
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
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "hello", "1727600000.000001")
	srv.allowSlackSender(slackOperator)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "binding", "1727600000.000002")
	named := func(link slackStatus) bool {
		return link.ChannelID == slackChannel && link.ChannelName == strings.ToLower(slackChannel)
	}
	srv.waitSlackLink("terra", named)

	srv.stop()
	srv = startServerArgs(t, dataDir, "--runtime", "bare", "--slack-api-base", slack.srv.URL)
	srv.waitSlackLink("terra", named)
}
