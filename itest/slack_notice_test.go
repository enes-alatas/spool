//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A person the app turns away is told why, once, in the DM they wrote in
// (#230): a sender Spool has not allowed gets their pairing code, and an
// allowed sender who DMs a loop that is not theirs learns whose it is. A
// pending sender in the channel gets no answer there, since the code is
// theirs to hand the operator.
func TestSlackTurnedAwaySenderIsToldWhy(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const stranger, strangerDM = "U0STRANGER", "D0STRANGER"
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, stranger, "anyone?", "1727600000.000100")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "let me in", "1727600000.000101")
	slack.pushMessage(t, slackAppToken, "im", strangerDM, stranger, "please", "1727600000.000102")

	const pairText = "Your pairing code is"
	told := slack.waitPost(t, pairText)
	if told.Token != slackBotToken || told.Channel != strangerDM || !strings.Contains(told.Text, slackPairCode(srv, stranger)) {
		t.Fatalf("the stranger was told %+v, want their code from terra's app in their DM", told)
	}

	const notOwnerText = "reads direct messages only from"
	operatorDM := "D" + slackOperator
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, "hi terra", "1727600000.000110")
	slack.pushMessage(t, slackAppToken, "im", operatorDM, slackOperator, "hello?", "1727600000.000111")
	if told := slack.waitPost(t, notOwnerText); told.Channel != operatorDM ||
		told.Text != "Spool: terra reads direct messages only from its owner for now. Reach it in its channel instead." {
		t.Fatalf("a non-owner was told %+v", told)
	}

	// each notice is said once; the second message of each pair, if it
	// were told again, is paced a second behind the first
	time.Sleep(2500 * time.Millisecond)
	for _, text := range []string{pairText, notOwnerText} {
		if n := countPosts(slack, text); n != 1 {
			t.Errorf("%q was posted %d times, want once: %s", text, n, dump(slack.postsTaken()))
		}
	}
}

// slackPairCode is the pairing code the hub gave a Slack sender.
func slackPairCode(srv *server, userID string) string {
	srv.t.Helper()
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
		PairCode    string `json:"pair_code"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	for _, sender := range senders {
		if sender.SlackUserID == userID && sender.PairCode != "" {
			return sender.PairCode
		}
	}
	srv.t.Fatalf("no pairing code for %s: %s", userID, dump(senders))
	return ""
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
